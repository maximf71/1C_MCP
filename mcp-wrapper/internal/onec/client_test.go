package onec

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGetDecodesJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "", "")
	var result map[string]bool
	if err := client.Get(context.Background(), "", &result); err != nil {
		t.Fatal(err)
	}
	if !result["ok"] {
		t.Fatal("expected ok=true")
	}
}

func TestConfigurableResponseLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"value":"too long"}`))
	}))
	defer server.Close()
	client := NewClientWithOptions(server.URL, "", "", time.Second, 8)
	var result any
	err := client.Get(context.Background(), "", &result)
	if err == nil || !strings.Contains(err.Error(), "larger than 8 bytes") {
		t.Fatalf("unexpected error: %v", err)
	}
}
