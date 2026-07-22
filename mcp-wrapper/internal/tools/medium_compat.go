package tools

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"mcp-1c-analog/internal/ditrix"
	"mcp-1c-analog/internal/mcp"
)

const maximumHelpBytes = 1 << 20

var (
	htmlTagPattern    = regexp.MustCompile(`(?s)<[^>]*>`)
	htmlSpacePattern  = regexp.MustCompile(`[\t\r\n ]+`)
	htmlUnsafePattern = regexp.MustCompile(`(?is)<(?:script|style)[^>]*>.*?</(?:script|style)>`)
	languagePattern   = regexp.MustCompile(`^[a-z]{2}(?:-[a-z0-9]+)?$`)
)

func registerMediumCompatibilityTools(server *mcp.Server, remote *ditrix.Client, project, workDir string,
	available map[string]bool, options DitrixRegistrationOptions,
) {
	if available["list_projects"] && available["get_metadata_details"] {
		addIfMissing(server, objectHelpTool(remote, project))
	}
	addIfMissing(server, launchDebuggerTool(remote, project, available))
	addIfMissing(server, metadataFacadeTool(remote, project, available))
	addIfMissing(server, codeReviewTool(remote, project, workDir, available, options))
}

func objectHelpTool(remote *ditrix.Client, project string) mcp.Tool {
	return readOnlyTool("get_object_help", "Read an EDT object's localized Help HTML together with plain text and metadata details. The object is resolved only inside the fixed project.", schema(map[string]any{
		"object_fqn": field("string", "Metadata FQN, for example Catalog.Products or Catalog.Products.Form.ItemForm."),
		"language":   field("string", "Help language code; default ru."),
	}, "object_fqn"), func(ctx context.Context, args map[string]any) (any, error) {
		fqn := strings.TrimSpace(requiredString(args, "object_fqn"))
		language := strings.ToLower(strings.TrimSpace(requiredString(args, "language")))
		if language == "" {
			language = "ru"
		}
		if !languagePattern.MatchString(language) {
			return nil, errors.New("language must be a short locale code")
		}
		root, err := resolveEDTProjectRoot(ctx, remote, project)
		if err != nil {
			return nil, err
		}
		relative, err := helpRelativePath(fqn, language)
		if err != nil {
			return nil, err
		}
		path, err := pathBelowRoot(root, relative)
		if err != nil {
			return nil, err
		}
		data, err := readLimitedFile(path, maximumHelpBytes)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, fmt.Errorf("help %s is not defined for %s", language, fqn)
			}
			return nil, fmt.Errorf("read object help: %w", err)
		}
		details, err := remote.CallTool(ctx, "get_metadata_details", map[string]any{
			"projectName": project, "objectFqns": []string{fqn}, "full": false,
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"project": project, "object_fqn": fqn, "language": language,
			"help_file": filepath.ToSlash(relative), "html": string(data),
			"text": htmlToText(string(data)), "metadata_details": details,
		}, nil
	})
}

