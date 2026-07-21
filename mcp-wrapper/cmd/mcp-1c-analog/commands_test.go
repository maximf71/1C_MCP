package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mcp-1c-analog/internal/profile"
)

func TestSetupCreatesSecretFreeProfileAndChecksHTTP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"version":"test"}`))
	}))
	defer server.Close()
	root := t.TempDir()
	profiles := filepath.Join(root, "profiles")
	infobase := filepath.Join(root, "base")
	if err := os.MkdirAll(infobase, 0o755); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err := runSetup([]string{
		"--id", "demo_base", "--name", "Demo", "--infobase", infobase,
		"--base-url", server.URL, "--profiles-dir", profiles,
		"--skip-extension", "--skip-codex",
	}, strings.NewReader(""), &output)
	if err != nil {
		t.Fatal(err)
	}
	store, _ := profile.NewStore(profiles)
	value, err := store.Load("demo_base")
	if err != nil {
		t.Fatal(err)
	}
	if value.ID != "demo_base" || !strings.Contains(output.String(), `"live_http": true`) {
		t.Fatalf("unexpected setup result: %#v\n%s", value, output.String())
	}
}

func TestAnalyzeProducesSARIF(t *testing.T) {
	root := t.TempDir()
	dumpDir := filepath.Join(root, "dump")
	if err := os.MkdirAll(dumpDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dumpDir, "broken.xml"), []byte("<broken>"), 0o600); err != nil {
		t.Fatal(err)
	}
	profiles := filepath.Join(root, "profiles")
	store, _ := profile.NewStore(profiles)
	if err := store.Save(profile.Profile{ID: "demo_base", DumpDir: dumpDir}); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := runAnalyze([]string{"--profile", "demo_base", "--profiles-dir", profiles, "--format", "sarif"}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"version": "2.1.0"`) || !strings.Contains(output.String(), "xml-well-formed") {
		t.Fatalf("unexpected SARIF: %s", output.String())
	}
}
