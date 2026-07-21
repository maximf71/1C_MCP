package formdump

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadFormStructure(t *testing.T) {
	root := t.TempDir()
	forms := filepath.Join(root, "Catalogs", "Partners", "Forms")
	if err := os.MkdirAll(filepath.Join(forms, "ObjectForm", "Ext"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(forms, "ObjectForm.xml"), []byte("<MetaDataObject/>"), 0o600); err != nil {
		t.Fatal(err)
	}
	layout := `<Form><Attributes><Attribute name="Object"/></Attributes><Commands><Command name="Print"/></Commands><ChildItems><InputField name="Name"/></ChildItems></Form>`
	if err := os.WriteFile(filepath.Join(forms, "ObjectForm", "Ext", "Form.xml"), []byte(layout), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := Read(root, "Catalog", "Partners")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Forms) != 1 || len(result.Forms[0].Attributes) != 1 || len(result.Forms[0].Commands) != 1 || len(result.Forms[0].Elements) != 1 {
		t.Fatalf("unexpected form: %#v", result)
	}
}
