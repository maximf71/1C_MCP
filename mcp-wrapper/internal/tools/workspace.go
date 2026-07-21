package tools

import (
	"context"
	"errors"

	"mcp-1c-analog/internal/mcp"
	"mcp-1c-analog/internal/workspace"
)

func RegisterWorkspace(server *mcp.Server, workDir string) {
	memory := workspace.Memory{Root: workDir}
	addIfMissing(server, readOnlyTool("list_templates", "List built-in local 1C templates bundled with the server.", objectSchema(), func(ctx context.Context, args map[string]any) (any, error) {
		return workspace.Templates(), nil
	}))
	addIfMissing(server, readOnlyTool("apply_template", "Render a built-in template with supplied variables. This returns text and never changes project files.", objectSchema(
		prop("name", "string", "Template name."), prop("variables", "object", "Values substituted into {{name}} placeholders."),
	), func(ctx context.Context, args map[string]any) (any, error) {
		name := requiredString(args, "name")
		if name == "" {
			return nil, errors.New("name is required")
		}
		variables, _ := args["variables"].(map[string]any)
		return workspace.RenderTemplate(name, variables)
	}))
	addIfMissing(server, readOnlyTool("memory_list", "List profile-scoped local notes. The memory is stored only in the fixed work directory.", objectSchema(), func(ctx context.Context, args map[string]any) (any, error) {
		return memory.List()
	}))
	addIfMissing(server, metadataTool("memory_put", "Create or replace one profile-scoped local note. It cannot access paths outside the fixed work directory.", schema(map[string]any{
		"key": field("string", "Stable note key."), "value": field("string", "Note value, maximum 64 KiB."),
	}, "key", "value"), localWrite("Write profile memory"), func(ctx context.Context, args map[string]any) (any, error) {
		return memory.Put(requiredString(args, "key"), requiredString(args, "value"))
	}))
}
