package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"mcp-1c-analog/internal/analysis"
	"mcp-1c-analog/internal/dump"
	"mcp-1c-analog/internal/mcp"
)

func RegisterAnalysis(server *mcp.Server, index *dump.Index, dumpDir string, comparisonDump ...string) {
	secondDump := ""
	if len(comparisonDump) > 0 {
		secondDump = comparisonDump[0]
	}
	addIfMissing(server, readOnlyTool("validate_xml_sources", "Validate that all XML sources in the fixed dump are well-formed.", objectSchema(), func(ctx context.Context, args map[string]any) (any, error) {
		if dumpDir == "" {
			return nil, errors.New("XML validation is disabled: configure a dump directory")
		}
		diagnostics, err := analysis.ValidateXML(dumpDir)
		return map[string]any{"valid": err == nil && len(diagnostics) == 0, "diagnostics": diagnostics}, err
	}))
	addIfMissing(server, readOnlyTool("lint_bsl", "Run deterministic offline BSL structural checks on the fixed dump. EDT diagnostics remain authoritative when EDT is available.", objectSchema(), func(ctx context.Context, args map[string]any) (any, error) {
		if dumpDir == "" {
			return nil, errors.New("BSL lint is disabled: configure a dump directory")
		}
		diagnostics, err := analysis.LintBSL(dumpDir)
		return map[string]any{"diagnostics": diagnostics, "count": len(diagnostics)}, err
	}))
	addIfMissing(server, readOnlyTool("analyze_query", "Analyze a read-only 1C query and return deterministic performance suggestions without executing it.", objectSchema(prop("query", "string", "1C query text.")), func(ctx context.Context, args map[string]any) (any, error) {
		return analysis.AnalyzeQuery(requiredString(args, "query")), nil
	}))
	addIfMissing(server, readOnlyTool("optimize_query", "Return safe optimization suggestions for a 1C query. The query is never rewritten or executed automatically.", objectSchema(prop("query", "string", "1C query text.")), func(ctx context.Context, args map[string]any) (any, error) {
		result := analysis.AnalyzeQuery(requiredString(args, "query"))
		return map[string]any{"query": requiredString(args, "query"), "valid": result.Valid, "suggestions": result.Suggestions, "diagnostics": result.Diagnostics, "changed": false}, nil
	}))
	addIfMissing(server, readOnlyTool("bulk_analyze", "Validate XML, lint BSL, index symbols and build a static call graph for the fixed dump.", objectSchema(), func(ctx context.Context, args map[string]any) (any, error) {
		if dumpDir == "" {
			return nil, errors.New("bulk analysis is disabled: configure a dump directory")
		}
		return analysis.AnalyzeDump(dumpDir)
	}))
	addIfMissing(server, readOnlyTool("semantic_search", "Search the fixed dump using a local deterministic hashed embedding. No source code leaves the machine.", objectSchema(
		prop("query", "string", "Natural-language or BSL search text."),
		prop("limit", "number", "Maximum results."),
	), func(ctx context.Context, args map[string]any) (any, error) {
		if index == nil {
			return nil, errors.New("semantic search is disabled: configure a dump directory")
		}
		return analysis.SemanticSearch(index, requiredString(args, "query"), intArg(args, "limit", 10)), nil
	}))
	addIfMissing(server, readOnlyTool("get_call_graph", "Get a static BSL method call graph from the fixed dump.", objectSchema(prop("symbol", "string", "Optional caller or callee name.")), func(ctx context.Context, args map[string]any) (any, error) {
		report, err := requireAnalysis(dumpDir)
		if err != nil {
			return nil, err
		}
		return analysis.CallGraph(report, requiredString(args, "symbol")), nil
	}))
	addIfMissing(server, readOnlyTool("find_references", "Find static BSL calls to a symbol in the fixed dump.", objectSchema(prop("symbol", "string", "Method name.")), func(ctx context.Context, args map[string]any) (any, error) {
		symbol := requiredString(args, "symbol")
		if symbol == "" {
			return nil, errors.New("symbol is required")
		}
		report, err := requireAnalysis(dumpDir)
		if err != nil {
			return nil, err
		}
		var result []analysis.Call
		for _, call := range report.Calls {
			if strings.EqualFold(call.Callee, symbol) {
				result = append(result, call)
			}
		}
		return result, nil
	}))
	addIfMissing(server, readOnlyTool("render_architecture", "Render the static BSL call architecture as Mermaid.", objectSchema(), func(ctx context.Context, args map[string]any) (any, error) {
		report, err := requireAnalysis(dumpDir)
		if err != nil {
			return nil, err
		}
		return map[string]any{"format": "mermaid", "diagram": analysis.ArchitectureMermaid(report)}, nil
	}))
	addIfMissing(server, readOnlyTool("generate_documentation", "Generate Markdown documentation from symbols in the fixed dump without changing files.", objectSchema(), func(ctx context.Context, args map[string]any) (any, error) {
		report, err := requireAnalysis(dumpDir)
		if err != nil {
			return nil, err
		}
		return map[string]any{"format": "markdown", "document": analysis.Documentation(report)}, nil
	}))
	addIfMissing(server, readOnlyTool("inspect_extension", "Inspect whether the fixed XML dump represents an extension and return its root metadata properties.", objectSchema(), func(ctx context.Context, args map[string]any) (any, error) {
		if dumpDir == "" {
			return nil, errors.New("extension inspection is disabled: configure a dump directory")
		}
		path := filepath.Join(dumpDir, "Configuration.xml")
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		text := string(data)
		return map[string]any{
			"path":                      path,
			"is_extension":              strings.Contains(text, "<ConfigurationExtensionCompatibilityMode>") || strings.Contains(text, "<Purpose>"),
			"contains_borrowed_objects": strings.Contains(text, "<ObjectBelonging>Adopted</ObjectBelonging>"),
		}, nil
	}))
	addIfMissing(server, readOnlyTool("compare_configurations", "Compare the fixed primary and comparison dumps by content hash. Paths cannot be supplied by a tool caller.", objectSchema(), func(ctx context.Context, args map[string]any) (any, error) {
		if dumpDir == "" || secondDump == "" {
			return nil, errors.New("comparison is disabled: configure both dump and comparison-dump")
		}
		differences, err := analysis.CompareDirectories(dumpDir, secondDump)
		return map[string]any{"differences": differences, "count": len(differences)}, err
	}))
	addIfMissing(server, readOnlyTool("compare_extension", "Compare a fixed extension/configuration dump pair without accepting arbitrary filesystem paths.", objectSchema(), func(ctx context.Context, args map[string]any) (any, error) {
		if dumpDir == "" || secondDump == "" {
			return nil, errors.New("extension comparison is disabled: configure both dump and comparison-dump")
		}
		differences, err := analysis.CompareDirectories(dumpDir, secondDump)
		return map[string]any{"differences": differences, "count": len(differences)}, err
	}))
	addIfMissing(server, readOnlyTool("check_api_compatibility", "Report public BSL methods removed from the primary dump relative to the fixed comparison baseline.", objectSchema(), func(ctx context.Context, args map[string]any) (any, error) {
		if dumpDir == "" || secondDump == "" {
			return nil, errors.New("API compatibility is disabled: configure both dump and comparison-dump")
		}
		current, err := analysis.AnalyzeDump(dumpDir)
		if err != nil {
			return nil, err
		}
		baseline, err := analysis.AnalyzeDump(secondDump)
		if err != nil {
			return nil, err
		}
		return analysis.APICompatibility(current, baseline), nil
	}))
	addIfMissing(server, readOnlyTool("analyze_rls", "Find role files in the fixed dump and report potential restriction-by-condition rules.", objectSchema(), func(ctx context.Context, args map[string]any) (any, error) {
		return scanFixedFiles(dumpDir, "Rights.xml", []string{"restrictionByCondition", "ОграничениеДоступаКДанным"})
	}))
	addIfMissing(server, readOnlyTool("analyze_exchange_plans", "List exchange plan metadata and modules from the fixed dump.", objectSchema(), func(ctx context.Context, args map[string]any) (any, error) {
		return listFixedTree(dumpDir, "ExchangePlans")
	}))
}

