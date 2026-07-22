package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"mcp-1c-analog/internal/ditrix"
	"mcp-1c-analog/internal/mcp"
)

var (
	bslFencePattern         = regexp.MustCompile("(?s)```(?:bsl|1c)?[ \\t]*\\r?\\n(.*?)\\r?\\n```")
	frontMatterValuePattern = regexp.MustCompile(`(?m)^([A-Za-z][A-Za-z0-9]*):[ \t]*(.+?)[ \t]*$`)
)

func registerRSVCompatibilityTools(server *mcp.Server, remote *ditrix.Client, project, workDir string, available []ditrix.Tool, options DitrixRegistrationOptions) {
	has := map[string]bool{}
	for _, item := range available {
		has[item.Name] = true
	}
	if has["list_projects"] {
		addIfMissing(server, readOnlyTool("list_workspace_projects", "List every project in the current EDT workspace with its state, path and project nature.", objectSchema(), func(ctx context.Context, args map[string]any) (any, error) {
			result, err := remote.CallTool(ctx, "list_projects", map[string]any{})
			if err != nil {
				return nil, err
			}
			return mcp.RawToolResult(result), nil
		}))
	}
	if has["get_metadata_details"] && has["get_module_structure"] && has["read_module_source"] {
		addIfMissing(server, aiContextTool(remote, project, has))
	}
	if has["search_in_code"] {
		addIfMissing(server, codeSearchTool(remote, project, has))
	}
	if has["read_module_source"] && has["write_module_source"] {
		addIfMissing(server, safeWriteModuleTool(remote, project, workDir, has))
	}
	registerMediumCompatibilityTools(server, remote, project, workDir, has, options)
	registerLargeSubsystems(server, remote, project, has, options)
}

func aiContextTool(remote *ditrix.Client, project string, available map[string]bool) mcp.Tool {
	return readOnlyTool("ai_context", "Collect compact, standard or full EDT context for one metadata object or BSL module in one call.", schema(map[string]any{
		"target":      field("string", "Metadata FQN or module path from src/."),
		"target_type": enumField("Target kind: auto, object, form or module.", "auto", "object", "form", "module"),
		"depth":       enumField("Context depth: minimal, standard or full.", "minimal", "standard", "full"),
	}, "target"), func(ctx context.Context, args map[string]any) (any, error) {
		target := strings.TrimSpace(requiredString(args, "target"))
		if target == "" {
			return nil, errors.New("target is required")
		}
		kind := strings.ToLower(strings.TrimSpace(requiredString(args, "target_type")))
		if kind == "" || kind == "auto" {
			if strings.HasSuffix(strings.ToLower(target), ".bsl") {
				kind = "module"
			} else if strings.Contains(strings.ToLower(target), ".form.") || strings.HasPrefix(strings.ToLower(target), "commonform.") {
				kind = "form"
			} else {
				kind = "object"
			}
		}
		depth := strings.ToLower(strings.TrimSpace(requiredString(args, "depth")))
		if depth == "" {
			depth = "standard"
		}
		if depth != "minimal" && depth != "standard" && depth != "full" {
			return nil, errors.New("depth must be minimal, standard or full")
		}
		result := map[string]any{"project": project, "target": target, "target_type": kind, "depth": depth}
		switch kind {
		case "object", "form":
			details, err := remote.CallTool(ctx, "get_metadata_details", map[string]any{
				"projectName": project, "objectFqns": []string{target}, "full": depth == "full",
			})
			if err != nil {
				return nil, err
			}
			result["details"] = details
			if depth == "full" && available["find_references"] && kind == "object" {
				references, err := remote.CallTool(ctx, "find_references", map[string]any{
					"projectName": project, "objectFqn": target, "limit": 100,
				})
				if err != nil {
					return nil, err
				}
				result["references"] = references
			}
		case "module":
			structureArgs := map[string]any{"projectName": project, "modulePath": target, "responseFormat": "concise"}
			if depth == "full" {
				structureArgs["responseFormat"] = "detailed"
				structureArgs["includeVariables"] = true
				structureArgs["includeComments"] = true
			}
			structure, err := remote.CallTool(ctx, "get_module_structure", structureArgs)
			if err != nil {
				return nil, err
			}
			result["structure"] = structure
			if depth != "minimal" {
				source, err := remote.CallTool(ctx, "read_module_source", map[string]any{"projectName": project, "modulePath": target})
				if err != nil {
					return nil, err
				}
				result["source"] = source
			}
			if depth == "full" && available["get_method_call_hierarchy"] {
				outgoing, err := remote.CallTool(ctx, "get_method_call_hierarchy", map[string]any{
					"projectName": project, "modulePath": target, "direction": "outgoing", "limit": 200,
				})
				if err != nil {
					return nil, err
				}
				result["outgoing_calls"] = outgoing
			}
		default:
			return nil, errors.New("target_type must be auto, object, form or module")
		}
		if depth == "full" && available["get_project_errors"] {
			diagnostics, err := remote.CallTool(ctx, "get_project_errors", map[string]any{
				"projectName": project, "limit": 200, "responseFormat": "detailed",
			})
			if err != nil {
				return nil, err
			}
			result["diagnostics"] = diagnostics
		}
		return result, nil
	})
}

