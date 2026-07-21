package tools

import (
	"context"

	"mcp-1c-analog/internal/edt"
	"mcp-1c-analog/internal/mcp"
)

func RegisterEdtMetadata(server *mcp.Server, client *edt.Client) {
	server.AddTool(metadataTool(
		"get_configuration_status",
		"Check the fixed EDT project and local authenticated bridge. This never changes the EDT project or the infobase.",
		schema(map[string]any{}),
		readOnly("EDT project status"),
		func(ctx context.Context, args map[string]any) (any, error) { return client.Health(ctx) },
	))

	server.AddTool(metadataTool(
		"list_metadata_objects",
		"List top-level metadata directly from the fixed EDT project without dumping the infobase.",
		schema(map[string]any{
			"metadata_type": field("string", "Optional EDT metadata type such as Document, Catalog or Report."),
		}),
		readOnly("List EDT metadata"),
		func(ctx context.Context, args map[string]any) (any, error) {
			return client.List(ctx, requiredString(args, "metadata_type"))
		},
	))

	server.AddTool(metadataTool(
		"inspect_metadata_object",
		"Inspect one top-level object directly in the fixed EDT model, including contained model element counts.",
		schema(map[string]any{
			"metadata_type": field("string", "EDT metadata type such as Document."),
			"name":          field("string", "1C metadata object name."),
		}, "metadata_type", "name"),
		readOnly("Inspect EDT metadata object"),
		func(ctx context.Context, args map[string]any) (any, error) {
			return client.Inspect(ctx, requiredString(args, "metadata_type"), requiredString(args, "name"))
		},
	))

	server.AddTool(metadataTool(
		"prepare_clone_metadata",
		"Prepare a clone plan against the current EDT project. It validates source, target and project fingerprint but changes nothing.",
		schema(map[string]any{
			"metadata_type": field("string", "EDT metadata type such as Document, Catalog or Report."),
			"source_name":   field("string", "Existing source object name."),
			"target_name":   field("string", "New object name that does not exist."),
		}, "metadata_type", "source_name", "target_name"),
		readOnly("Prepare EDT metadata clone"),
		func(ctx context.Context, args map[string]any) (any, error) {
			return client.Prepare(ctx, requiredString(args, "metadata_type"),
				requiredString(args, "source_name"), requiredString(args, "target_name"))
		},
	))

	server.AddTool(metadataTool(
		"apply_prepared_change",
		"Apply one exact plan_id to the fixed EDT project using EDT native copy and refactoring services. It does not update any infobase.",
		schema(map[string]any{
			"plan_id": field("string", "The exact 32-character plan_id returned by prepare_clone_metadata."),
		}, "plan_id"),
		localWrite("Apply prepared EDT project change"),
		func(ctx context.Context, args map[string]any) (any, error) {
			return client.Apply(ctx, requiredString(args, "plan_id"))
		},
	))

	server.AddTool(metadataTool(
		"verify_metadata_object",
		"Verify an object directly in the synchronized EDT model. This does not inspect or update an infobase.",
		schema(map[string]any{
			"metadata_type": field("string", "EDT metadata type."),
			"name":          field("string", "1C metadata object name."),
		}, "metadata_type", "name"),
		readOnly("Verify EDT metadata object"),
		func(ctx context.Context, args map[string]any) (any, error) {
			return client.Verify(ctx, requiredString(args, "metadata_type"), requiredString(args, "name"))
		},
	))

	server.AddTool(metadataTool(
		"discard_prepared_change",
		"Discard an unapplied in-memory EDT clone plan.",
		schema(map[string]any{
			"plan_id": field("string", "The exact 32-character prepared plan ID."),
		}, "plan_id"),
		localWrite("Discard prepared EDT change"),
		func(ctx context.Context, args map[string]any) (any, error) {
			return client.Discard(ctx, requiredString(args, "plan_id"))
		},
	))

	server.AddTool(metadataTool(
		"list_bsl_modules",
		"List BSL modules in the fixed EDT project. Use contains to narrow the path before reading code.",
		schema(map[string]any{
			"contains": field("string", "Optional case-insensitive substring of the module path."),
			"limit":    field("number", "Maximum returned paths; default 200, maximum 1000."),
		}),
		readOnly("List EDT BSL modules"),
		func(ctx context.Context, args map[string]any) (any, error) {
			return client.ListBslModules(ctx, requiredString(args, "contains"), intArg(args, "limit", 200))
		},
	))

	server.AddTool(metadataTool(
		"read_bsl_module",
		"Read a bounded line range from a BSL module in the fixed EDT project. Paths come from list_bsl_modules.",
		schema(map[string]any{
			"module_path": field("string", "Relative .bsl path under project src."),
			"start_line":  field("number", "Optional first 1-based line; default 1."),
			"end_line":    field("number", "Optional last 1-based line; default start+399, maximum 2000 lines."),
		}, "module_path"),
		readOnly("Read EDT BSL module"),
		func(ctx context.Context, args map[string]any) (any, error) {
			return client.ReadBslModule(ctx, requiredString(args, "module_path"),
				intArg(args, "start_line", 1), intArg(args, "end_line", 0))
		},
	))

	server.AddTool(metadataTool(
		"search_bsl_code",
		"Search BSL source in the fixed EDT project and return matching lines with module paths.",
		schema(map[string]any{
			"query":         field("string", "Case-insensitive source text to find."),
			"path_contains": field("string", "Optional case-insensitive module path filter."),
			"limit":         field("number", "Maximum matches; default 50, maximum 200."),
		}, "query"),
		readOnly("Search EDT BSL source"),
		func(ctx context.Context, args map[string]any) (any, error) {
			return client.SearchBslCode(ctx, requiredString(args, "query"),
				requiredString(args, "path_contains"), intArg(args, "limit", 50))
		},
	))

	server.AddTool(metadataTool(
		"get_bsl_diagnostics",
		"Read actual EDT problem markers for BSL modules, including syntax and semantic diagnostics.",
		schema(map[string]any{
			"module_path": field("string", "Optional exact relative .bsl path; omit for all BSL modules."),
			"limit":       field("number", "Maximum problems; default 100, maximum 200."),
		}),
		readOnly("Read EDT BSL diagnostics"),
		func(ctx context.Context, args map[string]any) (any, error) {
			return client.BslDiagnostics(ctx, requiredString(args, "module_path"), intArg(args, "limit", 100))
		},
	))

	server.AddTool(metadataTool(
		"get_bsl_content_assist",
		"Get real 1C:EDT BSL completion proposals and optional platform documentation at a saved module position.",
		schema(map[string]any{
			"module_path":           field("string", "Relative .bsl path under project src."),
			"line":                  field("number", "Caret line, 1-based."),
			"column":                field("number", "Caret column, 1-based; place it after a dot for members."),
			"contains":              field("string", "Optional case-insensitive proposal filter."),
			"limit":                 field("number", "Maximum proposals; default 50, maximum 200."),
			"include_documentation": field("boolean", "Include EDT platform documentation when available."),
		}, "module_path", "line", "column"),
		readOnly("Get EDT BSL content assist"),
		func(ctx context.Context, args map[string]any) (any, error) {
			return client.BslContentAssist(ctx, requiredString(args, "module_path"),
				intArg(args, "line", 0), intArg(args, "column", 0), requiredString(args, "contains"),
				intArg(args, "limit", 50), boolArg(args, "include_documentation", false))
		},
	))
}