func addIfMissing(server *mcp.Server, tool mcp.Tool) {
	if !server.HasTool(tool.Name) {
		server.AddTool(tool)
	}
}

func readOnlyTool(name, description string, schema map[string]any, handler mcp.Handler) mcp.Tool {
	value := tool(name, description, schema, handler)
	value.Annotations = &mcp.Annotations{Title: name, ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: false}
	return value
}

func requireAnalysis(dumpDir string) (analysis.Report, error) {
	if dumpDir == "" {
		return analysis.Report{}, errors.New("static analysis is disabled: configure a dump directory")
	}
	return analysis.AnalyzeDump(dumpDir)
}

func scanFixedFiles(root, fileName string, patterns []string) (any, error) {
	if root == "" {
		return nil, errors.New("analysis is disabled: configure a dump directory")
	}
	var matches []map[string]any
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !entry.Type().IsRegular() || !strings.EqualFold(entry.Name(), fileName) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var found []string
		for _, pattern := range patterns {
			if strings.Contains(strings.ToLower(string(data)), strings.ToLower(pattern)) {
				found = append(found, pattern)
			}
		}
		relative, _ := filepath.Rel(root, path)
		matches = append(matches, map[string]any{"path": filepath.ToSlash(relative), "rules": found})
		return nil
	})
	return map[string]any{"files": matches, "count": len(matches)}, err
}

func listFixedTree(root, directory string) (any, error) {
	if root == "" {
		return nil, errors.New("analysis is disabled: configure a dump directory")
	}
	target := filepath.Join(root, directory)
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		return map[string]any{"objects": []string{}, "count": 0}, nil
	}
	var paths []string
	err := filepath.WalkDir(target, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && entry.Type().IsRegular() {
			relative, _ := filepath.Rel(root, path)
			paths = append(paths, filepath.ToSlash(relative))
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan %s: %w", directory, err)
	}
	return map[string]any{"objects": paths, "count": len(paths)}, nil
}
