package tools

import (
	"context"
	"fmt"
	"strings"

	"mcp-1c-analog/internal/mcp"
	"mcp-1c-analog/internal/metadata"
)

func RegisterMetadata(server *mcp.Server, manager *metadata.Manager) {
	server.AddTool(metadataTool(
		"get_configuration_status",
		"Check the configured 1C platform, locked infobase and optional authentication probe. Never returns credentials.",
		schema(map[string]any{
			"probe_authentication": field("boolean", "Run DumpConfigToFiles to verify authentication; this can take several minutes."),
		}),
		readOnly("1C configuration status"),
		func(ctx context.Context, args map[string]any) (any, error) {
			return manager.Status(ctx, boolArg(args, "probe_authentication", false)), nil
		},
	))

	server.AddTool(metadataTool(
		"list_metadata_objects",
		"Dump the configured infobase and list top-level 1C metadata objects. Optionally filter by metadata type.",
		schema(map[string]any{
			"metadata_type": field("string", "Optional platform metadata type such as Document, Catalog or Report."),
		}),
		readOnly("List 1C metadata"),
		func(ctx context.Context, args map[string]any) (any, error) {
			objects, err := manager.List(ctx)
			if err != nil {
				return nil, err
			}
			filter := requiredString(args, "metadata_type")
			if filter == "" {
				return map[string]any{"count": len(objects), "types": metadata.ObjectTypes(objects), "objects": objects}, nil
			}
			filtered := objects[:0]
			for _, object := range objects {
				if strings.EqualFold(object.Type, filter) {
					filtered = append(filtered, object)
				}
			}
			return map[string]any{"count": len(filtered), "objects": filtered}, nil
		},
	))

	server.AddTool(metadataTool(
		"inspect_metadata_object",
		"Dump the configured infobase and inspect one top-level object, including its forms, templates, modules and XML UUID count.",
		schema(map[string]any{
			"metadata_type": field("string", "Platform metadata type such as Document."),
			"name":          field("string", "1C metadata object name."),
		}, "metadata_type", "name"),
		readOnly("Inspect 1C metadata object"),
		func(ctx context.Context, args map[string]any) (any, error) {
			return manager.Inspect(ctx, requiredString(args, "metadata_type"), requiredString(args, "name"))
		},
	))

	server.AddTool(metadataTool(
		"prepare_clone_metadata",
		"Prepare and validate an independent clone of a top-level 1C metadata object. This writes only to the MCP work directory and returns a plan_id; it never changes the configured infobase.",
		schema(map[string]any{
			"metadata_type": field("string", "Platform metadata type such as Document, Catalog or Report."),
			"source_name":   field("string", "Existing source object name."),
			"target_name":   field("string", "New object name that does not exist."),
		}, "metadata_type", "source_name", "target_name"),
		localWrite("Prepare 1C metadata clone"),
		func(ctx context.Context, args map[string]any) (any, error) {
			return manager.Prepare(ctx,
				requiredString(args, "metadata_type"),
				requiredString(args, "source_name"),
				requiredString(args, "target_name"),
			)
		},
	))

	server.AddTool(metadataTool(
		"apply_prepared_change",
		"CHANGE THE CONFIGURED 1C INFOBASE by applying one previously validated plan_id. Refuses stale or already-used plans, creates a CF backup and attempts automatic rollback on failure.",
		schema(map[string]any{
			"plan_id": field("string", "The exact 32-character plan_id returned by prepare_clone_metadata."),
		}, "plan_id"),
		&mcp.Annotations{Title: "Apply prepared 1C change", ReadOnlyHint: false, DestructiveHint: true, IdempotentHint: false, OpenWorldHint: false},
		func(ctx context.Context, args map[string]any) (any, error) {
			return manager.Apply(ctx, requiredString(args, "plan_id"))
		},
	))

	server.AddTool(metadataTool(
		"verify_metadata_object",
		"Dump the configured infobase and verify that a top-level metadata object exists and can be inspected.",
		schema(map[string]any{
			"metadata_type": field("string", "Platform metadata type."),
			"name":          field("string", "1C metadata object name."),
		}, "metadata_type", "name"),
		readOnly("Verify 1C metadata object"),
		func(ctx context.Context, args map[string]any) (any, error) {
			return manager.Verify(ctx, requiredString(args, "metadata_type"), requiredString(args, "name"))
		},
	))

	server.AddTool(metadataTool(
		"discard_prepared_change",
		"Delete an unapplied prepared plan and its canonical dump from the MCP work directory. Applied audit records cannot be discarded.",
		schema(map[string]any{
			"plan_id": field("string", "The exact 32-character prepared plan ID."),
		}, "plan_id"),
		localWrite("Discard prepared 1C change"),
		func(ctx context.Context, args map[string]any) (any, error) {
			planID := requiredString(args, "plan_id")
			if err := manager.Discard(planID); err != nil {
				return nil, err
			}
			return map[string]any{"plan_id": planID, "state": "discarded"}, nil
		},
	))
}

func metadataTool(name, description string, inputSchema map[string]any, annotations *mcp.Annotations, handler mcp.Handler) mcp.Tool {
	return mcp.Tool{Name: name, Description: description, InputSchema: inputSchema, Annotations: annotations, Handler: handler}
}

func schema(properties map[string]any, required ...string) map[string]any {
	result := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		result["required"] = required
	}
	return result
}

func field(kind, description string) map[string]any {
	return map[string]any{"type": kind, "description": description}
}

func readOnly(title string) *mcp.Annotations {
	return &mcp.Annotations{Title: title, ReadOnlyHint: true, DestructiveHint: false, IdempotentHint: true, OpenWorldHint: false}
}

func localWrite(title string) *mcp.Annotations {
	return &mcp.Annotations{Title: title, ReadOnlyHint: false, DestructiveHint: false, IdempotentHint: false, OpenWorldHint: false}
}

func boolArg(args map[string]any, name string, fallback bool) bool {
	if value, ok := args[name].(bool); ok {
		return value
	}
	return fallback
}

func requireMetadataManager(manager *metadata.Manager) error {
	if manager == nil {
		return fmt.Errorf("metadata management is disabled: start with --platform and --infobase")
	}
	return nil
}
