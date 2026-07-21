package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mcp-1c-analog/internal/bslhelp"
	"mcp-1c-analog/internal/ditrix"
	"mcp-1c-analog/internal/mcp"
	"mcp-1c-analog/internal/onec"
)

func TestIsSelect(t *testing.T) {
	tests := []struct {
		query string
		want  bool
	}{
		{"SELECT 1", true},
		{"  ВЫБРАТЬ 1", true},
		{"ОБНОВИТЬ Справочник.Номенклатура", false},
		{"DELETE FROM Catalog", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isSelect(tt.query); got != tt.want {
			t.Fatalf("isSelect(%q) = %v, want %v", tt.query, got, tt.want)
		}
	}
}

func TestEDTCompatibilityToolsUseDitrixInsteadOf1CHTTP(t *testing.T) {
	type toolCall struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	calls := make(chan toolCall, 2)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     any             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if request.Method != "tools/call" {
			http.Error(w, "unexpected method", http.StatusBadRequest)
			return
		}
		var params toolCall
		if err := json.Unmarshal(request.Params, &params); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		calls <- params
		result := map[string]any{
			"content":           []any{map[string]any{"type": "text", "text": "native:" + params.Name}},
			"structuredContent": map[string]any{"backend": "edt"},
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": request.ID, "result": result,
		})
	}))
	defer remote.Close()

	proxy, err := ditrix.New(remote.URL + "/mcp")
	if err != nil {
		t.Fatal(err)
	}
	server := mcp.NewServer("test", "1")
	// The deliberately unreachable HTTP client proves these two handlers do not
	// fall back to the published 1C service when the EDT backend is configured.
	legacy := onec.NewClient("http://127.0.0.1:1/hs/mcp-1c", "", "")
	RegisterWithOptions(server, legacy, nil, bslhelp.Default(), RegisterOptions{
		DitrixClient: proxy, DitrixProject: "Удалить 2",
	})

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_configuration_info","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_object_structure","arguments":{"type":"DataProcessor","name":"ExportPartners"}}}`,
	}, "\n") + "\n"
	var output bytes.Buffer
	if err := server.ServeStdio(context.Background(), strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "native:get_configuration_properties") ||
		!strings.Contains(output.String(), "native:get_metadata_details") ||
		!strings.Contains(output.String(), `"backend":"edt"`) {
		t.Fatalf("EDT results were not preserved: %s", output.String())
	}

	first, second := <-calls, <-calls
	if first.Name != "get_configuration_properties" || first.Arguments["projectName"] != "Удалить 2" {
		t.Fatalf("configuration adapter call = %#v", first)
	}
	if second.Name != "get_metadata_details" || second.Arguments["projectName"] != "Удалить 2" || second.Arguments["full"] != true {
		t.Fatalf("object adapter call = %#v", second)
	}
	fqns, ok := second.Arguments["objectFqns"].([]any)
	if !ok || len(fqns) != 1 || fmt.Sprint(fqns[0]) != "DataProcessor.ExportPartners" {
		t.Fatalf("object adapter FQNs = %#v", second.Arguments["objectFqns"])
	}
}
