package com.codex.onec.edt.mcp;

import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.Collection;
import java.util.Comparator;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Objects;
import java.util.Optional;
import java.util.UUID;

import org.eclipse.core.resources.IProject;
import org.eclipse.core.resources.ResourcesPlugin;

import com._1c.g5.v8.dt.platform.services.core.infobases.IInfobaseAssociation;
import com._1c.g5.v8.dt.platform.services.core.infobases.IInfobaseAssociationManager;
import com._1c.g5.v8.dt.platform.services.core.infobases.IInfobaseManager;
import com._1c.g5.v8.dt.platform.services.core.infobases.InfobaseAssociationContext;
import com._1c.g5.v8.dt.platform.services.core.infobases.InfobaseAssociationSettings;
import com._1c.g5.v8.dt.platform.services.model.FileConnectionString;
import com._1c.g5.v8.dt.platform.services.model.Group;
import com._1c.g5.v8.dt.platform.services.model.IConnectionString;
import com._1c.g5.v8.dt.platform.services.model.InfobaseReference;
import com._1c.g5.v8.dt.platform.services.model.ModelFactory;
import com._1c.g5.v8.dt.platform.services.model.Section;
import com._1c.g5.v8.dt.platform.services.model.ServerConnectionString;
import com._1c.g5.wiring.ServiceSupplier;

final class EdtInfobaseService {
    private final String projectName;
    private final ServiceSupplier<IInfobaseManager> infobaseManager;
    private final ServiceSupplier<IInfobaseAssociationManager> associationManager;

    EdtInfobaseService(String projectName, ServiceSupplier<IInfobaseManager> infobaseManager,
        ServiceSupplier<IInfobaseAssociationManager> associationManager) {
        this.projectName = Objects.requireNonNull(projectName);
        this.infobaseManager = Objects.requireNonNull(infobaseManager);
        this.associationManager = Objects.requireNonNull(associationManager);
    }

    Map<String, Object> list() {
        IProject project = project();
        Optional<IInfobaseAssociation> association = associationManager.get().getAssociation(project);
        Collection<InfobaseReference> bound = association.map(IInfobaseAssociation::getInfobases).orElse(List.of());
        InfobaseReference defaultInfobase = association.map(IInfobaseAssociation::getDefaultInfobase).orElse(null);
        List<Map<String, Object>> items = new ArrayList<>();
        for (InfobaseReference reference : allInfobases()) {
            Map<String, Object> item = describe(reference);
            item.put("bound", bound.contains(reference));
            item.put("default", reference.equals(defaultInfobase));
            items.add(item);
        }
        items.sort(Comparator.comparing(value -> String.valueOf(value.get("name")), String.CASE_INSENSITIVE_ORDER));
        Map<String, Object> result = baseResult();
        result.put("infobases", items);
        result.put("count", items.size());
        return result;
    }

    Map<String, Object> bind(String name, boolean register, String kind, String filePath,
        String server, String reference, String version, boolean alreadySynchronized,
        boolean setDefault, boolean confirm) {
        requireConfirm(confirm);
        if (name == null || name.isBlank()) {
            throw new EdtMetadataService.BridgeException(400, "infobase_name is required");
        }
        IInfobaseManager manager = infobaseManager.get();
        Optional<InfobaseReference> existing = manager.findInfobaseByName(name.trim());
        boolean created = false;
        InfobaseReference infobase;
        if (existing.isPresent()) {
            infobase = existing.get();
        } else {
            if (!register) {
                throw new EdtMetadataService.BridgeException(404,
                    "Infobase is not registered in EDT; set register=true and provide a connection");
            }
            infobase = createReference(name.trim(), kind, filePath, server, reference, version);
            manager.add(infobase, "");
            created = true;
        }
        IProject project = project();
        try {
            Optional<IInfobaseAssociation> current = associationManager.get().getAssociation(project);
            if (current.isEmpty() || !current.get().getInfobases().contains(infobase)) {
                InfobaseAssociationSettings settings = alreadySynchronized
                    ? InfobaseAssociationSettings.alreadySynchronized()
                    : InfobaseAssociationSettings.notSynchronized();
                associationManager.get().associate(project, infobase, settings);
            }
            if (setDefault) {
                associationManager.get().setDefaultInfobase(project, infobase, InfobaseAssociationContext.empty());
            }
        } catch (RuntimeException exception) {
            if (created) {
                try {
                    manager.delete(infobase);
                } catch (RuntimeException ignored) {
                    // Preserve the original association failure.
                }
            }
            throw exception;
        }
        Map<String, Object> result = baseResult();
        result.put("operation", "bind");
        result.put("registered", created);
        result.put("infobase", describe(infobase));
        result.put("default", setDefault);
        result.put("already_synchronized", alreadySynchronized);
        return result;
    }

