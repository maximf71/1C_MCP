package analysis

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAnalyzeDumpBuildsCallGraphAndXMLDiagnostics(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Module.bsl"), []byte("Процедура Start()\nDoWork();\nКонецПроцедуры\nФункция DoWork()\nВозврат 1;\nКонецФункции"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "broken.xml"), []byte("<broken>"), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := AnalyzeDump(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Symbols) != 2 || len(report.Calls) == 0 || len(report.Diagnostics) == 0 {
		t.Fatalf("unexpected report: %#v", report)
	}
	if graph := ArchitectureMermaid(report); graph == "" {
		t.Fatal("empty architecture graph")
	}
}

func TestAnalyzeQuery(t *testing.T) {
	result := AnalyzeQuery("ВЫБРАТЬ * ИЗ Справочник.Контрагенты")
	if !result.Valid || len(result.Suggestions) < 2 {
		t.Fatalf("unexpected query analysis: %#v", result)
	}
}
