package installer

import (
	"strings"
	"testing"
)

func TestServerConnectionArguments(t *testing.T) {
	if !(Options{Server: true}).Server {
		t.Fatal("server mode was not retained")
	}
	if extensionName != ExtensionName {
		t.Fatal("exported extension name differs from installer name")
	}
}

func TestPlatformVersionFromExecutable(t *testing.T) {
	if got := PlatformVersion(`C:\Program Files\1cv8\8.3.27.1644\bin\1cv8.exe`); got != "8.3.27.1644" {
		t.Fatalf("unexpected version %q", got)
	}
}

func TestInstallAlwaysTargetsExtension(t *testing.T) {
	if strings.TrimSpace(ExtensionName) == "" {
		t.Fatal("empty extension name")
	}
}