func codeSearchTool(remote *ditrix.Client, project string, available map[string]bool) mcp.Tool {
	return readOnlyTool("code_search", "Unified semantic and text navigation across BSL code. Operations: textSearch, objectReferences, methodReferences, resolveSymbol, callHierarchy and help.", schema(map[string]any{
		"operation":      enumField("Search operation.", "textSearch", "objectReferences", "methodReferences", "resolveSymbol", "callHierarchy", "help"),
		"query":          field("string", "Text, metadata FQN or symbol depending on operation."),
		"module_path":    field("string", "Module path from src/ for method and position operations."),
		"method_name":    field("string", "Method name for methodReferences/callHierarchy."),
		"direction":      enumField("Call hierarchy direction.", "callers", "callees", "outgoing"),
		"line":           field("number", "1-based line for position-based symbol resolution."),
		"column":         field("number", "1-based column for position-based symbol resolution."),
		"limit":          field("number", "Maximum results; default 100, maximum 500."),
		"case_sensitive": field("boolean", "Case-sensitive text search."),
		"regex":          field("boolean", "Treat textSearch query as a regular expression."),
		"file_mask":      field("string", "Optional module path filter for textSearch."),
	}, "operation"), func(ctx context.Context, args map[string]any) (any, error) {
		operation := strings.TrimSpace(requiredString(args, "operation"))
		query := strings.TrimSpace(requiredString(args, "query"))
		modulePath := strings.TrimSpace(requiredString(args, "module_path"))
		methodName := strings.TrimSpace(requiredString(args, "method_name"))
		limit := clamp(intArg(args, "limit", 100), 1, 500)
		var toolName string
		call := map[string]any{"projectName": project}
		switch operation {
		case "textSearch":
			if query == "" {
				return nil, errors.New("query is required for textSearch")
			}
			toolName = "search_in_code"
			call["query"], call["limit"] = query, limit
			call["caseSensitive"], _ = args["case_sensitive"].(bool)
			call["isRegex"], _ = args["regex"].(bool)
			if mask := strings.TrimSpace(requiredString(args, "file_mask")); mask != "" {
				call["fileMask"] = mask
			}
		case "objectReferences":
			if !available["find_references"] {
				return nil, errors.New("objectReferences is unavailable in the installed EDT backend")
			}
			if query == "" {
				return nil, errors.New("query must contain a metadata FQN")
			}
			toolName, call["objectFqn"], call["limit"] = "find_references", query, limit
		case "methodReferences", "callHierarchy":
			if !available["get_method_call_hierarchy"] {
				return nil, errors.New("method call hierarchy is unavailable in the installed EDT backend")
			}
			if modulePath == "" || methodName == "" {
				return nil, errors.New("module_path and method_name are required")
			}
			toolName = "get_method_call_hierarchy"
			call["modulePath"], call["methodName"], call["limit"] = modulePath, methodName, limit
			direction := strings.TrimSpace(requiredString(args, "direction"))
			if operation == "methodReferences" || direction == "" {
				direction = "callers"
			}
			call["direction"] = direction
		case "resolveSymbol":
			positionBased := intArg(args, "line", 0) > 0 && intArg(args, "column", 0) > 0 && available["get_symbol_info"]
			if query == "" && !positionBased {
				return nil, errors.New("query must contain a symbol unless line and column are supplied")
			}
			if positionBased {
				if modulePath == "" {
					return nil, errors.New("module_path is required for position-based resolution")
				}
				toolName = "get_symbol_info"
				call["modulePath"], call["line"], call["column"] = modulePath, intArg(args, "line", 0), intArg(args, "column", 0)
			} else {
				if !available["go_to_definition"] {
					return nil, errors.New("symbol resolution is unavailable in the installed EDT backend")
				}
				toolName, call["symbol"], call["includeSource"] = "go_to_definition", query, true
				if modulePath != "" {
					call["modulePath"] = modulePath
				}
			}
		case "help":
			return map[string]any{"operations": []map[string]string{
				{"name": "textSearch", "requires": "query"},
				{"name": "objectReferences", "requires": "query=metadata FQN"},
				{"name": "methodReferences", "requires": "module_path, method_name"},
				{"name": "resolveSymbol", "requires": "query; optional module_path/line/column"},
				{"name": "callHierarchy", "requires": "module_path, method_name; optional direction"},
			}}, nil
		default:
			return nil, errors.New("unsupported operation")
		}
		result, err := remote.CallTool(ctx, toolName, call)
		if err != nil {
			return nil, err
		}
		return mcp.RawToolResult(result), nil
	})
}

