package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"mcp-1c-analog/internal/edt"
	"mcp-1c-analog/internal/mcp"
)

func TestManageInfobasePreviewsAndUsesLockedBridgeEndpoints(t *testing.T) {
	const token = "infobase-test-token"
	var mutex sync.Mutex
	var paths []string
	bridge := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+token {
			t.Fatalf("unexpected authorization")
		}
		mutex.Lock()
		paths = append(paths, request.URL.Path)
		mutex.Unlock()
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if request.URL.Path != "/infobases/list" && payload["confirm"] != true {
			t.Fatalf("mutation reached bridge without confirmation: %#v", payload)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"success":true,"database_changed":false}`))
	}))
	defer bridge.Close()
	port, err := strconv.Atoi(strings.TrimPrefix(bridge.URL, "http://127.0.0.1:"))
	if err != nil {
		t.Fatal(err)
	}
	descriptor := filepath.Join(t.TempDir(), "bridge.json")
	descriptorData, _ := json.Marshal(map[string]any{"version": 1, "host": "127.0.0.1", "port": port, "token": token})
	if err := os.WriteFile(descriptor, descriptorData, 0o600); err != nil {
		t.Fatal(err)
	}
	server := mcp.NewServer("test", "1")
	RegisterInfobaseManagement(server, edt.New(descriptor))
	requests := []map[string]any{
		{"name": "manage_infobase", "arguments": map[string]any{"operation": "list"}},
		{"name": "manage_infobase", "arguments": map[string]any{"operation": "bind", "infobase_name": "Test", "register": true, "base_kind": "file", "file_path": `C:\Bases\Test`}},
		{"name": "manage_infobase", "arguments": map[string]any{"operation": "bind", "infobase_name": "Test", "confirm": true}},
		{"name": "manage_infobase", "arguments": map[string]any{"operation": "unbind", "infobase_name": "Test", "confirm": true}},
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
		t.Fatalf("unexpected responses: %s", output.String())
	}
	mutex.Lock()
	defer mutex.Unlock()
	want := []string{"/infobases/list", "/infobases/bind", "/infobases/unbind"}
	if len(paths) != len(want) {
		t.Fatalf("bridge calls=%#v", paths)
	}
	for index := range want {
		if paths[index] != want[index] {
			t.Fatalf("bridge calls=%#v", paths)
		}
	}
}
