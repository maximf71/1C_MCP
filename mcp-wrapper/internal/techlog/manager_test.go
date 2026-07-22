package techlog

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnableAnalyzeDisable(t *testing.T) {
	root := t.TempDir()
	manager := &Manager{ConfigPath: filepath.Join(root, "conf", "logcfg.xml"), LogRoot: filepath.Join(root, "logs"), WorkDir: filepath.Join(root, "work")}
	if _, err := manager.Enable("performance", 10, 12); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(manager.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	var parsed any
	if err := xml.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("invalid XML: %v", err)
	}
	for _, marker := range []string{"tech-log", "DBMSSQL", "duration", manager.LogRoot} {
		if !strings.Contains(text, marker) {
			t.Fatalf("config has no %q: %s", marker, text)
		}
	}
	log := "10:00.000001-25000,DBMSSQL,1,process=rphost,Context='CommonModule.Test:42'\n10:00.000002-1000,CALL,1,process=rphost\n"
	if err := os.WriteFile(filepath.Join(manager.LogRoot, "sample.log"), []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := manager.Analyze(10, "CommonModule.Test", 10)
	if err != nil {
		t.Fatal(err)
	}
	if result.Events != 1 || result.Returned != 1 || result.Longest[0].DurationMS != 25 {
		t.Fatalf("unexpected analysis: %#v", result)
	}
	if _, err := manager.Disable(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(manager.ConfigPath); !os.IsNotExist(err) {
		t.Fatalf("config was not removed: %v", err)
	}
}

func TestEnableRestoresExistingConfig(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "logcfg.xml")
	if err := os.WriteFile(config, []byte("previous"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{ConfigPath: config, LogRoot: filepath.Join(root, "logs"), WorkDir: filepath.Join(root, "work")}
	if _, err := manager.Enable("exceptions", 0, 24); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Disable(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(config)
	if string(data) != "previous" {
		t.Fatalf("previous config not restored: %q", data)
	}
}
