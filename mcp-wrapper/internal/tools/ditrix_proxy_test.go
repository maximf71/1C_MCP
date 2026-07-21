package tools

import (
	"os"
	"path/filepath"
	"testing"

	"mcp-1c-analog/internal/ditrix"
)

func TestLockDitrixArgumentsInjectsFixedProject(t *testing.T) {
	tool := ditrix.Tool{
		Name: "read_module_source",
		InputSchema: map[string]any{
			"properties": map[string]any{"projectName": map[string]any{"type": "string"}},
		},
	}
	locked, err := lockDitrixArguments(tool, map[string]any{"moduleFqn": "Document.X.ObjectModule"}, "Удалить 2")
	if err != nil {
		t.Fatal(err)
	}
	if locked["projectName"] != "Удалить 2" {
		t.Fatalf("projectName was not injected: %#v", locked)
	}
	if _, err := lockDitrixArguments(tool, map[string]any{"projectName": "Чужой проект"}, "Удалить 2"); err == nil {
		t.Fatal("foreign project was accepted")
	}
}

func TestLockDitrixArgumentsRejectsFilesystemEscape(t *testing.T) {
	tool := ditrix.Tool{Name: "get_event_log", InputSchema: map[string]any{"properties": map[string]any{}}}
	if _, err := lockDitrixArguments(tool, map[string]any{"logDir": `C:\\Secret`}, "Удалить 2"); err == nil {
		t.Fatal("logDir was accepted")
	}
}

func TestKnownDestructiveToolsRequireWriteApproval(t *testing.T) {
	for _, name := range []string{"delete_metadata", "update_database"} {
		policy, ok := ditrixToolPolicies[name]
		if !ok || policy.readOnly || !policy.destructive {
			t.Fatalf("unsafe policy for %s: %+v, exists=%v", name, policy, ok)
		}
	}
}

func TestLockDitrixArgumentsRejectsExternalWorkflowPaths(t *testing.T) {
	tool := ditrix.Tool{Name: "build_external_objects", InputSchema: map[string]any{"properties": map[string]any{}}}
	for _, argument := range []string{"outputDir", "importPath", "sourceDir"} {
		if _, err := lockDitrixArguments(tool, map[string]any{argument: `C:\Secret`}, "FixedProject"); err == nil {
			t.Fatalf("%s was accepted", argument)
		}
	}
}

func TestManagedExternalProjectBoundary(t *testing.T) {
	if _, err := requireManagedExternalProject("CodexExt_TestObject"); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"BaseProject", "CodexExt_../Base", "CodexExt_", `CodexExt_X\Y`} {
		if _, err := requireManagedExternalProject(value); err == nil {
			t.Fatalf("unsafe project name was accepted: %q", value)
		}
	}
}

func TestManagedExternalSourceBoundary(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "processor.xml")
	if err := os.WriteFile(inside, []byte("<MetaDataObject/>"), 0o600); err != nil {
		t.Fatal(err)
	}
	relative, err := validateManagedSource(root, "processor.xml")
	if err != nil || relative != "processor.xml" {
		t.Fatalf("valid source rejected: relative=%q err=%v", relative, err)
	}
	for _, value := range []string{inside, "../processor.xml", "processor.epf", "missing.xml"} {
		if _, err := validateManagedSource(root, value); err == nil {
			t.Fatalf("unsafe source was accepted: %q", value)
		}
	}
}
