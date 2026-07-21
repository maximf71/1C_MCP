package com.codex.onec.edt.mcp;

import org.eclipse.core.runtime.IStatus;
import org.eclipse.core.runtime.Plugin;
import org.eclipse.core.runtime.Status;
import org.osgi.framework.BundleContext;

import com._1c.g5.v8.dt.core.platform.IBmModelManager;
import com._1c.g5.v8.dt.core.platform.IConfigurationProvider;
import com._1c.g5.v8.dt.core.platform.IV8ProjectManager;
import com._1c.g5.v8.dt.import_.IImportOperationFactory;
import com._1c.g5.v8.dt.md.copy.IModelObjectCopySupport;
import com._1c.g5.v8.dt.md.refactoring.core.IMdRefactoringService;
import com._1c.g5.wiring.ServiceAccess;
import com._1c.g5.wiring.ServiceSupplier;

public final class Activator extends Plugin {
    public static final String PLUGIN_ID = "com.codex.onec.edt.mcp";

    private static Activator instance;
    private ServiceSupplier<IConfigurationProvider> configurationProvider;
    private ServiceSupplier<IBmModelManager> modelManager;
    private ServiceSupplier<IModelObjectCopySupport> copySupport;
    private ServiceSupplier<IMdRefactoringService> refactoringService;
    private ServiceSupplier<IImportOperationFactory> importOperationFactory;
    private ServiceSupplier<IV8ProjectManager> v8ProjectManager;
    private EdtBridgeServer server;

    public static Activator getDefault() {
        return instance;
    }

    @Override
    public void start(BundleContext context) throws Exception {
        super.start(context);
        instance = this;
        configurationProvider = ServiceAccess.supplier(IConfigurationProvider.class, this);
        modelManager = ServiceAccess.supplier(IBmModelManager.class, this);
        copySupport = ServiceAccess.supplier(IModelObjectCopySupport.class, this);
        refactoringService = ServiceAccess.supplier(IMdRefactoringService.class, this);
        importOperationFactory = ServiceAccess.supplier(IImportOperationFactory.class, this);
        v8ProjectManager = ServiceAccess.supplier(IV8ProjectManager.class, this);
    }

    public synchronized void startBridge() {
        if (server != null) {
            return;
        }
        try {
            String projectName = System.getProperty("onec.mcp.project", "").trim();
            if (projectName.isEmpty()) {
                throw new IllegalStateException(
                    "Set -Donec.mcp.project=<EDT project name> in 1cedt.ini");
            }
            EdtMetadataService metadata = new EdtMetadataService(
                projectName,
                configurationProvider, modelManager, copySupport, refactoringService);
            EdtExternalObjectService externalObjects = new EdtExternalObjectService(projectName,
                importOperationFactory, v8ProjectManager, modelManager);
            server = new EdtBridgeServer(metadata, new EdtBslService(projectName), externalObjects);
            server.start();
            getLog().log(new Status(IStatus.INFO, PLUGIN_ID,
                "Codex EDT bridge started on loopback interface"));
        } catch (Exception e) {
            server = null;
            getLog().log(new Status(IStatus.ERROR, PLUGIN_ID, "Cannot start Codex EDT bridge", e));
        }
    }

    @Override
    public synchronized void stop(BundleContext context) throws Exception {
        if (server != null) {
            server.close();
            server = null;
        }
        close(refactoringService);
        close(v8ProjectManager);
        close(importOperationFactory);
        close(copySupport);
        close(modelManager);
        close(configurationProvider);
        instance = null;
        super.stop(context);
    }

    private static void close(AutoCloseable value) {
        if (value != null) {
            try {
                value.close();
            } catch (Exception ignored) {
                // EDT is already shutting down.
            }
        }
    }
}