func safeWriteModuleTool(remote *ditrix.Client, project, workDir string, available map[string]bool) mcp.Tool {
	return metadataTool("write_module_source", "Safely edit a BSL module with dry-run, automatic backups, optimistic locking, six edit modes, a 50% deletion guard and post-write EDT validation.", schema(map[string]any{
		"modulePath":         field("string", "Module path from src/."),
		"objectName":         field("string", "Alternative Type.Name module target."),
		"moduleType":         field("string", "ObjectModule, ManagerModule, FormModule, CommandModule, RecordSetModule or Module."),
		"formName":           field("string", "Form name for FormModule."),
		"commandName":        field("string", "Command name for CommandModule."),
		"source":             field("string", "Replacement or inserted BSL source."),
		"oldSource":          field("string", "Exact old fragment for searchReplace."),
		"mode":               enumField("Edit mode.", "replace", "searchReplace", "append", "replaceLines", "replaceMethod", "insertBefore", "insertAfter"),
		"startLine":          field("number", "First 1-based line for replaceLines."),
		"endLine":            field("number", "Last 1-based line for replaceLines."),
		"methodName":         field("string", "Procedure/function name for replaceMethod."),
		"anchor":             field("string", "Unique anchor for insertBefore/insertAfter."),
		"expectedHash":       field("string", "Optional content hash from a previous read."),
		"expectedSource":     field("string", "Optional exact source from a previous read."),
		"overwrite":          field("boolean", "Compatibility flag; the wrapper still reads and locks the current revision."),
		"dryRun":             field("boolean", "Preview without writing; default true."),
		"allowLargeDeletion": field("boolean", "Allow removal of more than 50% of non-empty lines."),
		"skipSyntaxCheck":    field("boolean", "Forwarded to EDT; default false."),
	}, "source"), localWrite("Safely write BSL module"), func(ctx context.Context, args map[string]any) (any, error) {
		target, err := moduleTarget(args)
		if err != nil {
			return nil, err
		}
		current, currentHash, err := readCompleteModule(ctx, remote, project, target)
		if err != nil {
			return nil, err
		}
		if expected := strings.TrimSpace(requiredString(args, "expectedHash")); expected != "" && expected != currentHash {
			return nil, errors.New("expectedHash does not match the current module; read it again before editing")
		}
		if expected, supplied := args["expectedSource"].(string); supplied && strings.ReplaceAll(expected, "\r\n", "\n") != strings.ReplaceAll(current, "\r\n", "\n") {
			return nil, errors.New("expectedSource does not match the current module; read it again before editing")
		}
		mode := strings.TrimSpace(requiredString(args, "mode"))
		if mode == "" {
			mode = "searchReplace"
			args["mode"] = mode
		}
		if mode == "replaceMethod" && !available["read_method_source"] {
			return nil, errors.New("replaceMethod is unavailable in the installed EDT backend")
		}
		updated, err := applyModuleEdit(ctx, remote, project, target, current, args)
		if err != nil {
			return nil, err
		}
		if len(updated) > 500000 {
			return nil, errors.New("resulting module exceeds the 500000 character safety limit")
		}
		removedRatio := deletionRatio(current, updated)
		allowLarge, _ := args["allowLargeDeletion"].(bool)
		if removedRatio > 0.5 && !allowLarge {
			return nil, fmt.Errorf("edit removes %.0f%% of non-empty lines; set allowLargeDeletion=true after reviewing a dry run", removedRatio*100)
		}
		dryRun := true
		if supplied, ok := args["dryRun"].(bool); ok {
			dryRun = supplied
		}
		summary := moduleChangeSummary(current, updated)
		summary["dry_run"] = dryRun
		summary["content_hash_before"] = currentHash
		summary["content_hash_after"] = contentHash(updated)
		summary["removed_ratio"] = removedRatio
		if dryRun || current == updated {
			if current == updated {
				summary["written"] = false
			}
			return summary, nil
		}
		backupPath, err := writeModuleBackup(workDir, project, target, current)
		if err != nil {
			return nil, err
		}
		call := map[string]any{"projectName": project, "source": updated, "mode": "replace", "expectedHash": currentHash}
		copyModuleTarget(call, target)
		if skip, ok := args["skipSyntaxCheck"].(bool); ok {
			call["skipSyntaxCheck"] = skip
		}
		writeResult, err := remote.CallTool(ctx, "write_module_source", call)
		if err != nil {
			return nil, err
		}
		summary["backup"] = backupPath
		summary["write"] = writeResult
		if available["get_project_errors"] {
			validation, validationErr := remote.CallTool(ctx, "get_project_errors", map[string]any{
				"projectName": project, "limit": 200, "responseFormat": "detailed",
			})
			if validationErr != nil {
				summary["validation_error"] = validationErr.Error()
			} else {
				summary["validation"] = validation
			}
		}
		return summary, nil
	})
}

