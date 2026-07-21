package com.codex.onec.edt.mcp;

import java.lang.reflect.InvocationTargetException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Objects;
import java.util.concurrent.atomic.AtomicReference;
import java.util.regex.Pattern;

import org.eclipse.core.resources.IProject;
import org.eclipse.core.resources.ResourcesPlugin;
import org.eclipse.core.runtime.CoreException;
import org.eclipse.core.runtime.IProgressMonitor;
import org.eclipse.core.runtime.IStatus;
import org.eclipse.core.runtime.NullProgressMonitor;
import org.eclipse.core.runtime.Status;
import org.eclipse.core.runtime.jobs.Job;

import com._1c.g5.v8.dt.core.platform.IBmModelManager;
import com._1c.g5.v8.dt.core.platform.IConfigurationProject;
import com._1c.g5.v8.dt.core.platform.IV8Project;
import com._1c.g5.v8.dt.core.platform.IV8ProjectManager;
import com._1c.g5.v8.dt.import_.IImportOperation;
import com._1c.g5.v8.dt.import_.IImportOperationFactory;
import com._1c.g5.v8.dt.platform.version.Version;
import com._1c.g5.wiring.ServiceSupplier;

/** Imports Designer XML for an EPF/ERF into a tightly scoped external-object EDT project. */
final class EdtExternalObjectService {
    private static final Pattern VALID_PROJECT = Pattern.compile("[\\p{L}_][\\p{L}\\p{N}_.-]{0,119}");
    private static final String EXTERNAL_NATURE = "com._1c.g5.v8.dt.core.V8ExternalObjectsNature";
    private static final long IMPORT_TIMEOUT_MS = 300_000L;

    private final String baseProjectName;
    private final String managedPrefix = System.getProperty("onec.mcp.externalProjectPrefix", "CodexExt_");
    private final Path sourceRoot = Path.of(System.getProperty("onec.mcp.externalRoot",
        System.getProperty("user.home")))
        .toAbsolutePath().normalize();
    private final ServiceSupplier<IImportOperationFactory> importOperationFactory;
    private final ServiceSupplier<IV8ProjectManager> v8ProjectManager;
    private final ServiceSupplier<IBmModelManager> modelManager;

    EdtExternalObjectService(String baseProjectName,
        ServiceSupplier<IImportOperationFactory> importOperationFactory,
        ServiceSupplier<IV8ProjectManager> v8ProjectManager,
        ServiceSupplier<IBmModelManager> modelManager) {
        this.baseProjectName = Objects.requireNonNull(baseProjectName);
        this.importOperationFactory = Objects.requireNonNull(importOperationFactory);
        this.v8ProjectManager = Objects.requireNonNull(v8ProjectManager);
        this.modelManager = Objects.requireNonNull(modelManager);
    }

    Map<String, Object> importXml(String projectName, String sourceXml, String platformVersion) {
        validateProjectName(projectName);
        Path source = resolveSource(sourceXml);
        Version version = parseVersion(platformVersion);
        IConfigurationProject baseProject = requireBaseProject();
        IProject project = ResourcesPlugin.getWorkspace().getRoot().getProject(projectName);
        boolean existed = project.exists();
        if (existed) {
            requireManagedExternalProject(project);
        }

        IImportOperation operation = importOperationFactory.get().createImportExternalObjectOperation(
            projectName, version, source, baseProject);
        operation.setRefreshProject(true);
        AtomicReference<Throwable> failure = new AtomicReference<>();
        Job job = new Job("Codex: import external object " + projectName) {
            @Override
            protected IStatus run(IProgressMonitor monitor) {
                try {
                    ResourcesPlugin.getWorkspace().run(pm -> runImport(operation, pm), monitor);
                } catch (Throwable error) {
                    failure.set(error);
                }
                return Status.OK_STATUS;
            }
        };
        job.setUser(false);
        job.schedule();
        try {
            job.join(IMPORT_TIMEOUT_MS, new NullProgressMonitor());
        } catch (InterruptedException error) {
            Thread.currentThread().interrupt();
            throw new EdtMetadataService.BridgeException(500, "External object import was interrupted");
        }
        if (job.getState() != Job.NONE) {
            job.cancel();
            throw new EdtMetadataService.BridgeException(500,
                "External object import timed out after " + (IMPORT_TIMEOUT_MS / 1000) + " seconds");
        }
        if (failure.get() != null) {
            throw new EdtMetadataService.BridgeException(500,
                "External object import failed: " + safeMessage(failure.get()));
        }
        IStatus status = operation.getStatus();
        if (status != null && status.matches(IStatus.ERROR | IStatus.CANCEL)) {
            throw new EdtMetadataService.BridgeException(500,
                "External object import failed: " + statusMessages(status));
        }

        project = ResourcesPlugin.getWorkspace().getRoot().getProject(projectName);
        requireManagedExternalProject(project);
        modelManager.get().waitModelSynchronization(project);

        Map<String, Object> result = new LinkedHashMap<>();
        result.put("success", true);
        result.put("action", existed ? "updated" : "created");
        result.put("project", projectName);
        result.put("base_project", baseProjectName);
        result.put("source_xml", sourceRoot.relativize(source).toString());
        result.put("platform_version", version.toString());
        result.put("project_open", project.isOpen());
        result.put("status", status == null ? "OK" : statusMessages(status));
        result.put("database_changed", false);
        return result;
    }

