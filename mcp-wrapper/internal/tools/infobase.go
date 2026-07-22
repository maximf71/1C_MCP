package tools

import (
	"context"
	"errors"
	"strings"

	"mcp-1c-analog/internal/edt"
	"mcp-1c-analog/internal/mcp"
)

func RegisterInfobaseManagement(server *mcp.Server, client *edt.Client) {
	server.AddTool(mcp.Tool{
		Name:        "manage_infobase",
		Description: "List, bind or unbind infobases for the one project fixed in the EDT bridge. Mutations are previews until confirm=true; credentials are never accepted.",
		InputSchema: schema(map[string]any{
			"operation":            enumField("Infobase operation.", "list", "bind", "unbind", "help"),
			"infobase_name":        field("string", "Registered EDT infobase name."),
			"register":             field("boolean", "Register a missing infobase while binding."),
			"base_kind":            enumField("Connection kind for a new registration.", "file", "server"),
			"file_path":            field("string", "Existing directory of a file infobase."),
			"server":               field("string", "1C server for a server infobase."),
			"reference":            field("string", "Server infobase reference."),
			"version":              field("string", "Optional platform version."),
			"already_synchronized": field("boolean", "Mark the project and infobase as already synchronized."),
			"set_default":          field("boolean", "Make this infobase the default binding."),
			"unregister":           field("boolean", "Remove the EDT registration after unbinding, if unused elsewhere."),
			"confirm":              field("boolean", "Required to execute bind or unbind."),
		}, "operation"),
		Annotations: localWrite("Manage fixed-project infobase bindings"),
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			switch strings.TrimSpace(requiredString(args, "operation")) {
			case "help":
				return map[string]any{"operations": []string{"list", "bind", "unbind"}, "credentials_supported": false, "mutations_require_confirm": true}, nil
			case "list":
				return client.ListInfobases(ctx)
			case "bind", "unbind":
				operation := strings.TrimSpace(requiredString(args, "operation"))
				name := strings.TrimSpace(requiredString(args, "infobase_name"))
				if name == "" {
					return nil, errors.New("infobase_name is required")
				}
				payload := infobasePayload(args)
				payload["infobase_name"] = name
				if !boolValue(args["confirm"]) {
					return map[string]any{"dry_run": true, "operation": operation, "arguments": payload}, nil
				}
				payload["confirm"] = true
				if operation == "bind" {
					return client.BindInfobase(ctx, payload)
				}
				return client.UnbindInfobase(ctx, payload)
			default:
				return nil, errors.New("operation must be list, bind, unbind or help")
			}
		},
	})
}

func infobasePayload(args map[string]any) map[string]any {
	result := map[string]any{}
	for _, name := range []string{"register", "base_kind", "file_path", "server", "reference", "version", "already_synchronized", "set_default", "unregister"} {
		if value, exists := args[name]; exists {
			result[name] = value
		}
	}
	return result
}