type resolvedModuleTarget struct {
	ModulePath  string
	ObjectName  string
	ModuleType  string
	FormName    string
	CommandName string
}

func moduleTarget(args map[string]any) (resolvedModuleTarget, error) {
	target := resolvedModuleTarget{
		ModulePath: strings.TrimSpace(requiredString(args, "modulePath")), ObjectName: strings.TrimSpace(requiredString(args, "objectName")),
		ModuleType: strings.TrimSpace(requiredString(args, "moduleType")), FormName: strings.TrimSpace(requiredString(args, "formName")),
		CommandName: strings.TrimSpace(requiredString(args, "commandName")),
	}
	if (target.ModulePath == "") == (target.ObjectName == "") {
		return target, errors.New("pass exactly one of modulePath or objectName")
	}
	if target.ModulePath == "" {
		resolved, err := modulePathForObject(target)
		if err != nil {
			return target, err
		}
		target.ModulePath = resolved
	}
	clean := filepath.ToSlash(filepath.Clean(target.ModulePath))
	if filepath.IsAbs(target.ModulePath) || clean == ".." || strings.HasPrefix(clean, "../") || !strings.HasSuffix(strings.ToLower(clean), ".bsl") {
		return target, errors.New("modulePath must be a relative .bsl path below the fixed project's src directory")
	}
	target.ModulePath = clean
	return target, nil
}

func modulePathForObject(target resolvedModuleTarget) (string, error) {
	parts := strings.SplitN(target.ObjectName, ".", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[1]) == "" {
		return "", errors.New("objectName must use Type.Name form")
	}
	folders := map[string]string{
		"catalog": "Catalogs", "document": "Documents", "enum": "Enums", "constant": "Constants",
		"dataprocessor": "DataProcessors", "report": "Reports", "commonmodule": "CommonModules",
		"commonform": "CommonForms", "commoncommand": "CommonCommands", "informationregister": "InformationRegisters",
		"accumulationregister": "AccumulationRegisters", "accountingregister": "AccountingRegisters",
		"calculationregister": "CalculationRegisters", "exchangeplan": "ExchangePlans", "businessprocess": "BusinessProcesses",
		"task": "Tasks", "chartofaccounts": "ChartsOfAccounts", "chartofcharacteristictypes": "ChartsOfCharacteristicTypes",
		"chartofcalculationtypes": "ChartsOfCalculationTypes", "documentjournal": "DocumentJournals",
	}
	folder := folders[strings.ToLower(strings.TrimSpace(parts[0]))]
	if folder == "" {
		return "", fmt.Errorf("objectName type %q cannot be mapped safely; use modulePath", parts[0])
	}
	name := strings.TrimSpace(parts[1])
	if strings.ContainsAny(name, `/\\`) || name == "." || name == ".." {
		return "", errors.New("objectName contains unsafe path characters")
	}
	moduleType := strings.TrimSpace(target.ModuleType)
	if moduleType == "" {
		moduleType = "ObjectModule"
		if strings.EqualFold(parts[0], "CommonModule") {
			moduleType = "Module"
		} else if strings.EqualFold(parts[0], "CommonForm") {
			moduleType = "Module"
		} else if strings.EqualFold(parts[0], "CommonCommand") {
			moduleType = "CommandModule"
		}
	}
	base := filepath.ToSlash(filepath.Join(folder, name))
	switch moduleType {
	case "FormModule":
		if strings.EqualFold(parts[0], "CommonForm") {
			return filepath.ToSlash(filepath.Join(base, "Module.bsl")), nil
		}
		if target.FormName == "" {
			return "", errors.New("formName is required for FormModule")
		}
		return filepath.ToSlash(filepath.Join(base, "Forms", target.FormName, "Module.bsl")), nil
	case "CommandModule":
		if strings.EqualFold(parts[0], "CommonCommand") {
			return filepath.ToSlash(filepath.Join(base, "CommandModule.bsl")), nil
		}
		if target.CommandName == "" {
			return "", errors.New("commandName is required for CommandModule")
		}
		return filepath.ToSlash(filepath.Join(base, "Commands", target.CommandName, "CommandModule.bsl")), nil
	case "ObjectModule", "ManagerModule", "RecordSetModule", "Module":
		return filepath.ToSlash(filepath.Join(base, moduleType+".bsl")), nil
	default:
		return "", errors.New("unsupported moduleType")
	}
}

