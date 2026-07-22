package configmerge

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCompareDryRunAndMerge(t *testing.T) {
	root := t.TempDir()
	main := filepath.Join(root, "main", "src")
	sources := filepath.Join(root, "sources", "vendor", "src")
	_ = os.MkdirAll(filepath.Join(main, "Catalogs"), 0o700)
	_ = os.MkdirAll(filepath.Join(sources, "Catalogs"), 0o700)
	_ = os.WriteFile(filepath.Join(main, "Catalogs", "A.xml"), []byte("old"), 0o600)
	_ = os.WriteFile(filepath.Join(sources, "Catalogs", "A.xml"), []byte("new"), 0o600)
	_ = os.WriteFile(filepath.Join(sources, "Catalogs", "B.xml"), []byte("added"), 0o600)
	m := &Manager{SourceRoot: filepath.Join(root, "sources"), WorkDir: filepath.Join(root, "work")}
	plan, err := m.Compare(filepath.Join(root, "main"), "vendor", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Differences) != 2 {
		t.Fatalf("differences=%d", len(plan.Differences))
	}
	preview, err := m.Merge(plan.ID, nil, true, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if !preview["dry_run"].(bool) {
		t.Fatal("expected dry run")
	}
	result, err := m.Merge(plan.ID, nil, true, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if !result["changed"].(bool) {
		t.Fatal("expected change")
	}
	data, _ := os.ReadFile(filepath.Join(main, "Catalogs", "A.xml"))
	if string(data) != "new" {
		t.Fatalf("not merged: %q", data)
	}
	if _, err := os.Stat(filepath.Join(main, "Catalogs", "B.xml")); err != nil {
		t.Fatal(err)
	}
}
func TestRejectsEscapingSource(t *testing.T) {
	root := t.TempDir()
	sources := filepath.Join(root, "sources")
	_ = os.MkdirAll(sources, 0o700)
	m := &Manager{SourceRoot: sources, WorkDir: filepath.Join(root, "work")}
	if _, err := m.Compare(root, "..\\other", ""); err == nil {
		t.Fatal("expected escape error")
	}
}

func TestRejectsStalePlan(t *testing.T) {
	root := t.TempDir()
	main := filepath.Join(root, "main")
	other := filepath.Join(root, "sources", "other")
	_ = os.MkdirAll(main, 0o700)
	_ = os.MkdirAll(other, 0o700)
	file := filepath.Join(main, "A.xml")
	_ = os.WriteFile(file, []byte("one"), 0o600)
	_ = os.WriteFile(filepath.Join(other, "A.xml"), []byte("two"), 0o600)
	m := &Manager{SourceRoot: filepath.Join(root, "sources"), WorkDir: filepath.Join(root, "work")}
	plan, err := m.Compare(main, "other", "")
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(file, []byte("changed after plan"), 0o600)
	if _, err := m.Merge(plan.ID, nil, true, false, true); err == nil {
		t.Fatal("stale plan was accepted")
	}
}
