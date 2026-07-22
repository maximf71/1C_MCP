package tools

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mcp-1c-analog/internal/mcp"
)

type fakeConfigurationExporter struct {
	extension string
}

func (f *fakeConfigurationExporter) DumpCfg(_ context.Context, destination string) error {
	return os.WriteFile(destination, []byte("cf-data"), 0o600)
}

func (f *fakeConfigurationExporter) DumpExtensionCfg(_ context.Context, destination, extension string) error {
	f.extension = extension
	return os.WriteFile(destination, []byte("cfe-data"), 0o600)
}

func TestExportObjectUsesFixedRootForCFAndCFE(t *testing.T) {
	server := mcp.NewServer("test", "1")
	root := t.TempDir()
	exporter := &fakeConfigurationExporter{}
	if err := RegisterExportObject(server, nil, exporter, "FixedProject", root); err != nil {
		t.Fatal(err)
	}
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"export_object","arguments":{"format":"cf"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"export_object","arguments":{"format":"cfe","extension_name":"SafeExtension"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"export_object","arguments":{"format":"cf","file_name":"..\\escape.cf"}}}`,
	}, "\n") + "\n"
	var output bytes.Buffer
	if err := server.ServeStdio(context.Background(), strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "sha256") || !strings.Contains(output.String(), "file_name must be a plain") {
		t.Fatalf("unexpected export results: %s", output.String())
	}
	if exporter.extension != "SafeExtension" {
		t.Fatalf("extension=%q", exporter.extension)
	}
	for _, path := range []string{filepath.Join(root, "cf", "FixedProject.cf"), filepath.Join(root, "cfe", "SafeExtension.cfe")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("artifact missing: %s: %v", path, err)
		}
	}
}
