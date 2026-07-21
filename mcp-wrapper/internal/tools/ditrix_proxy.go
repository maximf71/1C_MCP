package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"mcp-1c-analog/internal/ditrix"
	"mcp-1c-analog/internal/mcp"
)

// DitrixProxyReport describes the capabilities that were accepted from the
// separately installed EDT-MCP plug-in. New upstream tools are deliberately
// not exposed until reviewed and added to this allowlist.
type DitrixProxyReport struct {
	ServerName     string   `json:"server_name"`
	ServerVersion  string   `json:"server_version"`
	LockedProject  string   `json:"locked_project"`
	Registered     []string `json:"registered_tools"`
	NativeWins     []string `json:"native_tools_kept"`
	PolicyExcluded []string `json:"policy_excluded_tools"`
}

func RegisterDitrixEDT(ctx context.Context, server *mcp.Server, client *ditrix.Client, project string) (DitrixProxyReport, error) {
	if strings.TrimSpace(project) == "" {
		return DitrixProxyReport{}, fmt.Errorf("fixed EDT project name is required")
	}
	info, err := client.Initialize(ctx)
	if err != nil {
		return DitrixProxyReport{}, err
	}
	remoteTools, err := client.ListTools(ctx)
	if err != nil {
		return DitrixProxyReport{}, err
	}
	report := DitrixProxyReport{ServerName: info.Name, ServerVersion: info.Version, LockedProject: project}
	for _, remote := range remoteTools {
		if server.HasTool(remote.Name) {
			report.NativeWins = append(report.NativeWins, remote.Name)
			continue
		}
		policy, allowed := ditrixToolPolicies[remote.Name]
		if !allowed {
			report.PolicyExcluded = append(report.PolicyExcluded, remote.Name)
			continue
		}
		remote := remote
		annotation := annotationsForRemote(remote, policy)
		server.AddTool(mcp.Tool{
			Name:        remote.Name,
			Description: "[DitriX EDT-MCP " + info.Version + "; fixed project " + project + "] " + remote.Description,
			InputSchema: remote.InputSchema,
			Annotations: annotation,
			Handler: func(callCtx context.Context, args map[string]any) (any, error) {
				locked, err := lockDitrixArguments(remote, args, project)
				if err != nil {
					return nil, err
				}
				result, err := client.CallTool(callCtx, remote.Name, locked)
				if err != nil {
					return nil, err
				}
				return mcp.RawToolResult(result), nil
			},
		})
		report.Registered = append(report.Registered, remote.Name)
	}
	for _, remote := range remoteTools {
		if remote.Name != "run_yaxunit_tests" || server.HasTool("run_tests") {
			continue
		}
		remote := remote
		server.AddTool(mcp.Tool{
			Name:        "run_tests",
			Description: "Run YAxUnit tests through the fixed EDT project using the reviewed DitriX backend.",
			InputSchema: remote.InputSchema,
			Annotations: localWrite("Run YAxUnit tests"),
			Handler: func(callCtx context.Context, args map[string]any) (any, error) {
				locked, err := lockDitrixArguments(remote, args, project)
				if err != nil {
					return nil, err
				}
				result, err := client.CallTool(callCtx, remote.Name, locked)
				if err != nil {
					return nil, err
				}
				return mcp.RawToolResult(result), nil
			},
		})
		report.Registered = append(report.Registered, "run_tests")
	}
	sort.Strings(report.Registered)
	sort.Strings(report.NativeWins)
	sort.Strings(report.PolicyExcluded)
	server.AddTool(mcp.Tool{
		Name:        "get_ditrix_edt_capabilities",
		Description: "Show the DitriX EDT-MCP version, fixed project, proxied tools, native overrides, and policy exclusions.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
		Annotations: readOnly("Inspect integrated EDT-MCP capabilities"),
		Handler: func(context.Context, map[string]any) (any, error) {
			return report, nil
		},
	})
	return report, nil
}

type ditrixPolicy struct {
	readOnly    bool
	destructive bool
}

var ditrixToolPolicies = func() map[string]ditrixPolicy {
	result := map[string]ditrixPolicy{}
	for _, name := range []string{
		"get_edt_version", "get_server_status", "get_tool_guide", "get_configuration_properties",
		"get_problem_summary", "get_project_errors", "get_markers", "get_check_description",
		"get_content_assist", "get_platform_documentation", "get_metadata_objects", "get_metadata_details",
		"list_subsystems", "get_subsystem_content", "find_references", "get_tags", "get_objects_by_tags",
		"get_applications", "list_configurations", "debug_status", "list_breakpoints", "get_variables",
		"get_profiling_results", "read_module_source", "get_module_structure", "list_modules",
		"read_method_source", "get_method_call_hierarchy", "go_to_definition", "get_symbol_info",
		"get_form_layout_snapshot", "get_form_screenshot", "get_template_screenshot", "list_common_pictures",
		"get_outgoing_structures", "get_mcp_history", "get_translation_project_info", "search_in_code",
		"wait_for_break", "list_toolsets",
	} {
		result[name] = ditrixPolicy{readOnly: true}
	}
	for _, name := range []string{
		"clean_project", "revalidate_objects", "write_module_source", "set_breakpoint", "remove_breakpoint",
		"resume", "step", "evaluate_expression", "start_profiling", "stop_profiling", "run_yaxunit_tests",
		"debug_yaxunit_tests", "debug_launch", "terminate_launch", "create_metadata", "rename_metadata_object",
		"modify_metadata", "set_variable", "generate_translation_strings", "translate_configuration",
		"export_common_picture", "build_external_objects", "resync_to_disk",
	} {
		result[name] = ditrixPolicy{}
	}
	for _, name := range []string{"delete_metadata", "update_database"} {
		result[name] = ditrixPolicy{destructive: true}
	}
	return result
}()

func annotationsForRemote(tool ditrix.Tool, policy ditrixPolicy) *mcp.Annotations {
	title := "EDT: " + tool.Name
	if tool.Annotations != nil && tool.Annotations.Title != "" {
		title = tool.Annotations.Title
	}
	// Local policy is authoritative. It prevents a missing or overly permissive
	// upstream annotation from turning a write into an auto-approved read.
	return &mcp.Annotations{
		Title:           title,
		ReadOnlyHint:    policy.readOnly,
		DestructiveHint: policy.destructive,
		IdempotentHint:  policy.readOnly,
		OpenWorldHint:   false,
	}
}

func lockDitrixArguments(tool ditrix.Tool, args map[string]any, project string) (map[string]any, error) {
	locked := make(map[string]any, len(args)+1)
	for key, value := range args {
		if forbiddenDitrixArgument(key) {
			return nil, fmt.Errorf("argument %q is disabled by the fixed-project proxy policy", key)
		}
		locked[key] = value
	}
	properties, _ := tool.InputSchema["properties"].(map[string]any)
	if _, acceptsProject := properties["projectName"]; acceptsProject {
		if supplied, ok := locked["projectName"]; ok && fmt.Sprint(supplied) != project {
			return nil, fmt.Errorf("tool %s is locked to EDT project %q", tool.Name, project)
		}
		locked["projectName"] = project
	}
	return locked, nil
}

func forbiddenDitrixArgument(name string) bool {
	switch strings.ToLower(name) {
	case "logdir", "directory", "outputdirectory", "sourcedirectory", "targetdirectory",
		"outputdir", "sourcedir", "targetdir", "importpath",
		"xmldirectory", "projectpath", "repositorypath", "configurationpath", "databasepath",
		"filesystempath", "infobasepath":
		return true
	default:
		return false
	}
}
