package subsystems

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadAndAnalyzeDump(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "Catalogs", "Partners.xml"), `<MetaDataObject/>`)
	mustWrite(t, filepath.Join(root, "Catalogs", "Orphan.xml"), `<MetaDataObject/>`)
	mustWrite(t, filepath.Join(root, "Subsystems", "Sales.xml"), `
<MetaDataObject xmlns:xr="urn:x"><Subsystem><Properties><Name>Sales</Name><Content>
<xr:Item>Catalog.Partners</xr:Item></Content></Properties><ChildObjects><Subsystem>Retail</Subsystem></ChildObjects></Subsystem></MetaDataObject>`)
	mustWrite(t, filepath.Join(root, "Subsystems", "Sales", "Subsystems", "Retail.xml"), `
<MetaDataObject xmlns:xr="urn:x"><Subsystem><Properties><Name>Retail</Name><Content>
<xr:Item>Catalog.Partners</xr:Item></Content></Properties><ChildObjects/></Subsystem></MetaDataObject>`)
	forest, err := ReadDump(root)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Analyze(forest, "orphans", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if result["count"] != 1 {
		t.Fatalf("unexpected orphans: %#v", result)
	}
	intersections, _ := Analyze(forest, "intersections", "", "", false)
	if intersections["count"] != 1 {
		t.Fatalf("unexpected intersections: %#v", intersections)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