func copyModuleTarget(target map[string]any, source resolvedModuleTarget) {
	if source.ModulePath != "" {
		target["modulePath"] = source.ModulePath
	} else {
		target["objectName"], target["moduleType"] = source.ObjectName, source.ModuleType
		if source.FormName != "" {
			target["formName"] = source.FormName
		}
		if source.CommandName != "" {
			target["commandName"] = source.CommandName
		}
	}
}

func readCompleteModule(ctx context.Context, remote *ditrix.Client, project string, target resolvedModuleTarget) (string, string, error) {
	var chunks []string
	hash := ""
	startLine := 0
	for page := 0; page < 1000; page++ {
		call := map[string]any{"projectName": project}
		copyModuleTarget(call, target)
		if startLine > 0 {
			call["startLine"] = startLine
		}
		result, err := remote.CallTool(ctx, "read_module_source", call)
		if err != nil {
			return "", "", err
		}
		text := mcpText(result)
		source, values, err := parseBSLResponse(text)
		if err != nil {
			return "", "", err
		}
		chunks = append(chunks, source)
		if hash == "" {
			hash = values["contentHash"]
		}
		next, _ := strconv.Atoi(values["nextStartLine"])
		if !strings.EqualFold(values["truncated"], "true") || next <= startLine {
			break
		}
		startLine = next
	}
	if hash == "" {
		hash = contentHash(strings.Join(chunks, ""))
	}
	return strings.Join(chunks, ""), hash, nil
}

func applyModuleEdit(ctx context.Context, remote *ditrix.Client, project string, target resolvedModuleTarget, current string, args map[string]any) (string, error) {
	mode := requiredString(args, "mode")
	source := requiredString(args, "source")
	switch mode {
	case "replace":
		return source, nil
	case "append":
		return current + source, nil
	case "searchReplace":
		return replaceUnique(current, requiredString(args, "oldSource"), source)
	case "insertBefore", "insertAfter":
		anchor := requiredString(args, "anchor")
		if anchor == "" {
			return "", errors.New("anchor is required")
		}
		if mode == "insertBefore" {
			return replaceUnique(current, anchor, source+anchor)
		}
		return replaceUnique(current, anchor, anchor+source)
	case "replaceLines":
		start, end := intArg(args, "startLine", 0), intArg(args, "endLine", 0)
		if start < 1 || end < start {
			return "", errors.New("startLine and endLine must define a valid 1-based inclusive range")
		}
		return replaceLineRange(current, start, end, source)
	case "replaceMethod":
		method := strings.TrimSpace(requiredString(args, "methodName"))
		if method == "" {
			return "", errors.New("methodName is required")
		}
		call := map[string]any{"projectName": project, "modulePath": target.ModulePath, "methodName": method}
		result, err := remote.CallTool(ctx, "read_method_source", call)
		if err != nil {
			return "", err
		}
		oldMethod, _, err := parseBSLResponse(mcpText(result))
		if err != nil {
			return "", err
		}
		return replaceUnique(current, oldMethod, source)
	default:
		return "", errors.New("mode must be replace, searchReplace, append, replaceLines, replaceMethod, insertBefore or insertAfter")
	}
}

