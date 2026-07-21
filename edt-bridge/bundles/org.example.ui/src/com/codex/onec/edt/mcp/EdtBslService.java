package com.codex.onec.edt.mcp;

import java.io.BufferedReader;
import java.io.IOException;
import java.io.InputStreamReader;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.Comparator;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.concurrent.atomic.AtomicReference;

import org.eclipse.core.resources.IFile;
import org.eclipse.core.resources.IMarker;
import org.eclipse.core.resources.IProject;
import org.eclipse.core.resources.IResource;
import org.eclipse.core.resources.ResourcesPlugin;
import org.eclipse.core.runtime.CoreException;
import org.eclipse.core.runtime.NullProgressMonitor;
import org.eclipse.jface.text.BadLocationException;
import org.eclipse.jface.text.IDocument;
import org.eclipse.jface.text.contentassist.ContentAssistant;
import org.eclipse.jface.text.contentassist.ICompletionProposal;
import org.eclipse.jface.text.contentassist.ICompletionProposalExtension5;
import org.eclipse.jface.text.contentassist.IContentAssistProcessor;
import org.eclipse.jface.text.source.ISourceViewer;
import org.eclipse.swt.widgets.Display;
import org.eclipse.ui.IEditorPart;
import org.eclipse.ui.IWorkbenchPage;
import org.eclipse.ui.IWorkbenchWindow;
import org.eclipse.ui.PlatformUI;
import org.eclipse.ui.ide.IDE;
import org.eclipse.xtext.ui.editor.XtextEditor;
import org.eclipse.xtext.ui.editor.XtextSourceViewer;

import com.codex.onec.edt.mcp.EdtMetadataService.BridgeException;

/** Read-only access to BSL source, EDT diagnostics and EDT content assist. */
final class EdtBslService {
    private static final int MAX_LIST_LIMIT = 1000;
    private static final int MAX_READ_LINES = 2000;
    private static final int MAX_RESULTS = 200;

    private final String projectName;

    EdtBslService(String projectName) {
        this.projectName = projectName;
    }

    Map<String, Object> listModules(String contains, int requestedLimit) {
        IProject project = readyProject();
        int limit = clamp(requestedLimit, MAX_LIST_LIMIT, 200);
        String filter = normalizeFilter(contains);
        List<String> modules = new ArrayList<>();
        try {
            project.getFolder("src").accept(resource -> {
                if (modules.size() >= limit) {
                    return false;
                }
                if (resource instanceof IFile file && "bsl".equalsIgnoreCase(file.getFileExtension())) {
                    String path = fromSrc(file);
                    if (filter == null || path.toLowerCase(Locale.ROOT).contains(filter)) {
                        modules.add(path);
                    }
                }
                return true;
            });
        } catch (CoreException e) {
            throw new BridgeException(500, "Cannot list BSL modules", e);
        }
        modules.sort(String.CASE_INSENSITIVE_ORDER);
        Map<String, Object> result = baseResult();
        result.put("modules", modules);
        result.put("returned", modules.size());
        result.put("limit", limit);
        return result;
    }

    Map<String, Object> readModule(String modulePath, int requestedStart, int requestedEnd) {
        IFile file = requireModule(modulePath);
        int startLine = Math.max(1, requestedStart <= 0 ? 1 : requestedStart);
        int endLine = requestedEnd <= 0 ? startLine + 399 : requestedEnd;
        if (endLine < startLine) {
            throw new BridgeException(400, "end_line must be greater than or equal to start_line");
        }
        endLine = Math.min(endLine, startLine + MAX_READ_LINES - 1);
        List<Map<String, Object>> lines = new ArrayList<>();
        int totalLines = 0;
        try (BufferedReader reader = new BufferedReader(
            new InputStreamReader(file.getContents(true), StandardCharsets.UTF_8))) {
            String line;
            while ((line = reader.readLine()) != null) {
                totalLines++;
                if (totalLines >= startLine && totalLines <= endLine) {
                    Map<String, Object> item = new LinkedHashMap<>();
                    item.put("line", totalLines);
                    item.put("text", line);
                    lines.add(item);
                }
            }
        } catch (CoreException | IOException e) {
            throw new BridgeException(500, "Cannot read BSL module", e);
        }
        Map<String, Object> result = baseResult();
        result.put("module_path", fromSrc(file));
        result.put("start_line", startLine);
        result.put("end_line", lines.isEmpty() ? startLine - 1 : startLine + lines.size() - 1);
        result.put("total_lines", totalLines);
        result.put("lines", lines);
        return result;
    }

