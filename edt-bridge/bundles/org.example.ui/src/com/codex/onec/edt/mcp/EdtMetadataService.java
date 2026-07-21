package com.codex.onec.edt.mcp;

import java.nio.charset.StandardCharsets;
import java.lang.reflect.InvocationTargetException;
import java.lang.reflect.Method;
import java.lang.reflect.ParameterizedType;
import java.lang.reflect.Type;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.time.Duration;
import java.time.Instant;
import java.util.ArrayList;
import java.util.Collection;
import java.util.Collections;
import java.util.Comparator;
import java.util.HashMap;
import java.util.Iterator;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Objects;
import java.util.UUID;
import java.util.concurrent.ConcurrentHashMap;
import java.util.regex.Pattern;

import org.eclipse.core.resources.IProject;
import org.eclipse.core.resources.IResource;
import org.eclipse.core.resources.ResourcesPlugin;
import org.eclipse.core.runtime.CoreException;
import org.eclipse.core.runtime.NullProgressMonitor;
import org.eclipse.emf.ecore.EAttribute;
import org.eclipse.emf.ecore.EObject;
import org.eclipse.emf.ecore.EReference;
import org.eclipse.emf.ecore.EStructuralFeature;

import com._1c.g5.v8.dt.core.platform.IBmModelManager;
import com._1c.g5.v8.dt.core.platform.IConfigurationProvider;
import com._1c.g5.v8.dt.md.copy.IModelObjectCopySupport;
import com._1c.g5.v8.dt.md.refactoring.core.IMdRefactoringService;
import com._1c.g5.v8.dt.metadata.mdclass.Configuration;
import com._1c.g5.v8.dt.metadata.mdclass.MdObject;
import com._1c.g5.v8.dt.refactoring.core.IRefactoring;
import com._1c.g5.v8.dt.refactoring.core.IRefactoringProblem;
import com._1c.g5.wiring.ServiceSupplier;

final class EdtMetadataService {
    private static final Pattern VALID_NAME = Pattern.compile("[\\p{L}_][\\p{L}\\p{N}_]{0,79}");
    private static final Duration PLAN_TTL = Duration.ofHours(2);
    private static final Map<String, String> TYPE_ALIASES = createTypeAliases();

    private final String projectName;
    private final ServiceSupplier<IConfigurationProvider> configurationProvider;
    private final ServiceSupplier<IBmModelManager> modelManager;
    private final ServiceSupplier<IModelObjectCopySupport> copySupport;
    private final ServiceSupplier<IMdRefactoringService> refactoringService;
    private final Map<String, PreparedPlan> plans = new ConcurrentHashMap<>();

    EdtMetadataService(String projectName,
        ServiceSupplier<IConfigurationProvider> configurationProvider,
        ServiceSupplier<IBmModelManager> modelManager,
        ServiceSupplier<IModelObjectCopySupport> copySupport,
        ServiceSupplier<IMdRefactoringService> refactoringService) {
        this.projectName = Objects.requireNonNull(projectName);
        this.configurationProvider = Objects.requireNonNull(configurationProvider);
        this.modelManager = Objects.requireNonNull(modelManager);
        this.copySupport = Objects.requireNonNull(copySupport);
        this.refactoringService = Objects.requireNonNull(refactoringService);
    }

    Map<String, Object> health() {
        Map<String, Object> result = baseResult();
        IProject project = project();
        result.put("project_exists", project.exists());
        result.put("project_open", project.isOpen());
        result.put("manifest_exists", project.getFile("DT-INF/PROJECT.PMF").exists());
        result.put("bridge_mode", "edt-model");
        result.put("database_changed", false);
        result.put("bsl_source_access", true);
        result.put("bsl_diagnostics", true);
        result.put("bsl_content_assist", true);
        try {
            Configuration configuration = configuration(project);
            result.put("ready", configuration != null);
            result.put("configuration", configuration == null ? null : configuration.getName());
        } catch (RuntimeException e) {
            result.put("ready", false);
            result.put("error", safeMessage(e));
        }
        return result;
    }

    Map<String, Object> listObjects(String requestedType) {
        Configuration configuration = readyConfiguration();
        String type = requestedType == null || requestedType.isBlank() ? null : normalizeType(requestedType);
        List<Map<String, Object>> objects = new ArrayList<>();
        for (MdObject object : topLevelObjects(configuration)) {
            if (type == null || object.eClass().getName().equalsIgnoreCase(type)) {
                objects.add(describeObject(object));
            }
        }
        objects.sort(Comparator.comparing(value -> String.valueOf(value.get("fqn")), String.CASE_INSENSITIVE_ORDER));
        Map<String, Object> result = baseResult();
        result.put("objects", objects);
        result.put("count", objects.size());
        return result;
    }

