package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"mcp-1c-analog/internal/ditrix"
	"mcp-1c-analog/internal/mcp"
)

func TestMediumCompatibilityToolsAreLockedAndGuardMutations(t *testing.T) {
	projectRoot := t.TempDir()
	help := filepath.Join(projectRoot, "src", "Catalogs", "Partners", "Help", "ru.html")
	if err := os.MkdirAll(filepath.Dir(help), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(help, []byte("<html><style>hidden</style><body><h1>Партнеры</h1><p>Справка &amp; текст</p></body></html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	var mutex sync.Mutex
	var calls []recordedEDTCall
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     any             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		var result any
		switch request.Method {
		case "initialize":
			result = map[string]any{"serverInfo": map[string]any{"name": "fake-edt", "version": "test"}}
		case "tools/list":
			names := []string{"list_projects", "get_metadata_details", "get_applications", "list_configurations", "debug_status", "set_breakpoint", "list_breakpoints", "wait_for_break", "get_variables", "evaluate_expression", "step", "resume", "debug_launch", "terminate_launch", "create_metadata", "modify_metadata", "delete_metadata", "get_form_screenshot", "get_form_layout_snapshot"}
			var tools []map[string]any
			for _, name := range names {
				tools = append(tools, map[string]any{"name": name, "description": name, "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"projectName": map[string]any{"type": "string"}}}})
			}
			result = map[string]any{"tools": tools}
		case "tools/call":
			var params recordedEDTCall
			if err := json.Unmarshal(request.Params, &params); err != nil {
				t.Fatal(err)
			}
			mutex.Lock()
			calls = append(calls, params)
			mutex.Unlock()
			text := "ok:" + params.Name
			if params.Name == "list_projects" {
				text = "| Name | State | Path | A | B |\n| --- | --- | --- | --- | --- |\n| FixedProject | ready | " + projectRoot + " | Yes | Yes |"
			}
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}}
		default:
			t.Fatalf("unexpected method %s", request.Method)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}))
	defer remote.Close()
	client, err := ditrix.New(remote.URL + "/mcp")
	if err != nil {
		t.Fatal(err)
	}
	server := mcp.NewServer("test", "1")
	if _, err := RegisterDitrixEDTWithOptions(context.Background(), server, client, "FixedProject", DitrixRegistrationOptions{WorkDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	requests := []map[string]any{
		{"name": "get_object_help", "arguments": map[string]any{"object_fqn": "Catalog.Partners"}},
		{"name": "launch_debugger", "arguments": map[string]any{"operation": "status"}},
		{"name": "launch_debugger", "arguments": map[string]any{"operation": "evaluate", "arguments": map[string]any{"expression": "1+1"}}},
		{"name": "edit_metadata", "arguments": map[string]any{"operation": "setDcs", "arguments": map[string]any{"objectFqn": "Report.Sales"}}},
		{"name": "edit_metadata", "arguments": map[string]any{"operation": "addField", "arguments": map[string]any{"formFqn": "Catalog.Partners.Form.ItemForm", "name": "Comment"}}},
		{"name": "diagnostics", "arguments": map[string]any{"operation": "status"}},
		{"name": "vanessa", "arguments": map[string]any{"operation": "status"}},
		{"name": "update_configuration", "arguments": map[string]any{"operation": "help"}},
	}
	var input strings.Builder
	for index, request := range requests {
		payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": index + 1, "method": "tools/call", "params": request})
		input.Write(payload)
		input.WriteByte('\n')
	}
	var output bytes.Buffer
	if err := server.ServeStdio(context.Background(), strings.NewReader(input.String()), &output); err != nil {
		t.Fatal(err)
	}
	responses := output.String()
	if !strings.Contains(responses, "Партнеры") || !strings.Contains(responses, "Справка") || !strings.Contains(responses, "evaluate requires confirm=true") || !strings.Contains(responses, `\"dry_run\": true`) || !strings.Contains(responses, "guarded-source-tree") || !strings.Contains(responses, "performance_backend") {
		t.Fatalf("unexpected responses: %s", responses)
	}
	mutex.Lock()
	defer mutex.Unlock()
	for _, call := range calls {
		if call.Name == "evaluate_expression" || call.Name == "modify_metadata" {
			t.Fatalf("guarded mutation reached backend: %#v", call)
		}
		if value, exists := call.Arguments["projectName"]; exists && value != "FixedProject" {
			t.Fatalf("foreign project reached backend: %#v", call)
		}
	}
}

func TestMetadataFacadeCoversArchitectOperationTree(t *testing.T) {
	operations := metadataOperations()
	if len(operations) < 140 {
		t.Fatalf("only %d metadata operations are registered", len(operations))
	}
	for _, required := range []string{"createObject", "addRegisterField", "addDynamicListTable", "drawTemplate", "setRoleRestriction", "addHttpServiceMethod", "addSettingsVariant", "syncExport"} {
		if !isMetadataSemanticOperation(required) {
			t.Fatalf("operation %s is missing", required)
		}
	}
}

func TestHelpPathRejectsTraversal(t *testing.T) {
	if _, err := helpRelativePath("Catalog...\\secret", "ru"); err == nil {
		t.Fatal("unsafe metadata FQN was accepted")
	}
	if text := htmlToText("<style>bad</style><p>Good &amp; safe</p>"); text != "Good & safe" {
		t.Fatalf("htmlToText=%q", text)
	}
}
