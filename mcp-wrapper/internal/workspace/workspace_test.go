package workspace

import (
	"strings"
	"testing"
)

func TestRenderAndMemory(t *testing.T) {
	rendered, err := RenderTemplate("bsl_exported_procedure", map[string]any{"name": "Test", "parameters": "", "body": "Возврат;"})
	if err != nil || !strings.Contains(rendered, "Процедура Test") {
		t.Fatalf("unexpected template: %q, %v", rendered, err)
	}
	memory := Memory{Root: t.TempDir()}
	if _, err := memory.Put("decision", "use EDT"); err != nil {
		t.Fatal(err)
	}
	values, err := memory.List()
	if err != nil || len(values) != 1 || values[0].Value != "use EDT" {
		t.Fatalf("unexpected memory: %#v, %v", values, err)
	}
}