    Map<String, Object> inspect(String type, String name) {
        MdObject object = requireObject(readyConfiguration(), type, name);
        Map<String, Integer> containedTypes = new HashMap<>();
        Iterator<EObject> contents = object.eAllContents();
        while (contents.hasNext()) {
            EObject child = contents.next();
            containedTypes.merge(child.eClass().getName(), 1, Integer::sum);
        }
        Map<String, Object> features = new LinkedHashMap<>();
        for (EStructuralFeature feature : object.eClass().getEAllStructuralFeatures()) {
            if (!object.eIsSet(feature)) {
                continue;
            }
            Object value = object.eGet(feature, false);
            if (feature instanceof EAttribute && isScalar(value)) {
                features.put(feature.getName(), value == null ? null : String.valueOf(value));
            } else if (feature instanceof EReference reference && reference.isContainment()) {
                features.put(feature.getName(), value instanceof Collection<?> collection ? collection.size() : 1);
            }
        }
        Map<String, Object> result = baseResult();
        result.putAll(describeObject(object));
        result.put("features", features);
        result.put("contained_types", containedTypes);
        result.put("contained_count", containedTypes.values().stream().mapToInt(Integer::intValue).sum());
        return result;
    }

    synchronized Map<String, Object> prepareClone(String type, String sourceName, String targetName) {
        cleanupExpiredPlans();
        validateName(sourceName, "source_name");
        validateName(targetName, "target_name");
        if (sourceName.equalsIgnoreCase(targetName)) {
            throw new BridgeException(400, "source_name and target_name must differ");
        }
        IProject project = readyProject();
        modelManager.get().waitModelSynchronization(project);
        Configuration configuration = configuration(project);
        MdObject source = requireObject(configuration, type, sourceName);
        if (findObject(configuration, type, targetName) != null) {
            throw new BridgeException(409, "Target metadata object already exists");
        }
        String planId = UUID.randomUUID().toString().replace("-", "");
        PreparedPlan plan = new PreparedPlan(planId, source.eClass().getName(), source.getName(), targetName,
            uuidOf(source), fingerprint(project, configuration), Instant.now());
        plans.put(planId, plan);
        Map<String, Object> result = baseResult();
        result.put("plan_id", planId);
        result.put("source", describeObject(source));
        result.put("target_fqn", plan.type() + "." + targetName);
        result.put("configuration_fingerprint", plan.fingerprint());
        result.put("expires_at", plan.createdAt().plus(PLAN_TTL).toString());
        result.put("changes_database", false);
        result.put("warnings", List.of("Applying this plan changes only the EDT project; deployment is a separate operation."));
        return result;
    }

    synchronized Map<String, Object> applyClone(String planId) {
        PreparedPlan plan = requirePlan(planId);
        IProject project = readyProject();
        modelManager.get().waitModelSynchronization(project);
        Configuration configuration = configuration(project);
        MdObject source = requireObject(configuration, plan.type(), plan.sourceName());
        if (!Objects.equals(plan.sourceUuid(), uuidOf(source))) {
            throw new BridgeException(409, "Prepared plan is stale: source UUID changed");
        }
        if (findObject(configuration, plan.type(), plan.targetName()) != null) {
            throw new BridgeException(409, "Prepared plan cannot be applied: target already exists");
        }
        String currentFingerprint = fingerprint(project, configuration);
        if (!plan.fingerprint().equals(currentFingerprint)) {
            throw new BridgeException(409, "Prepared plan is stale: EDT project changed");
        }

        MdObject copy = null;
        try {
            copy = copySupport.get().copyAndAttach(source, project, new NullProgressMonitor());
            if (!plan.targetName().equals(copy.getName())) {
                Collection<IRefactoring> refactorings = refactoringService.get()
                    .createMdObjectRenameRefactoring(copy, plan.targetName());
                validateRefactorings(refactorings);
                for (IRefactoring refactoring : refactorings) {
                    refactoring.perform();
                }
            }
            modelManager.get().waitModelSynchronization(project);
            Configuration refreshed = configuration(project);
            MdObject target = findObject(refreshed, plan.type(), plan.targetName());
            if (target == null) {
                throw new IllegalStateException("EDT did not expose the copied metadata object after synchronization");
            }
            plans.remove(planId);
            Map<String, Object> result = baseResult();
            result.put("applied", true);
            result.put("object", describeObject(target));
            result.put("project_changed", true);
            result.put("database_changed", false);
            result.put("deployment_required", true);
            return result;
        } catch (Exception e) {
            if (copy != null) {
                deleteQuietly(copy);
            }
            if (e instanceof BridgeException bridgeException) {
                throw bridgeException;
            }
            throw new BridgeException(500, safeMessage(e), e);
        }
    }

