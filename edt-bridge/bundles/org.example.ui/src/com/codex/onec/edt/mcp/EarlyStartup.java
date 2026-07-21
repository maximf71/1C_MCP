package com.codex.onec.edt.mcp;

import org.eclipse.ui.IStartup;

public final class EarlyStartup implements IStartup {
    @Override
    public void earlyStartup() {
        Activator activator = Activator.getDefault();
        if (activator != null) {
            activator.startBridge();
        }
    }
}
