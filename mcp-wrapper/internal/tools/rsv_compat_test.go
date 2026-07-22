package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"mcp-1c-analog/internal/ditrix"
	"mcp-1c-analog/internal/mcp"
)

type recordedEDTCall struct {
	Name      string
	Arguments map[string]any
}

func TestRSVCompatibilityToolsRouteAndProtectWrites(t *testing.T) {
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
			names := []string{"list_projects", "get_metadata_details", "get_module_structure", "read_module_source", "read_method_source", "search_in_code", "find_references", "get_method_call_hierarchy", "go_to_definition", "get_symbol_info", "write_module_source", "get_project_errors"}
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
			if params.Name == "read_module_source" {
				text = "---\ncontentHash: revision-1\ntotalLines: 3\n---\n```bsl\nLine1\nLine2\nLine3\n```"
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
	workDir := t.TempDir()
	if _, err := RegisterDitrixEDTWithOptions(context.Background(), server, client, "FixedProject", DitrixRegistrationOptions{WorkDir: workDir}); err != nil {
		t.Fatal(err)
	}

	requests := []map[string]any{
		{"name": "list_workspace_projects", "arguments": map[string]any{}},
		{"name": "code_search", "arguments": map[string]any{"operation": "textSearch", "query": "Procedure"}},
		{"name": "ai_context", "arguments": map[string]any{"target": "CommonModules.Test.Module.bsl", "target_type": "module", "depth": "minimal"}},
		{"name": "write_module_source", "arguments": map[string]any{"modulePath": "CommonModules/Test/Module.bsl", "mode": "replaceLines", "startLine": 2, "endLine": 2, "source": "Changed", "dryRun": true}},
		{"name": "write_module_source", "arguments": map[string]any{"modulePath": "CommonModules/Test/Module.bsl", "mode": "insertAfter", "anchor": "Line2", "source": "\nInserted", "dryRun": false}},
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
	if strings.Contains(output.String(), `"error":`) || !strings.Contains(output.String(), `\"dry_run\": true`) {
		t.Fatalf("unexpected MCP responses: %s", output.String())
	}
	mutex.Lock()
	defer mutex.Unlock()
	foundWrite := false
	for _, call := range calls {
		if project, exists := call.Arguments["projectName"]; exists && project != "FixedProject" {
			t.Fatalf("foreign project routed to EDT: %#v", call)
		}
		if call.Name == "write_module_source" {
			foundWrite = true
			if call.Arguments["mode"] != "replace" || call.Arguments["expectedHash"] != "revision-1" || call.Arguments["source"] != "Line1\nLine2\nInserted\nLine3" {
				t.Fatalf("unsafe write call: %#v", call.Arguments)
			}
		}
	}
	if !foundWrite {
		t.Fatal("actual write was not routed")
	}
	backups, err := filepath.Glob(filepath.Join(workDir, "module-backups", "FixedProject", "*.bsl"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("backup count=%d err=%v", len(backups), err)
	}
}

func TestModuleEditSafetyHelpers(t *testing.T) {
	updated, err := replaceLineRange("one\ntwo\nthree\n", 2, 2, "TWO")
	if err != nil || updated != "one\nTWO\nthree\n" {
		t.Fatalf("replaceLineRange=%q err=%v", updated, err)
	}
	if _, err := replaceUnique("x x", "x", "y"); err == nil {
		t.Fatal("ambiguous replacement was accepted")
	}
	if ratio := deletionRatio("one\ntwo\nthree\nfour", "one"); ratio != 0.75 {
		t.Fatalf("deletion ratio=%v", ratio)
	}
	target, err := moduleTarget(map[string]any{"objectName": "Catalog.Products", "moduleType": "ManagerModule"})
	if err != nil || target.ModulePath != "Catalogs/Products/ManagerModule.bsl" {
		t.Fatalf("object target=%#v err=%v", target, err)
	}
	commonForm, err := moduleTarget(map[string]any{"objectName": "CommonForm.Settings", "moduleType": "FormModule"})
	if err != nil || commonForm.ModulePath != "CommonForms/Settings/Module.bsl" {
		t.Fatalf("common form target=%#v err=%v", commonForm, err)
	}
}