    Map<String, Object> verify(String type, String name) {
        IProject project = readyProject();
        modelManager.get().waitModelSynchronization(project);
        Configuration configuration = configuration(project);
        MdObject object = findObject(configuration, type, name);
        Map<String, Object> result = baseResult();
        result.put("exists", object != null);
        result.put("object", object == null ? null : describeObject(object));
        result.put("project_fingerprint", fingerprint(project, configuration));
        result.put("database_checked", false);
        return result;
    }

    Map<String, Object> discard(String planId) {
        PreparedPlan removed = plans.remove(planId);
        Map<String, Object> result = baseResult();
        result.put("discarded", removed != null);
        return result;
    }

    private IProject project() {
        return ResourcesPlugin.getWorkspace().getRoot().getProject(projectName);
    }

    private IProject readyProject() {
        IProject project = project();
        if (!project.exists() || !project.isOpen()) {
            throw new BridgeException(409, "Fixed EDT project is not open: " + projectName);
        }
        if (!project.getFile("DT-INF/PROJECT.PMF").exists()) {
            throw new BridgeException(409, "EDT project import is incomplete: DT-INF/PROJECT.PMF is missing");
        }
        return project;
    }

    private Configuration readyConfiguration() {
        return configuration(readyProject());
    }

    private Configuration configuration(IProject project) {
        Configuration configuration = configurationProvider.get().getConfiguration(project);
        if (configuration == null) {
            throw new BridgeException(409, "EDT model is not initialized for project " + projectName);
        }
        return configuration;
    }

    private MdObject requireObject(Configuration configuration, String type, String name) {
        validateName(name, "name");
        MdObject object = findObject(configuration, type, name);
        if (object == null) {
            throw new BridgeException(404, "Metadata object not found: " + normalizeType(type) + "." + name);
        }
        return object;
    }

    private MdObject findObject(Configuration configuration, String type, String name) {
        String normalizedType = normalizeType(type);
        for (MdObject object : topLevelObjects(configuration)) {
            if (object.eClass().getName().equalsIgnoreCase(normalizedType)
                && object.getName().equalsIgnoreCase(name)) {
                return object;
            }
        }
        return null;
    }

    private static List<MdObject> topLevelObjects(Configuration configuration) {
        Map<String, MdObject> result = new LinkedHashMap<>();
        for (Method method : Configuration.class.getMethods()) {
            if (method.getParameterCount() != 0 || !Collection.class.isAssignableFrom(method.getReturnType())
                || !returnsMdObjects(method)) {
                continue;
            }
            try {
                Object value = method.invoke(configuration);
                if (value instanceof Collection<?> collection) {
                    for (Object item : collection) {
                        if (item instanceof MdObject object) {
                            String key = object.eClass().getName() + ":" + object.getName() + ":" + uuidOf(object);
                            result.putIfAbsent(key, object);
                        }
                    }
                }
            } catch (IllegalAccessException | InvocationTargetException e) {
                throw new BridgeException(500, "Cannot read EDT metadata collection " + method.getName(), e);
            }
        }
        return new ArrayList<>(result.values());
    }

    private static boolean returnsMdObjects(Method method) {
        Type returnType = method.getGenericReturnType();
        if (!(returnType instanceof ParameterizedType parameterized)) {
            return false;
        }
        Type[] arguments = parameterized.getActualTypeArguments();
        return arguments.length == 1 && arguments[0] instanceof Class<?> itemType
            && MdObject.class.isAssignableFrom(itemType);
    }

    private static Map<String, Object> describeObject(MdObject object) {
        Map<String, Object> result = new LinkedHashMap<>();
        String type = object.eClass().getName();
        result.put("type", type);
        result.put("name", object.getName());
        result.put("fqn", type + "." + object.getName());
        result.put("uuid", uuidOf(object));
        return result;
    }

    private Map<String, Object> baseResult() {
        Map<String, Object> result = new LinkedHashMap<>();
        result.put("project", projectName);
        return result;
    }

    private PreparedPlan requirePlan(String planId) {
        if (planId == null || !planId.matches("[0-9a-f]{32}")) {
            throw new BridgeException(400, "plan_id must be the exact 32-character identifier returned by prepare-clone");
        }
        PreparedPlan plan = plans.get(planId);
        if (plan == null) {
            throw new BridgeException(404, "Prepared plan not found");
        }
        if (plan.createdAt().plus(PLAN_TTL).isBefore(Instant.now())) {
            plans.remove(planId);
            throw new BridgeException(409, "Prepared plan expired");
        }
        return plan;
    }

