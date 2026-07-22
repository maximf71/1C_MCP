package tools

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestParseReportFiltersSortsAndLimits(t *testing.T) {
	root := t.TempDir()
	report := map[string]any{"fileinfos": []any{
		map[string]any{"path": filepath.Join(root, "Catalogs", "A", "Module.bsl"), "diagnostics": []any{
			map[string]any{"range": map[string]any{"start": map[string]any{"line": 8, "character": 1}, "end": map[string]any{"line": 8, "character": 2}}, "severity": "Warning", "code": "W1", "source": "bsl-language-server", "message": "warning"},
			map[string]any{"range": map[string]any{"start": map[string]any{"line": 2, "character": 1}, "end": map[string]any{"line": 2, "character": 2}}, "severity": "Error", "code": "E1", "source": "bsl-language-server", "message": "error"},
			map[string]any{"range": map[string]any{"start": map[string]any{"line": 1, "character": 1}, "end": map[string]any{"line": 1, "character": 2}}, "severity": "Hint", "code": "H1", "source": "bsl-language-server", "message": "hint"},
		}},
	}}
	data, _ := json.Marshal(report)
	result, err := parseReport(data, root, "warning", 1)
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalDiagnostics != 2 || result.Returned != 1 || !result.Truncated {
		t.Fatalf("unexpected totals: %#v", result)
	}
	if result.Diagnostics[0].Severity != "error" || result.Diagnostics[0].Path != "Catalogs/A/Module.bsl" {
		t.Fatalf("unexpected first diagnostic: %#v", result.Diagnostics[0])
	}
	if result.Summary["hint"] != 1 || result.Summary["warning"] != 1 || result.Summary["error"] != 1 {
		t.Fatalf("unexpected summary: %#v", result.Summary)
	}
}

func TestNormalizeModulesRejectsEscapes(t *testing.T) {
	if _, err := normalizeModules([]string{"../secret.bsl"}); err == nil {
		t.Fatal("path escape was accepted")
	}
	modules, err := normalizeModules([]string{"CommonModules/Test/Module.bsl", "commonmodules/test/module.bsl"})
	if err != nil || len(modules) != 1 {
		t.Fatalf("modules=%#v err=%v", modules, err)
	}
}

func TestRelativeReportPathAcceptsFileURI(t *testing.T) {
	root := `C:\work\source`
	actual := relativeReportPath(root, `./file:/C:/work/source/CommonModules/Test/Module.bsl`)
	if actual != "CommonModules/Test/Module.bsl" {
		t.Fatalf("relativeReportPath=%q", actual)
	}
}

func TestNormalizeSelectedModulePathsRestoresRequestedPath(t *testing.T) {
	diagnostics := []Diagnostic{{Path: "Module.bsl"}}
	modules := []string{"CommonModules/Test/Module.bsl"}
	normalizeSelectedModulePaths(diagnostics, modules)
	if diagnostics[0].Path != modules[0] {
		t.Fatalf("path=%q", diagnostics[0].Path)
	}
}
