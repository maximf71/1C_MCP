package profile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreRoundTripHasNoCredentials(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	value := Profile{ID: "demo_base", BaseKind: "file", Infobase: t.TempDir(), DBPasswordEnv: "DEMO_DB_PASSWORD"}
	if err := store.Save(value); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load("demo_base")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DBPasswordEnv != "DEMO_DB_PASSWORD" || loaded.MaxResponseSize != 128<<20 {
		t.Fatalf("unexpected profile: %#v", loaded)
	}
	data, err := os.ReadFile(filepath.Join(store.Root, "demo_base.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "secret") {
		t.Fatal("profile contains a credential value")
	}
}

func TestCodexConfigManagedReplacement(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(config, []byte("model = \"gpt\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	value := Profile{ID: "demo_base", BaseURL: "http://localhost/hs/mcp-1c", EDTBridge: filepath.Join(dir, "bridge.json")}
	if _, err := UpdateCodexConfig(config, filepath.Join(dir, "mcp.exe"), value); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateCodexConfig(config, filepath.Join(dir, "mcp-new.exe"), value); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(config)
	text := string(data)
	if strings.Count(text, "BEGIN mcp-1c-analog profile demo_base") != 1 {
		t.Fatalf("managed block duplicated: %s", text)
	}
	if !strings.Contains(text, "demo_base_db") || !strings.Contains(text, "demo_base_edt") || !strings.Contains(text, "mcp-new.exe") {
		t.Fatalf("missing generated server: %s", text)
	}
}

func TestCodexConfigRefusesUnmanagedCollision(t *testing.T) {
	config := filepath.Join(t.TempDir(), "config.toml")
	_ = os.WriteFile(config, []byte("[mcp_servers.demo_base_db]\ncommand=\"other\"\n"), 0o600)
	_, err := UpdateCodexConfig(config, "mcp.exe", Profile{ID: "demo_base", BaseURL: "http://localhost"})
	if err == nil {
		t.Fatal("expected unmanaged collision error")
	}
}