    private void cleanupExpiredPlans() {
        Instant cutoff = Instant.now().minus(PLAN_TTL);
        plans.values().removeIf(plan -> plan.createdAt().isBefore(cutoff));
    }

    private void deleteQuietly(MdObject object) {
        try {
            IRefactoring delete = refactoringService.get()
                .createMdObjectDeleteRefactoring(Collections.singleton(object));
            if (delete.getStatus().getProblems().isEmpty()) {
                delete.perform();
            }
        } catch (RuntimeException ignored) {
            // Preserve the original exception. EDT will report any cleanup problem in its log.
        }
    }

    private static void validateRefactorings(Collection<IRefactoring> refactorings) {
        if (refactorings.isEmpty()) {
            throw new BridgeException(409, "EDT did not create a rename refactoring");
        }
        List<String> problems = new ArrayList<>();
        for (IRefactoring refactoring : refactorings) {
            for (IRefactoringProblem problem : refactoring.getStatus().getProblems()) {
                problems.add(String.valueOf(problem));
            }
        }
        if (!problems.isEmpty()) {
            throw new BridgeException(409, "EDT rejected rename: " + String.join("; ", problems));
        }
    }

    private static void validateName(String value, String field) {
        if (value == null || !VALID_NAME.matcher(value).matches()) {
            throw new BridgeException(400, field + " is not a valid 1C metadata name");
        }
    }

    private static boolean isScalar(Object value) {
        return value == null || value instanceof String || value instanceof Number || value instanceof Boolean
            || value instanceof Enum<?> || value instanceof UUID;
    }

    private static String uuidOf(MdObject object) {
        return object.getUuid() == null ? null : object.getUuid().toString();
    }

    private static String normalizeType(String type) {
        if (type == null || type.isBlank()) {
            throw new BridgeException(400, "type is required");
        }
        String trimmed = type.trim();
        return TYPE_ALIASES.getOrDefault(trimmed.toLowerCase(Locale.ROOT), trimmed);
    }

    private static Map<String, String> createTypeAliases() {
        Map<String, String> aliases = new HashMap<>();
        aliases.put("document", "Document");
        aliases.put("documents", "Document");
        aliases.put("документ", "Document");
        aliases.put("документы", "Document");
        aliases.put("catalog", "Catalog");
        aliases.put("catalogs", "Catalog");
        aliases.put("справочник", "Catalog");
        aliases.put("справочники", "Catalog");
        aliases.put("report", "Report");
        aliases.put("отчет", "Report");
        aliases.put("отчёт", "Report");
        aliases.put("dataprocessor", "DataProcessor");
        aliases.put("обработка", "DataProcessor");
        aliases.put("informationregister", "InformationRegister");
        aliases.put("регистрсведений", "InformationRegister");
        aliases.put("accumulationregister", "AccumulationRegister");
        aliases.put("регистрнакопления", "AccumulationRegister");
        return aliases;
    }

    private static String fingerprint(IProject project, Configuration configuration) {
        try {
            MessageDigest digest = MessageDigest.getInstance("SHA-256");
            List<String> entries = new ArrayList<>();
            project.accept(resource -> {
                if (resource.getType() == IResource.FILE) {
                    entries.add(resource.getProjectRelativePath() + ":" + resource.getModificationStamp());
                }
                return true;
            });
            for (MdObject object : topLevelObjects(configuration)) {
                entries.add("model:" + object.eClass().getName() + ":" + object.getName() + ":" + uuidOf(object));
            }
            entries.sort(String::compareTo);
            for (String entry : entries) {
                digest.update(entry.getBytes(StandardCharsets.UTF_8));
                digest.update((byte) 0);
            }
            return toHex(digest.digest());
        } catch (NoSuchAlgorithmException | CoreException e) {
            throw new BridgeException(500, "Cannot calculate EDT project fingerprint", e);
        }
    }

    private static String toHex(byte[] bytes) {
        StringBuilder result = new StringBuilder(bytes.length * 2);
        for (byte value : bytes) {
            result.append(String.format("%02x", value));
        }
        return result.toString();
    }

    private static String safeMessage(Throwable error) {
        String message = error.getMessage();
        return message == null || message.isBlank() ? error.getClass().getSimpleName() : message;
    }

    private record PreparedPlan(String id, String type, String sourceName, String targetName, String sourceUuid,
        String fingerprint, Instant createdAt) {
    }

    static final class BridgeException extends RuntimeException {
        private static final long serialVersionUID = 1L;
        private final int status;

        BridgeException(int status, String message) {
            super(message);
            this.status = status;
        }

        BridgeException(int status, String message, Throwable cause) {
            super(message, cause);
            this.status = status;
        }

        int status() {
            return status;
        }
    }
}