    private static void runImport(IImportOperation operation, IProgressMonitor monitor) throws CoreException {
        try {
            operation.run(monitor);
        } catch (InvocationTargetException error) {
            Throwable cause = error.getCause() == null ? error : error.getCause();
            throw new CoreException(new Status(IStatus.ERROR, Activator.PLUGIN_ID,
                "External object XML import failed", cause));
        } catch (InterruptedException error) {
            Thread.currentThread().interrupt();
            throw new CoreException(new Status(IStatus.CANCEL, Activator.PLUGIN_ID,
                "External object XML import interrupted", error));
        }
    }

    private IConfigurationProject requireBaseProject() {
        IProject project = ResourcesPlugin.getWorkspace().getRoot().getProject(baseProjectName);
        if (!project.exists() || !project.isOpen()) {
            throw new EdtMetadataService.BridgeException(409,
                "Base EDT project is not open: " + baseProjectName);
        }
        IV8Project v8Project = v8ProjectManager.get().getProject(project);
        if (!(v8Project instanceof IConfigurationProject configurationProject)) {
            throw new EdtMetadataService.BridgeException(409,
                "Base EDT project is not a configuration project: " + baseProjectName);
        }
        return configurationProject;
    }

    private void validateProjectName(String projectName) {
        if (!projectName.startsWith(managedPrefix) || projectName.length() == managedPrefix.length()
            || !VALID_PROJECT.matcher(projectName).matches()) {
            throw new EdtMetadataService.BridgeException(400,
                "project_name must be a valid EDT project name starting with " + managedPrefix);
        }
        if (projectName.equals(baseProjectName)) {
            throw new EdtMetadataService.BridgeException(400, "The fixed base project cannot be an import target");
        }
    }

    private Path resolveSource(String relativeSource) {
        Path supplied;
        try {
            supplied = Path.of(relativeSource);
        } catch (RuntimeException error) {
            throw new EdtMetadataService.BridgeException(400, "source_xml is not a valid path");
        }
        if (supplied.isAbsolute()) {
            throw new EdtMetadataService.BridgeException(400, "source_xml must be relative to the configured root");
        }
        Path source = sourceRoot.resolve(supplied).normalize();
        if (!source.startsWith(sourceRoot) || !source.getFileName().toString().toLowerCase().endsWith(".xml")) {
            throw new EdtMetadataService.BridgeException(400,
                "source_xml must be an XML file inside the configured root");
        }
        try {
            Path realRoot = sourceRoot.toRealPath();
            Path realSource = source.toRealPath();
            if (!realSource.startsWith(realRoot) || !Files.isRegularFile(realSource)) {
                throw new EdtMetadataService.BridgeException(400,
                    "source_xml must be a regular XML file inside the configured root");
            }
            return realSource;
        } catch (EdtMetadataService.BridgeException error) {
            throw error;
        } catch (Exception error) {
            throw new EdtMetadataService.BridgeException(400,
                "source_xml is unavailable inside the configured root: " + relativeSource);
        }
    }

    private static Version parseVersion(String value) {
        String effective = value == null || value.isBlank() ? "8.3.27" : value.trim();
        try {
            return Version.create(effective);
        } catch (RuntimeException error) {
            throw new EdtMetadataService.BridgeException(400,
                "platform_version is invalid: " + effective);
        }
    }

    private static void requireManagedExternalProject(IProject project) {
        try {
            if (!project.exists() || !project.isOpen() || !project.hasNature(EXTERNAL_NATURE)) {
                throw new EdtMetadataService.BridgeException(409,
                    "Existing target is not an open external-object EDT project: " + project.getName());
            }
        } catch (CoreException error) {
            throw new EdtMetadataService.BridgeException(500,
                "Cannot inspect external-object EDT project: " + safeMessage(error));
        }
    }

    private static String statusMessages(IStatus status) {
        List<String> messages = new ArrayList<>();
        collectStatus(status, messages);
        return messages.isEmpty() ? "OK" : String.join("; ", messages);
    }

    private static void collectStatus(IStatus status, List<String> messages) {
        if (status.getMessage() != null && !status.getMessage().isBlank()) {
            messages.add(status.getMessage());
        }
        for (IStatus child : status.getChildren()) {
            collectStatus(child, messages);
        }
    }

    private static String safeMessage(Throwable error) {
        Throwable current = error;
        while (current.getCause() != null) {
            current = current.getCause();
        }
        String message = current.getMessage();
        return message == null || message.isBlank() ? current.getClass().getSimpleName() : message;
    }
}