    Map<String, Object> searchCode(String query, String pathContains, int requestedLimit) {
        if (query == null || query.isBlank()) {
            throw new BridgeException(400, "query is required");
        }
        IProject project = readyProject();
        int limit = clamp(requestedLimit, MAX_RESULTS, 50);
        String needle = query.toLowerCase(Locale.ROOT);
        String pathFilter = normalizeFilter(pathContains);
        List<Map<String, Object>> matches = new ArrayList<>();
        try {
            project.getFolder("src").accept(resource -> {
                if (matches.size() >= limit) {
                    return false;
                }
                if (!(resource instanceof IFile file) || !"bsl".equalsIgnoreCase(file.getFileExtension())) {
                    return true;
                }
                String modulePath = fromSrc(file);
                if (pathFilter != null && !modulePath.toLowerCase(Locale.ROOT).contains(pathFilter)) {
                    return true;
                }
                searchFile(file, modulePath, needle, matches, limit);
                return matches.size() < limit;
            });
        } catch (CoreException e) {
            throw new BridgeException(500, "Cannot search BSL modules", e);
        }
        Map<String, Object> result = baseResult();
        result.put("query", query);
        result.put("matches", matches);
        result.put("returned", matches.size());
        result.put("limit", limit);
        return result;
    }

    Map<String, Object> diagnostics(String modulePath, int requestedLimit) {
        IProject project = readyProject();
        String normalizedPath = modulePath == null || modulePath.isBlank() ? null : normalizeModulePath(modulePath);
        int limit = clamp(requestedLimit, MAX_RESULTS, 100);
        List<Map<String, Object>> problems = new ArrayList<>();
        try {
            for (IMarker marker : project.findMarkers(IMarker.PROBLEM, true, IResource.DEPTH_INFINITE)) {
                if (problems.size() >= limit) {
                    break;
                }
                IResource resource = marker.getResource();
                if (!(resource instanceof IFile file) || !"bsl".equalsIgnoreCase(file.getFileExtension())) {
                    continue;
                }
                String path = fromSrc(file);
                if (normalizedPath != null && !path.equalsIgnoreCase(normalizedPath)) {
                    continue;
                }
                Map<String, Object> item = new LinkedHashMap<>();
                item.put("module_path", path);
                item.put("line", marker.getAttribute(IMarker.LINE_NUMBER, -1));
                item.put("severity", severity(marker.getAttribute(IMarker.SEVERITY, IMarker.SEVERITY_INFO)));
                item.put("message", marker.getAttribute(IMarker.MESSAGE, ""));
                item.put("code", firstAttribute(marker, "code", "checkId", "uid"));
                problems.add(item);
            }
        } catch (CoreException e) {
            throw new BridgeException(500, "Cannot read EDT diagnostics", e);
        }
        problems.sort(Comparator.<Map<String, Object>, String>comparing(
            value -> String.valueOf(value.get("module_path")), String.CASE_INSENSITIVE_ORDER)
            .thenComparingInt(value -> (Integer) value.get("line")));
        Map<String, Object> result = baseResult();
        result.put("problems", problems);
        result.put("returned", problems.size());
        result.put("limit", limit);
        result.put("source", "EDT workspace problem markers");
        return result;
    }

