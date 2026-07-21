package dump

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSearch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Module.bsl"), []byte("Процедура ПриОткрытии()\nСообщить(\"Привет\");\nКонецПроцедуры"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx, err := Open(dir, "", true)
	if err != nil {
		t.Fatal(err)
	}
	results := idx.Search("Сообщить", 10)
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].Path != "Module.bsl" {
		t.Fatalf("path = %q", results[0].Path)
	}
}

func TestAdvancedSearchModesFiltersAndCache(t *testing.T) {
	root := t.TempDir()
	moduleDir := filepath.Join(root, "Catalogs", "Partners", "Ext")
	if err := os.MkdirAll(moduleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(moduleDir, "ObjectModule.bsl"), []byte("Procedure ExportPartners()\n  Message(\"partners\");\nEndProcedure"), 0o600); err != nil {
		t.Fatal(err)
	}
	cache := t.TempDir()
	index, err := Open(root, cache, true)
	if err != nil {
		t.Fatal(err)
	}
	exact, err := index.SearchAdvanced(SearchOptions{Query: "Message(\"partners\")", Mode: "exact", ObjectType: "Catalog", Module: "ObjectModule", Category: "module"})
	if err != nil || len(exact) != 1 || exact[0].Line != 2 {
		t.Fatalf("unexpected exact result: %#v, %v", exact, err)
	}
	regex, err := index.SearchAdvanced(SearchOptions{Query: `Export[A-Z]+`, Mode: "regex"})
	if err != nil || len(regex) != 1 {
		t.Fatalf("unexpected regex result: %#v, %v", regex, err)
	}
	if _, err := Open(root, cache, false); err != nil {
		t.Fatalf("open persistent cache: %v", err)
	}
}