func launchDebuggerTool(remote *ditrix.Client, project string, available map[string]bool) mcp.Tool {
	return mcp.Tool{
		Name:        "launch_debugger",
		Description: "Unified fixed-project EDT debugger. Read operations are immediate; launch, execution control, breakpoints, expression evaluation and variable changes require confirm=true.",
		InputSchema: schema(map[string]any{
			"operation": enumField("Debugger operation.", "help", "applications", "configurations", "launch", "status", "addBreakpoint", "removeBreakpoint", "listBreakpoints", "wait", "variables", "evaluate", "setVariable", "stepOver", "stepInto", "stepOut", "resume", "terminate"),
			"arguments": map[string]any{"type": "object", "description": "Arguments of the selected EDT debugger operation.", "additionalProperties": true},
			"confirm":   field("boolean", "Required for operations that change or execute the debug session."),
		}, "operation"),
		Annotations: localWrite("Control the fixed EDT debugger"),
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			op := strings.TrimSpace(requiredString(args, "operation"))
			if op == "help" {
				return map[string]any{"project": project, "operations": debuggerOperations()}, nil
			}
			mapping := map[string]string{
				"applications": "get_applications", "configurations": "list_configurations", "launch": "debug_launch",
				"status": "debug_status", "addBreakpoint": "set_breakpoint", "removeBreakpoint": "remove_breakpoint",
				"listBreakpoints": "list_breakpoints", "wait": "wait_for_break", "variables": "get_variables",
				"evaluate": "evaluate_expression", "setVariable": "set_variable", "stepOver": "step",
				"stepInto": "step", "stepOut": "step", "resume": "resume", "terminate": "terminate_launch",
			}
			toolName := mapping[op]
			if toolName == "" {
				return nil, errors.New("unknown debugger operation")
			}
			if !available[toolName] {
				return nil, fmt.Errorf("operation %s is unavailable in the installed EDT backend", op)
			}
			readOnly := op == "applications" || op == "configurations" || op == "status" || op == "listBreakpoints" || op == "wait" || op == "variables"
			if !readOnly && !boolValue(args["confirm"]) {
				return nil, fmt.Errorf("%s requires confirm=true", op)
			}
			call, err := nestedArguments(args)
			if err != nil {
				return nil, err
			}
			if op == "applications" || op == "configurations" || op == "launch" || op == "addBreakpoint" || op == "removeBreakpoint" || op == "listBreakpoints" || op == "terminate" {
				call["projectName"] = project
			}
			if op == "launch" {
				if _, set := call["updateBeforeLaunch"]; !set {
					call["updateBeforeLaunch"] = false
				}
			}
			if strings.HasPrefix(op, "step") {
				call["kind"] = map[string]string{"stepOver": "over", "stepInto": "into", "stepOut": "out"}[op]
			}
			if op == "terminate" && boolValue(call["all"]) {
				call["confirm"] = true
			}
			result, err := remote.CallTool(ctx, toolName, call)
			if err != nil {
				return nil, err
			}
			return mcp.RawToolResult(result), nil
		},
	}
}

func metadataFacadeTool(remote *ditrix.Client, project string, available map[string]bool) mcp.Tool {
	operations := append([]string{"help"}, metadataOperations()...)
	return mcp.Tool{
		Name:        "edit_metadata",
		Description: "Full fixed-project metadata dispatcher covering objects, specialized metadata, forms, templates, command interfaces, extensions, HTTP services and DCS. Read operations execute immediately; mutations are dry-run by default and require confirm=true.",
		InputSchema: schema(map[string]any{
			"operation": enumField("Metadata operation.", operations...),
			"arguments": map[string]any{"type": "object", "description": "Arguments accepted by the selected EDT operation.", "additionalProperties": true},
			"confirm":   field("boolean", "Execute a create, modify, delete or DCS mutation."),
		}, "operation"),
		Annotations: localWrite("Edit forms, extensions and DCS"),
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			op := strings.TrimSpace(requiredString(args, "operation"))
			if op == "help" {
				return map[string]any{"project": project, "operations": metadataOperations()}, nil
			}
			mapping := map[string]string{
				"details": "get_metadata_details", "formStructure": "get_metadata_details",
				"formScreenshot": "get_form_screenshot", "formLayout": "get_form_layout_snapshot",
				"create": "create_metadata", "modify": "modify_metadata", "delete": "delete_metadata",
				"setDcs": "modify_metadata", "extensionDetails": "get_metadata_details",
				"extensionHandler": "create_metadata", "extensionResync": "resync_to_disk",
				"createObject": "create_metadata", "removeObject": "delete_metadata", "renameObject": "rename_metadata_object",
				"createForm": "create_metadata", "removeForm": "delete_metadata", "listPictures": "list_common_pictures",
				"createExtensionProject": "create_metadata", "getCommandInterface": "get_metadata_details",
				"listFormConditionalAppearance": "get_metadata_details", "listTemplateDrawings": "get_template_screenshot",
				"listExtensions": "get_metadata_details",
			}
			toolName := mapping[op]
			if toolName == "" && isMetadataSemanticOperation(op) {
				toolName = "modify_metadata"
			}
			if toolName == "" {
				return nil, errors.New("unknown metadata operation")
			}
			if !available[toolName] {
				return nil, fmt.Errorf("operation %s is unavailable in the installed EDT backend", op)
			}
			call, err := nestedArguments(args)
			if err != nil {
				return nil, err
			}
			call["projectName"] = project
			if isMetadataSemanticOperation(op) && toolName == "modify_metadata" {
				call["operation"] = op
			}
			mutating := !isMetadataReadOperation(op)
			if mutating && !boolValue(args["confirm"]) {
				return map[string]any{"dry_run": true, "project": project, "operation": op, "backend_tool": toolName, "arguments": call}, nil
			}
			if op == "delete" || op == "removeObject" || op == "removeForm" {
				call["confirm"] = true
			}
			result, err := remote.CallTool(ctx, toolName, call)
			if err != nil {
				return nil, err
			}
			return mcp.RawToolResult(result), nil
		},
	}
}