    Map<String, Object> contentAssist(String modulePath, int line, int column, String contains,
        int requestedLimit, boolean includeDocumentation) {
        if (line < 1 || column < 1) {
            throw new BridgeException(400, "line and column must be positive 1-based numbers");
        }
        IFile file = requireModule(modulePath);
        int limit = clamp(requestedLimit, MAX_RESULTS, 50);
        AtomicReference<Map<String, Object>> result = new AtomicReference<>();
        AtomicReference<RuntimeException> error = new AtomicReference<>();
        Display display = PlatformUI.getWorkbench().getDisplay();
        display.syncExec(() -> {
            try {
                result.set(computeContentAssist(file, line, column, contains, limit, includeDocumentation));
            } catch (RuntimeException e) {
                error.set(e);
            }
        });
        if (error.get() != null) {
            throw error.get();
        }
        return result.get();
    }

    private Map<String, Object> computeContentAssist(IFile file, int line, int column, String contains,
        int limit, boolean includeDocumentation) {
        IWorkbenchWindow window = PlatformUI.getWorkbench().getActiveWorkbenchWindow();
        IWorkbenchPage page = window == null ? null : window.getActivePage();
        if (page == null) {
            throw new BridgeException(409, "EDT has no active workbench page");
        }
        try {
            IEditorPart part = IDE.openEditor(page, file, true);
            XtextEditor editor = part.getAdapter(XtextEditor.class);
            if (editor == null) {
                throw new BridgeException(409, "EDT did not open the file as a BSL editor");
            }
            ISourceViewer viewer = editor.getInternalSourceViewer();
            if (!(viewer instanceof XtextSourceViewer xtextViewer)) {
                throw new BridgeException(409, "EDT BSL source viewer is not ready; retry the call");
            }
            IDocument document = viewer.getDocument();
            int lineIndex = line - 1;
            int columnIndex = column - 1;
            int lineLength = document.getLineLength(lineIndex);
            if (columnIndex > lineLength) {
                throw new BridgeException(400, "column is outside the requested line");
            }
            int offset = document.getLineOffset(lineIndex) + columnIndex;
            if (offset < 0 || offset > document.getLength()) {
                throw new BridgeException(400, "line and column are outside the module");
            }
            String contentType = document.getContentType(offset);
            if (!(xtextViewer.getContentAssistant() instanceof ContentAssistant assistant)) {
                throw new BridgeException(409, "EDT content assist is not ready; retry the call");
            }
            IContentAssistProcessor processor = assistant.getContentAssistProcessor(contentType);
            if (processor == null) {
                throw new BridgeException(409, "No EDT content assist processor at the requested position");
            }
            ICompletionProposal[] proposals = processor.computeCompletionProposals(viewer, offset);
            if (proposals == null) {
                proposals = new ICompletionProposal[0];
            }
            Arrays.sort(proposals, Comparator.comparing(p -> String.valueOf(p.getDisplayString()),
                String.CASE_INSENSITIVE_ORDER));
            String filter = normalizeFilter(contains);
            List<Map<String, Object>> items = new ArrayList<>();
            for (ICompletionProposal proposal : proposals) {
                String displayString = proposal.getDisplayString();
                if (filter != null && !displayString.toLowerCase(Locale.ROOT).contains(filter)) {
                    continue;
                }
                Map<String, Object> item = new LinkedHashMap<>();
                item.put("display", displayString);
                if (includeDocumentation) {
                    String documentation = proposalDocumentation(proposal);
                    if (documentation != null && !documentation.isBlank()) {
                        item.put("documentation", cleanDocumentation(documentation));
                    }
                }
                items.add(item);
                if (items.size() >= limit) {
                    break;
                }
            }
            Map<String, Object> result = baseResult();
            result.put("module_path", fromSrc(file));
            result.put("line", line);
            result.put("column", column);
            result.put("proposals", items);
            result.put("returned", items.size());
            result.put("available_before_filter", proposals.length);
            result.put("source", "1C:EDT BSL content assist");
            return result;
        } catch (CoreException | BadLocationException e) {
            throw new BridgeException(500, "Cannot obtain EDT BSL content assist", e);
        }
    }

