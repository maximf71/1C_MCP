package edt

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestClientUsesRotatingDescriptorTokenAndLoopback(t *testing.T) {
	const token = "secret-token"
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+token {
			t.Fatalf("unexpected authorization header")
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"ready":true}`))
	}))
	defer server.Close()

	port, err := strconv.Atoi(strings.TrimPrefix(server.URL, "http://127.0.0.1:"))
	if err != nil {
		t.Fatal(err)
	}
	descriptor := filepath.Join(t.TempDir(), "bridge.json")
	data, _ := json.Marshal(bridgeInfo{Version: 1, Host: "127.0.0.1", Port: port, Token: token})
	if err := os.WriteFile(descriptor, data, 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := New(descriptor).Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ready, _ := result["ready"].(bool); !ready {
		t.Fatalf("unexpected response: %#v", result)
	}
}

func TestClientRejectsNonLoopbackDescriptor(t *testing.T) {
	descriptor := filepath.Join(t.TempDir(), "bridge.json")
	data, _ := json.Marshal(bridgeInfo{Version: 1, Host: "192.0.2.1", Port: 17831, Token: "secret"})
	if err := os.WriteFile(descriptor, data, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := New(descriptor).Health(context.Background())
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("expected loopback validation error, got %v", err)
	}
}

func TestClientDoesNotExposeTokenInBridgeErrors(t *testing.T) {
	const token = "must-not-appear"
	descriptor := filepath.Join(t.TempDir(), "bridge.json")
	data, _ := json.Marshal(bridgeInfo{Version: 1, Host: "127.0.0.1", Port: 1, Token: token})
	if err := os.WriteFile(descriptor, data, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := New(descriptor).Health(context.Background())
	if err == nil {
		t.Fatal("expected connection error")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("error exposes token: %v", err)
	}
}

func TestBslClientUsesLockedBridgeEndpoints(t *testing.T) {
	const token = "bsl-token"
	var seenPath string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+token {
			t.Fatalf("unexpected authorization header")
		}
		seenPath = request.URL.Path
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["module_path"] != "Documents/Test/ObjectModule.bsl" {
			t.Fatalf("unexpected payload: %#v", payload)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"proposals":[{"display":"Сообщить"}]}`))
	}))
	defer server.Close()

	port, err := strconv.Atoi(strings.TrimPrefix(server.URL, "http://127.0.0.1:"))
	if err != nil {
		t.Fatal(err)
	}
	descriptor := filepath.Join(t.TempDir(), "bridge.json")
	data, _ := json.Marshal(bridgeInfo{Version: 1, Host: "127.0.0.1", Port: port, Token: token})
	if err := os.WriteFile(descriptor, data, 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := New(descriptor).BslContentAssist(context.Background(),
		"Documents/Test/ObjectModule.bsl", 1, 1, "Сообщ", 10, true)
	if err != nil {
		t.Fatal(err)
	}
	if seenPath != "/bsl/content-assist" {
		t.Fatalf("unexpected endpoint: %s", seenPath)
	}
	if _, ok := result["proposals"]; !ok {
		t.Fatalf("unexpected response: %#v", result)
	}
}

func TestExternalImportClientUsesLockedBridgeEndpoint(t *testing.T) {
	const token = "external-token"
	var seenPath string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		seenPath = request.URL.Path
		if request.Header.Get("Authorization") != "Bearer "+token {
			t.Fatalf("unexpected authorization header")
		}
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["project_name"] != "CodexExt_Test" || payload["source_xml"] != `Test\src\Test.xml` {
			t.Fatalf("unexpected payload: %#v", payload)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"success":true,"database_changed":false}`))
	}))
	defer server.Close()

	port, err := strconv.Atoi(strings.TrimPrefix(server.URL, "http://127.0.0.1:"))
	if err != nil {
		t.Fatal(err)
	}
	descriptor := filepath.Join(t.TempDir(), "bridge.json")
	data, _ := json.Marshal(bridgeInfo{Version: 1, Host: "127.0.0.1", Port: port, Token: token})
	if err := os.WriteFile(descriptor, data, 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := New(descriptor).ImportExternalObjectXML(context.Background(),
		"CodexExt_Test", `Test\src\Test.xml`, "8.3.27")
	if err != nil {
		t.Fatal(err)
	}
	if seenPath != "/external/import-xml" || result["database_changed"] != false {
		t.Fatalf("unexpected result/path: %#v %s", result, seenPath)
	}
}