func replaceUnique(text, old, replacement string) (string, error) {
	if old == "" {
		return "", errors.New("oldSource/anchor cannot be empty")
	}
	if count := strings.Count(text, old); count != 1 {
		return "", fmt.Errorf("edit target must occur exactly once; found %d matches", count)
	}
	return strings.Replace(text, old, replacement, 1), nil
}

func replaceLineRange(text string, start, end int, replacement string) (string, error) {
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	trailing := strings.HasSuffix(normalized, "\n")
	lines := strings.Split(strings.TrimSuffix(normalized, "\n"), "\n")
	if start > len(lines) || end > len(lines) {
		return "", fmt.Errorf("line range %d-%d exceeds module length %d", start, end, len(lines))
	}
	replacementLines := strings.Split(strings.TrimSuffix(strings.ReplaceAll(replacement, "\r\n", "\n"), "\n"), "\n")
	if replacement == "" {
		replacementLines = nil
	}
	updated := append(append([]string{}, lines[:start-1]...), replacementLines...)
	updated = append(updated, lines[end:]...)
	result := strings.Join(updated, "\n")
	if trailing {
		result += "\n"
	}
	return result, nil
}

func parseBSLResponse(text string) (string, map[string]string, error) {
	match := bslFencePattern.FindStringSubmatch(text)
	if match == nil {
		return "", nil, errors.New("EDT response did not contain a fenced BSL source block")
	}
	values := map[string]string{}
	for _, item := range frontMatterValuePattern.FindAllStringSubmatch(text[:strings.Index(text, match[0])], -1) {
		values[item[1]] = strings.Trim(strings.TrimSpace(item[2]), "\"'")
	}
	return match[1], values, nil
}

func mcpText(result map[string]any) string {
	items, _ := result["content"].([]any)
	var parts []string
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		switch item["type"] {
		case "text":
			if value, _ := item["text"].(string); value != "" {
				parts = append(parts, value)
			}
		case "resource":
			resource, _ := item["resource"].(map[string]any)
			if value, _ := resource["text"].(string); value != "" {
				parts = append(parts, value)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func deletionRatio(before, after string) float64 {
	oldCount, newCount := nonEmptyLines(before), nonEmptyLines(after)
	if oldCount == 0 || newCount >= oldCount {
		return 0
	}
	return float64(oldCount-newCount) / float64(oldCount)
}

func nonEmptyLines(value string) int {
	count := 0
	for _, line := range strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}

func moduleChangeSummary(before, after string) map[string]any {
	beforeLines, afterLines := strings.Count(before, "\n")+1, strings.Count(after, "\n")+1
	return map[string]any{
		"changed": before != after, "characters_before": len(before), "characters_after": len(after),
		"lines_before": beforeLines, "lines_after": afterLines,
		"preview_before": boundedPreview(before), "preview_after": boundedPreview(after),
	}
}

func boundedPreview(value string) string {
	const maximum = 4000
	if len(value) <= maximum {
		return value
	}
	return value[:maximum] + "\n... [truncated]"
}

func contentHash(value string) string {
	sum := sha256.Sum256([]byte(strings.ReplaceAll(value, "\r\n", "\n")))
	return hex.EncodeToString(sum[:])
}

func writeModuleBackup(workDir, project string, target resolvedModuleTarget, source string) (string, error) {
	if strings.TrimSpace(workDir) == "" {
		return "", errors.New("module backups are disabled: configure --work-dir")
	}
	root := filepath.Join(workDir, "module-backups")
	name := strings.NewReplacer("/", "_", "\\", "_", ":", "_", ".", "_").Replace(target.ModulePath)
	path := filepath.Join(root, sanitizeFileName(project), time.Now().UTC().Format("20060102T150405.000000000Z")+"-"+name+".bsl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("create module backup directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		return "", fmt.Errorf("write module backup: %w", err)
	}
	return path, nil
}

func sanitizeFileName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "project"
	}
	return strings.Map(func(r rune) rune {
		if r == '<' || r == '>' || r == ':' || r == '"' || r == '/' || r == '\\' || r == '|' || r == '?' || r == '*' || r < 32 {
			return '_'
		}
		return r
	}, value)
}

func enumField(description string, values ...string) map[string]any {
	items := make([]any, len(values))
	for index, value := range values {
		items[index] = value
	}
	return map[string]any{"type": "string", "description": description, "enum": items}
}