    private IProject readyProject() {
        IProject project = ResourcesPlugin.getWorkspace().getRoot().getProject(projectName);
        if (!project.exists() || !project.isOpen() || !project.getFolder("src").exists()) {
            throw new BridgeException(409, "Fixed EDT project is not ready: " + projectName);
        }
        return project;
    }

    private IFile requireModule(String modulePath) {
        String normalized = normalizeModulePath(modulePath);
        IFile file = readyProject().getFile("src/" + normalized);
        if (!file.exists()) {
            throw new BridgeException(404, "BSL module not found: " + normalized);
        }
        return file;
    }

    private static String normalizeModulePath(String modulePath) {
        if (modulePath == null || modulePath.isBlank()) {
            throw new BridgeException(400, "module_path is required");
        }
        String normalized = modulePath.trim().replace('\\', '/');
        if (normalized.startsWith("src/")) {
            normalized = normalized.substring(4);
        }
        if (normalized.startsWith("/") || normalized.contains("../") || normalized.contains(":")
            || !normalized.toLowerCase(Locale.ROOT).endsWith(".bsl")) {
            throw new BridgeException(400, "module_path must be a relative .bsl path under project src");
        }
        return normalized;
    }

    private static void searchFile(IFile file, String path, String needle, List<Map<String, Object>> matches,
        int limit) {
        try (BufferedReader reader = new BufferedReader(
            new InputStreamReader(file.getContents(true), StandardCharsets.UTF_8))) {
            String line;
            int lineNumber = 0;
            while ((line = reader.readLine()) != null && matches.size() < limit) {
                lineNumber++;
                if (line.toLowerCase(Locale.ROOT).contains(needle)) {
                    Map<String, Object> item = new LinkedHashMap<>();
                    item.put("module_path", path);
                    item.put("line", lineNumber);
                    item.put("text", line.strip());
                    matches.add(item);
                }
            }
        } catch (CoreException | IOException e) {
            throw new BridgeException(500, "Cannot search BSL module " + path, e);
        }
    }

    private Map<String, Object> baseResult() {
        Map<String, Object> result = new LinkedHashMap<>();
        result.put("project", projectName);
        result.put("database_changed", false);
        return result;
    }

    private static String fromSrc(IFile file) {
        String path = file.getProjectRelativePath().toString().replace('\\', '/');
        return path.startsWith("src/") ? path.substring(4) : path;
    }

    private static int clamp(int value, int max, int fallback) {
        int normalized = value <= 0 ? fallback : value;
        return Math.max(1, Math.min(max, normalized));
    }

    private static String normalizeFilter(String value) {
        return value == null || value.isBlank() ? null : value.trim().toLowerCase(Locale.ROOT);
    }

    private static String severity(int value) {
        return switch (value) {
        case IMarker.SEVERITY_ERROR -> "error";
        case IMarker.SEVERITY_WARNING -> "warning";
        default -> "info";
        };
    }

    private static Object firstAttribute(IMarker marker, String... names) throws CoreException {
        for (String name : names) {
            Object value = marker.getAttribute(name);
            if (value != null) {
                return value;
            }
        }
        return null;
    }

    private static String proposalDocumentation(ICompletionProposal proposal) {
        if (proposal instanceof ICompletionProposalExtension5 extension) {
            Object value = extension.getAdditionalProposalInfo(new NullProgressMonitor());
            return value == null ? null : value.toString();
        }
        return proposal.getAdditionalProposalInfo();
    }

    private static String cleanDocumentation(String value) {
        return value.replaceAll("(?is)<style[^>]*>.*?</style>", "")
            .replaceAll("(?i)<br\\s*/?>", "\n").replaceAll("(?i)</p>", "\n\n")
            .replaceAll("(?s)<[^>]+>", " ").replace("&nbsp;", " ").replace("&lt;", "<")
            .replace("&gt;", ">").replace("&amp;", "&").replaceAll("[ \\t]+", " ")
            .replaceAll("\n{3,}", "\n\n").trim();
    }
}