func codeReviewTool(remote *ditrix.Client, project, workDir string, available map[string]bool, options DitrixRegistrationOptions) mcp.Tool {
	return readOnlyTool("code_review", "Run BSL Language Server diagnostics for the fixed EDT project or selected modules. The analyzer is an optional separately installed component and is never downloaded automatically.", schema(map[string]any{
		"module_paths":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional module paths below src/. Empty means the whole project."},
		"minimum_severity": enumField("Lowest returned severity.", "hint", "information", "warning", "error"),
		"limit":            field("number", "Maximum diagnostics; default 200, maximum 2000."),
	}, nil...), func(ctx context.Context, args map[string]any) (any, error) {
		if options.BSLLanguageServer == "" {
			return nil, errors.New("code_review requires --bsl-language-server; install BSL Language Server separately and configure its exact executable or JAR path")
		}
		if !available["list_projects"] {
			return nil, errors.New("code_review requires list_projects from the EDT backend")
		}
		root, err := resolveEDTProjectRoot(ctx, remote, project)
		if err != nil {
			return nil, err
		}
		modules, err := stringSlice(args["module_paths"])
		if err != nil {
			return nil, err
		}
		return runBSLReview(ctx, bslReviewOptions{
			ProjectRoot: root, WorkDir: workDir, Executable: options.BSLLanguageServer,
			JavaExecutable: options.JavaExecutable, Config: options.BSLLanguageServerConfig,
			ModulePaths: modules, MinimumSeverity: requiredString(args, "minimum_severity"),
			Limit: clamp(intArg(args, "limit", 200), 1, 2000),
		})
	})
}

func resolveEDTProjectRoot(ctx context.Context, remote *ditrix.Client, project string) (string, error) {
	result, err := remote.CallTool(ctx, "list_projects", map[string]any{})
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(mcpText(result), "\n") {
		columns := strings.Split(strings.TrimSpace(line), "|")
		if len(columns) < 5 {
			continue
		}
		name := strings.TrimSpace(columns[1])
		path := strings.TrimSpace(columns[3])
		if name == project && filepath.IsAbs(path) {
			info, err := os.Stat(path)
			if err == nil && info.IsDir() {
				return filepath.Clean(path), nil
			}
		}
	}
	return "", fmt.Errorf("fixed EDT project %q was not found in list_projects", project)
}

