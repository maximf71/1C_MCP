package tools

import (
	"context"
	"fmt"
	"strings"

	"mcp-1c-analog/internal/bslhelp"
	"mcp-1c-analog/internal/ditrix"
	"mcp-1c-analog/internal/dump"
	"mcp-1c-analog/internal/formdump"
	"mcp-1c-analog/internal/mcp"
	"mcp-1c-analog/internal/onec"
	"mcp-1c-analog/internal/subsystems"
)

func Register(server *mcp.Server, client *onec.Client, index *dump.Index, help *bslhelp.Help) {
	RegisterWithOptions(server, client, index, help, RegisterOptions{})
}

type RegisterOptions struct {
	DumpDir       string
	DitrixClient  *ditrix.Client
	DitrixProject string
}

func RegisterWithOptions(server *mcp.Server, client *onec.Client, index *dump.Index, help *bslhelp.Help, options RegisterOptions) {
	server.AddTool(tool("get_metadata_tree", "Read the 1C metadata tree.", objectSchema(), func(ctx context.Context, args map[string]any) (any, error) {
		var result any
		return result, client.Get(ctx, "/metadata", &result)
	}))
	server.AddTool(tool("get_object_structure", "Read attributes, tabular parts, dimensions and resources of a metadata object.", objectSchema(
		prop("type", "string", "Metadata type, for example Catalog, Document, AccumulationRegister."),
		prop("name", "string", "Metadata object name."),
	), func(ctx context.Context, args map[string]any) (any, error) {
		t, name := requiredString(args, "type"), requiredString(args, "name")
		if t == "" || name == "" {
			return nil, fmt.Errorf("type and name are required")
		}
		if options.DitrixClient != nil {
			return callDitrixTool(ctx, options.DitrixClient, "get_metadata_details", map[string]any{
				"projectName": options.DitrixProject,
				"objectFqns":  []string{t + "." + name},
				"full":        true,
			})
		}
		var result any
		return result, client.Get(ctx, "/object/"+t+"/"+name, &result)
	}))
	server.AddTool(tool("get_form_structure", "Read the main form structure of a metadata object.", objectSchema(
		prop("type", "string", "Metadata type."),
		prop("name", "string", "Metadata object name."),
	), func(ctx context.Context, args map[string]any) (any, error) {
		t, name := requiredString(args, "type"), requiredString(args, "name")
		if t == "" || name == "" {
			return nil, fmt.Errorf("type and name are required")
		}
		if options.DumpDir != "" {
			if result, err := formdump.Read(options.DumpDir, t, name); err == nil {
				return result, nil
			}
		}
		var result any
		return result, client.Get(ctx, "/form/"+t+"/"+name, &result)
	}))
	server.AddTool(tool("get_configuration_info", "Read configuration name, vendor, version and platform info.", objectSchema(), func(ctx context.Context, args map[string]any) (any, error) {
		if options.DitrixClient != nil {
			return callDitrixTool(ctx, options.DitrixClient, "get_configuration_properties", map[string]any{
				"projectName": options.DitrixProject,
			})
		}
		var result any
		return result, client.Get(ctx, "/configuration", &result)
	}))
	server.AddTool(tool("get_extensions", "Read installed configuration extensions from the fixed live 1C base.", objectSchema(), func(ctx context.Context, args map[string]any) (any, error) {
		var result any
		return result, client.Get(ctx, "/extensions", &result)
	}))
	server.AddTool(tool("execute_query", "Execute a read-only 1C query. Only SELECT/ВЫБРАТЬ is allowed by the 1C service.", objectSchema(
		prop("query", "string", "1C query text."),
		prop("parameters", "object", "Optional query parameters."),
		prop("limit", "number", "Maximum rows, up to 1000."),
	), func(ctx context.Context, args map[string]any) (any, error) {
		query := strings.TrimSpace(requiredString(args, "query"))
		if !isSelect(query) {
			return nil, fmt.Errorf("only SELECT/ВЫБРАТЬ queries are allowed")
		}
		var result any
		return result, client.Post(ctx, "/query", args, &result)
	}))
	server.AddTool(tool("validate_query", "Validate 1C query syntax without executing it.", objectSchema(prop("query", "string", "1C query text.")), func(ctx context.Context, args map[string]any) (any, error) {
		if requiredString(args, "query") == "" {
			return nil, fmt.Errorf("query is required")
		}
		var result any
		return result, client.Post(ctx, "/validate-query", args, &result)
	}))
	server.AddTool(tool("get_event_log", "Read 1C event log records with optional filters.", objectSchema(
		prop("start_date", "string", "Optional XML date string."),
		prop("end_date", "string", "Optional XML date string."),
		prop("level", "string", "Ошибка, Предупреждение, Информация or Примечание."),
		prop("user", "string", "Optional infobase user name."),
		prop("limit", "number", "Maximum rows, up to 500."),
	), func(ctx context.Context, args map[string]any) (any, error) {
		var result any
		return result, client.Post(ctx, "/eventlog", args, &result)
	}))
	server.AddTool(tool("search_code", "Search BSL and XML modules in a DumpConfigToFiles directory.", objectSchema(
		prop("query", "string", "Search query."),
		prop("limit", "number", "Maximum results."),
		prop("mode", "string", "Search mode: smart, exact or regex."),
		prop("object_type", "string", "Optional metadata object type filter."),
		prop("module", "string", "Optional module name filter, for example ObjectModule."),
		prop("category", "string", "Optional category filter: module, form, template or metadata."),
	), func(ctx context.Context, args map[string]any) (any, error) {
		if index == nil {
			return nil, fmt.Errorf("search_code is disabled: start with --dump")
		}
		limit := intArg(args, "limit", 10)
		results, err := index.SearchAdvanced(dump.SearchOptions{
			Query: requiredString(args, "query"), Limit: limit, Mode: requiredString(args, "mode"),
			ObjectType: requiredString(args, "object_type"), Module: requiredString(args, "module"),
			Category: requiredString(args, "category"),
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{"documents": index.Count(), "results": results}, nil
	}))
	server.AddTool(tool("analyze_subsystems", "Analyze subsystem coverage, membership and intersections from the fixed live base or configured dump.", objectSchema(
		prop("action", "string", "orphans, containing or intersections."),
		prop("object", "string", "Object full or short name for action=containing."),
		prop("object_type", "string", "Optional type filter for an ambiguous short object name."),
		prop("cross_branch_only", "boolean", "For intersections, keep only objects shared by different root branches."),
	), func(ctx context.Context, args map[string]any) (any, error) {
		var forest subsystems.Forest
		var err error
		if options.DumpDir != "" {
			forest, err = subsystems.ReadDump(options.DumpDir)
		} else {
			err = client.Get(ctx, "/subsystems", &forest)
		}
		if err != nil {
			return nil, fmt.Errorf("read subsystem topology: %w", err)
		}
		crossBranch, _ := args["cross_branch_only"].(bool)
		return subsystems.Analyze(forest, requiredString(args, "action"), requiredString(args, "object"), requiredString(args, "object_type"), crossBranch)
	}))
	server.AddTool(tool("bsl_syntax_help", "Search built-in BSL syntax help bundled with the server.", objectSchema(
		prop("query", "string", "Function, method or keyword."),
		prop("limit", "number", "Maximum results."),
	), func(ctx context.Context, args map[string]any) (any, error) {
		return help.Search(requiredString(args, "query"), intArg(args, "limit", 10)), nil
	}))
}

// callDitrixTool preserves the complete upstream MCP result. In particular,
// YAML resources and structured error responses must not be flattened into a
// JSON string by the compatibility tools above.
func callDitrixTool(ctx context.Context, client *ditrix.Client, name string, args map[string]any) (any, error) {
	result, err := client.CallTool(ctx, name, args)
	if err != nil {
		return nil, err
	}
	return mcp.RawToolResult(result), nil
}

func tool(name, description string, schema map[string]any, handler mcp.Handler) mcp.Tool {
	return mcp.Tool{Name: name, Description: description, InputSchema: schema, Handler: handler}
}

func objectSchema(props ...map[string]any) map[string]any {
	properties := map[string]any{}
	required := []string{}
	for _, p := range props {
		name := p["name"].(string)
		delete(p, "name")
		properties[name] = p
		if name == "type" || name == "name" || name == "query" {
			required = append(required, name)
		}
	}
	return map[string]any{"type": "object", "properties": properties, "required": required}
}

func prop(name, typ, description string) map[string]any {
	return map[string]any{"name": name, "type": typ, "description": description}
}

func requiredString(args map[string]any, name string) string {
	if value, ok := args[name].(string); ok {
		return value
	}
	return ""
}

func intArg(args map[string]any, name string, fallback int) int {
	switch value := args[name].(type) {
	case float64:
		return int(value)
	case int:
		return value
	default:
		return fallback
	}
}

func isSelect(query string) bool {
	upper := strings.ToUpper(strings.TrimSpace(query))
	return strings.HasPrefix(upper, "SELECT") || strings.HasPrefix(upper, "ВЫБРАТЬ")
}