    Map<String, Object> unbind(String name, boolean unregister, boolean confirm) {
        requireConfirm(confirm);
        if (name == null || name.isBlank()) {
            throw new EdtMetadataService.BridgeException(400, "infobase_name is required");
        }
        InfobaseReference infobase = infobaseManager.get().findInfobaseByName(name.trim())
            .orElseThrow(() -> new EdtMetadataService.BridgeException(404, "Infobase is not registered in EDT"));
        IProject project = project();
        Optional<IInfobaseAssociation> association = associationManager.get().getAssociation(project);
        if (association.isPresent() && association.get().getInfobases().contains(infobase)) {
            associationManager.get().dissociate(project, infobase, InfobaseAssociationContext.empty());
        }
        boolean removed = false;
        if (unregister) {
            if (associationManager.get().getAssociation(infobase).isPresent()) {
                throw new EdtMetadataService.BridgeException(409,
                    "Infobase is still associated with another EDT project and cannot be unregistered");
            }
            infobaseManager.get().delete(infobase);
            removed = true;
        }
        Map<String, Object> result = baseResult();
        result.put("operation", "unbind");
        result.put("infobase_name", name.trim());
        result.put("unregistered", removed);
        return result;
    }

    private InfobaseReference createReference(String name, String kind, String filePath,
        String server, String reference, String version) {
        InfobaseReference result = ModelFactory.eINSTANCE.createInfobaseReference();
        result.setName(name);
        result.setUuid(UUID.randomUUID());
        result.setShowInList(true);
        result.setExternal(false);
        result.setVersion(version == null ? "" : version.trim());
        if (kind == null || kind.isBlank() || "file".equalsIgnoreCase(kind)) {
            if (filePath == null || filePath.isBlank()) {
                throw new EdtMetadataService.BridgeException(400, "file_path is required for a file infobase");
            }
            Path path = Path.of(filePath).toAbsolutePath().normalize();
            if (!Files.isDirectory(path)) {
                throw new EdtMetadataService.BridgeException(400, "file_path must be an existing directory");
            }
            FileConnectionString connection = ModelFactory.eINSTANCE.createFileConnectionString();
            connection.setFile(path.toString());
            result.setConnectionString(connection);
        } else if ("server".equalsIgnoreCase(kind)) {
            if (server == null || server.isBlank() || reference == null || reference.isBlank()) {
                throw new EdtMetadataService.BridgeException(400,
                    "server and reference are required for a server infobase");
            }
            ServerConnectionString connection = ModelFactory.eINSTANCE.createServerConnectionString();
            connection.setServer(server.trim());
            connection.setReference(reference.trim());
            result.setConnectionString(connection);
        } else {
            throw new EdtMetadataService.BridgeException(400, "base_kind must be file or server");
        }
        return result;
    }

    private List<InfobaseReference> allInfobases() {
        List<InfobaseReference> result = new ArrayList<>();
        for (Section section : infobaseManager.get().getAll()) {
            collect(section, result);
        }
        return result;
    }

    private static void collect(Section section, List<InfobaseReference> result) {
        if (section instanceof InfobaseReference reference) {
            result.add(reference);
        } else if (section instanceof Group group) {
            for (Section child : group.getSubsections()) {
                collect(child, result);
            }
        }
    }

    private static Map<String, Object> describe(InfobaseReference reference) {
        Map<String, Object> result = new LinkedHashMap<>();
        result.put("name", reference.getName());
        result.put("uuid", String.valueOf(reference.getUuid()));
        result.put("version", reference.getVersion());
        IConnectionString connection = reference.getConnectionString();
        if (connection instanceof FileConnectionString file) {
            result.put("kind", "file");
            result.put("file_path", file.getFile());
        } else if (connection instanceof ServerConnectionString server) {
            result.put("kind", "server");
            result.put("server", server.getServer());
            result.put("reference", server.getReference());
        } else {
            result.put("kind", reference.getInfobaseType() == null ? "unknown"
                : reference.getInfobaseType().getName().toLowerCase());
        }
        return result;
    }

    private IProject project() {
        IProject project = ResourcesPlugin.getWorkspace().getRoot().getProject(projectName);
        if (!project.exists() || !project.isOpen()) {
            throw new EdtMetadataService.BridgeException(409, "Fixed EDT project is unavailable");
        }
        return project;
    }

    private Map<String, Object> baseResult() {
        Map<String, Object> result = new LinkedHashMap<>();
        result.put("project", projectName);
        result.put("database_changed", false);
        return result;
    }

    private static void requireConfirm(boolean confirm) {
        if (!confirm) {
            throw new EdtMetadataService.BridgeException(409, "confirm=true is required");
        }
    }
}
