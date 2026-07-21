package ditrix

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewRejectsNonLoopbackAndUnsafeURLParts(t *testing.T) {
	for _, endpoint := range []string{
		"https://127.0.0.1:8765/mcp",
		"http://example.com/mcp",
		"http://user:secret@127.0.0.1:8765/mcp",
		"http://127.0.0.1:8765/other",
		"http://127.0.0.1:8765/mcp?token=secret",
	} {
		if _, err := New(endpoint); err == nil {
			t.Fatalf("New(%q) unexpectedly succeeded", endpoint)
		}
	}
	if _, err := New("http://127.0.0.1:8765/mcp"); err != nil {
		t.Fatalf("loopback URL rejected: %v", err)
	}
}

func TestClientHandlesSSEResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		var result any
		switch request.Method {
		case "initialize":
			result = map[string]any{"serverInfo": map[string]any{"name": "edt-mcp-server", "version": "2.6.1"}}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{
				"name": "get_symbol_info", "description": "symbol", "inputSchema": map[string]any{"type": "object"},
			}}}
		case "tools/call":
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "ok"}}}
		default:
			t.Fatalf("unexpected method %s", request.Method)
		}
		payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
		fmt.Fprintf(w, "event: message\nid: 1\ndata: %s\n\n", payload)
	}))
	defer server.Close()

	client, err := New(server.URL + "/mcp")
	if err != nil {
		t.Fatal(err)
	}
	info, err := client.Initialize(context.Background())
	if err != nil || info.Version != "2.6.1" {
		t.Fatalf("Initialize() = %+v, %v", info, err)
	}
	tools, err := client.ListTools(context.Background())
	if err != nil || len(tools) != 1 || tools[0].Name != "get_symbol_info" {
		t.Fatalf("ListTools() = %+v, %v", tools, err)
	}
	result, err := client.CallTool(context.Background(), "get_symbol_info", map[string]any{})
	if err != nil || result["content"] == nil {
		t.Fatalf("CallTool() = %+v, %v", result, err)
	}
}

func TestCallToolRetriesTransientProjectBuild(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID any `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		attempt := attempts.Add(1)
		result := map[string]any{"content": []any{map[string]any{"type": "text", "text": "ready"}}}
		if attempt < 3 {
			result = map[string]any{
				"isError": true,
				"content": []any{map[string]any{"type": "text", "text": "Project is building: computing. Please wait and retry."}},
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}))
	defer server.Close()

	client, err := New(server.URL + "/mcp")
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.callToolWithRetry(context.Background(), "create_metadata", map[string]any{}, time.Second, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("transient build error was not retried: %v", err)
	}
	if attempts.Load() != 3 || textContent(result) != "ready" {
		t.Fatalf("attempts=%d result=%#v", attempts.Load(), result)
	}
}

func TestCallToolDoesNotRetryPermanentError(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID any `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		attempts.Add(1)
		result := map[string]any{
			"isError": true,
			"content": []any{map[string]any{"type": "text", "text": "Metadata object not found"}},
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}))
	defer server.Close()

	client, err := New(server.URL + "/mcp")
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.callToolWithRetry(context.Background(), "get_metadata_details", map[string]any{}, time.Second, 10*time.Millisecond)
	if err == nil || attempts.Load() != 1 {
		t.Fatalf("permanent error must not be retried: attempts=%d err=%v", attempts.Load(), err)
	}
}