func helpRelativePath(fqn, language string) (string, error) {
	parts := strings.Split(strings.TrimSpace(fqn), ".")
	if len(parts) < 2 {
		return "", errors.New("object_fqn must contain a metadata type and name")
	}
	folders := map[string]string{
		"Catalog": "Catalogs", "Document": "Documents", "InformationRegister": "InformationRegisters",
		"AccumulationRegister": "AccumulationRegisters", "AccountingRegister": "AccountingRegisters",
		"CalculationRegister": "CalculationRegisters", "CommonModule": "CommonModules", "CommonForm": "CommonForms",
		"Report": "Reports", "DataProcessor": "DataProcessors", "BusinessProcess": "BusinessProcesses",
		"Task": "Tasks", "ChartOfAccounts": "ChartsOfAccounts", "ChartOfCharacteristicTypes": "ChartsOfCharacteristicTypes",
		"ChartOfCalculationTypes": "ChartsOfCalculationTypes", "ExchangePlan": "ExchangePlans", "Constant": "Constants",
		"Enum": "Enums", "DefinedType": "DefinedTypes", "Subsystem": "Subsystems",
	}
	folder := folders[parts[0]]
	if folder == "" || !safeMetadataName(parts[1]) {
		return "", fmt.Errorf("unsupported or unsafe metadata FQN %q", fqn)
	}
	pathParts := []string{"src", folder, parts[1]}
	if len(parts) >= 4 && parts[2] == "Form" {
		if !safeMetadataName(parts[3]) {
			return "", errors.New("unsafe form name")
		}
		pathParts = append(pathParts, "Forms", parts[3])
	}
	pathParts = append(pathParts, "Help", language+".html")
	return filepath.Join(pathParts...), nil
}

func safeMetadataName(value string) bool {
	if value == "" || value == "." || value == ".." {
		return false
	}
	return !strings.ContainsAny(value, `/\\:*?"<>|`)
}

func pathBelowRoot(root, relative string) (string, error) {
	target := filepath.Clean(filepath.Join(root, relative))
	rel, err := filepath.Rel(filepath.Clean(root), target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("resolved path escapes the fixed EDT project")
	}
	return target, nil
}

func readLimitedFile(path string, maximum int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > maximum {
		return nil, errors.New("help file is too large")
	}
	data, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maximum {
		return nil, errors.New("help file is too large")
	}
	return data, nil
}

func htmlToText(value string) string {
	value = htmlUnsafePattern.ReplaceAllString(value, " ")
	value = htmlTagPattern.ReplaceAllString(value, " ")
	return strings.TrimSpace(htmlSpacePattern.ReplaceAllString(html.UnescapeString(value), " "))
}

func nestedArguments(args map[string]any) (map[string]any, error) {
	raw, exists := args["arguments"]
	if !exists || raw == nil {
		return map[string]any{}, nil
	}
	values, ok := raw.(map[string]any)
	if !ok {
		return nil, errors.New("arguments must be an object")
	}
	result := make(map[string]any, len(values)+1)
	for key, value := range values {
		if forbiddenDitrixArgument(key) || strings.EqualFold(key, "projectName") {
			return nil, fmt.Errorf("argument %q is controlled by the fixed-project policy", key)
		}
		result[key] = value
	}
	return result, nil
}

func boolValue(value any) bool {
	result, _ := value.(bool)
	return result
}

func stringSlice(value any) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok {
		if typed, ok := value.([]string); ok {
			return typed, nil
		}
		return nil, errors.New("module_paths must be an array of strings")
	}
	result := make([]string, 0, len(items))
	for _, raw := range items {
		item, ok := raw.(string)
		if !ok || strings.TrimSpace(item) == "" {
			return nil, errors.New("module_paths must contain non-empty strings")
		}
		result = append(result, item)
	}
	return result, nil
}

func debuggerOperations() []string {
	result := []string{"applications", "configurations", "launch", "status", "addBreakpoint", "removeBreakpoint", "listBreakpoints", "wait", "variables", "evaluate", "setVariable", "stepOver", "stepInto", "stepOut", "resume", "terminate"}
	sort.Strings(result)
	return result
}

func metadataOperations() []string {
	return []string{
		"details", "formStructure", "formScreenshot", "formLayout", "create", "modify", "delete", "setDcs", "extensionDetails", "extensionHandler", "extensionResync",
		"createObject", "removeObject", "renameObject", "setObjectProperty", "setHelp", "addObjectAttribute", "removeObjectAttribute", "addPredefined", "removePredefined", "addTabularSection", "removeTabularSection", "addTabularSectionAttribute", "removeTabularSectionAttribute", "setValueType", "changeAttributeType", "setBaseProject", "createObjectCommand", "removeCommand",
		"addRegisterField", "removeRegisterField", "addRecorder", "removeRecorder", "addEnumValue", "addSubsystemContent", "removeSubsystemContent", "addFunctionalOptionContent", "removeFunctionalOptionContent", "setRoleRight", "setRoleRestriction", "removeRoleRestriction", "setRestrictionTemplate", "removeRestrictionTemplate", "setDefinedTypeTypes", "addEventSubscriptionHandler", "addAccountExtDimensionType", "removeAccountExtDimensionType", "addExchangePlanContent", "removeExchangePlanContent", "setXdtoNamespace", "addXdtoObjectType", "addXdtoValueType", "addXdtoProperty", "removeXdtoType", "removeXdtoProperty",
		"createForm", "removeForm", "addFormAttribute", "addFormAttributeColumn", "removeFormAttribute", "removeFormAttributeColumn", "addDynamicListTable", "addField", "addGroup", "addButton", "addTable", "addDecoration", "addRadioButton", "setProperty", "setFormFunctionalOptions", "listPictures", "addEventHandler", "addCommandHandler", "setupSettingsComposerOnForm", "addFormConditionalAppearance", "listFormConditionalAppearance", "removeFormConditionalAppearance", "removeFormCommand", "addFormCommandInterfaceItem", "removeFormCommandInterfaceItem", "setFormCommandInterfaceItemProperty",
		"addTemplate", "setTemplateCell", "mergeTemplateCells", "setTemplateArea", "addTemplateDrawing", "listTemplateDrawings", "removeTemplateDrawing", "drawTemplate",
		"getCommandInterface", "setSubsystemsOrder", "setSubsystemVisibility", "addMainSectionCommand", "removeMainSectionCommand", "setMainSectionCommandVisibility", "setSubsystemCommandVisibility", "setCommandPlacement", "setCommandOrder",
		"createExtensionProject", "adoptObject", "adoptObjects", "adoptChild", "adoptFormItem", "updateAdopted", "unadoptChild", "adoptModule", "installExtension", "listExtensions", "setExtensionSecurity", "uninstallExtension",
		"addUrlTemplate", "removeUrlTemplate", "addHttpServiceMethod", "removeHttpServiceMethod",
		"createReportSchema", "repairReportSchema", "addDataSet", "setDataSetProperty", "removeDataSet", "addDataSetField", "removeDataSetField", "addQueryField", "removeQueryField", "addQueryCondition", "addDataSetLink", "setDataSetLinkProperty", "removeDataSetLink", "addSchemaParameter", "setSchemaParameter", "removeSchemaParameter", "moveSchemaParameter", "addCalculatedField", "setCalculatedField", "removeCalculatedField", "addTotalField", "setTotalField", "removeTotalField", "addSettingsGroup", "addSettingsTable", "addSettingsChart", "removeSettingsItem", "addSettingsSelectedField", "removeSettingsSelectedField", "clearSettingsSelectedFields", "addSettingsFilter", "addSettingsFilterGroup", "removeSettingsFilter", "addSettingsOrder", "removeSettingsOrder", "addConditionalAppearance", "setConditionalAppearance", "removeConditionalAppearance", "setDataSetFieldAppearance", "addSettingsVariant", "cloneSettingsVariant", "setSettingsVariantProperty", "removeSettingsVariant", "setSettingsParameter", "removeSettingsParameter", "setOutputParameter", "setSettingsItemUserMode", "addUserField", "moveItem", "removeItem", "syncExport",
	}
}

func isMetadataSemanticOperation(operation string) bool {
	for _, candidate := range metadataOperations() {
		if operation == candidate {
			return true
		}
	}
	return false
}

func isMetadataReadOperation(operation string) bool {
	switch operation {
	case "help", "details", "formStructure", "formScreenshot", "formLayout", "extensionDetails", "listPictures", "listFormConditionalAppearance", "listTemplateDrawings", "getCommandInterface", "listExtensions":
		return true
	default:
		return false
	}
}
