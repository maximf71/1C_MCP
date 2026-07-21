package metadata

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCloneMetadataObject(t *testing.T) {
	dump := t.TempDir()
	mustWrite(t, filepath.Join(dump, "Configuration.xml"), `<?xml version="1.0" encoding="UTF-8"?>
<MetaDataObject><Configuration><InternalInfo/><Properties><Name>Test</Name></Properties><ChildObjects>
			<Document>Источник</Document>
		</ChildObjects></Configuration></MetaDataObject>`)
	mustWrite(t, filepath.Join(dump, "ConfigDumpInfo.xml"), `<?xml version="1.0" encoding="UTF-8"?>
<ConfigDumpInfo><ConfigVersions>
		<Metadata name="Document.Источник" id="11111111-1111-4111-8111-111111111111" configVersion="abc">
			<Metadata name="Document.Источник.Attribute.Реквизит" id="22222222-2222-4222-8222-222222222222"/>
		</Metadata>
		<Metadata name="Document.Источник.ObjectModule" id="11111111-1111-4111-8111-111111111111.0" configVersion="def"/>
	</ConfigVersions></ConfigDumpInfo>`)
	mustWrite(t, filepath.Join(dump, "Documents", "Источник.xml"), `<?xml version="1.0" encoding="UTF-8"?>
<MetaDataObject><Document uuid="11111111-1111-4111-8111-111111111111"><InternalInfo>
<GeneratedType name="DocumentObject.Источник"><TypeId>33333333-3333-4333-8333-333333333333</TypeId></GeneratedType>
</InternalInfo><Properties><Name>Источник</Name><Synonym>Не менять Источник</Synonym></Properties><ChildObjects>
<Attribute uuid="22222222-2222-4222-8222-222222222222"><Properties><Name>Реквизит</Name></Properties></Attribute>
</ChildObjects></Document></MetaDataObject>`)
	module := "Процедура Тест()\n\tСообщить(\"Источник\");\nКонецПроцедуры\n"
	mustWrite(t, filepath.Join(dump, "Documents", "Источник", "Ext", "ObjectModule.bsl"), module)

	result, err := Clone(dump, "Document", "Источник", "Источник1")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.UUIDMap) != 3 {
		t.Fatalf("expected 3 regenerated UUIDs, got %d", len(result.UUIDMap))
	}
	targetXML, err := os.ReadFile(filepath.Join(dump, "Documents", "Источник1.xml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(targetXML)
	if !strings.Contains(text, "<Name>Источник1</Name>") || !strings.Contains(text, "DocumentObject.Источник1") {
		t.Fatalf("target identity was not updated: %s", text)
	}
	if strings.Contains(text, "11111111-1111-4111-8111-111111111111") {
		t.Fatal("source UUID remains in target XML")
	}
	moduleData, err := os.ReadFile(filepath.Join(dump, "Documents", "Источник1", "Ext", "ObjectModule.bsl"))
	if err != nil {
		t.Fatal(err)
	}
	if string(moduleData) != module {
		t.Fatal("BSL module was modified")
	}
	configData, _ := os.ReadFile(filepath.Join(dump, "Configuration.xml"))
	if !strings.Contains(string(configData), "<Document>Источник1</Document>") {
		t.Fatal("Configuration.xml does not contain target")
	}
	dumpInfo, _ := os.ReadFile(filepath.Join(dump, "ConfigDumpInfo.xml"))
	if !strings.Contains(string(dumpInfo), `name="Document.Источник1"`) {
		t.Fatal("ConfigDumpInfo.xml does not contain target")
	}
	if err := Equivalent(dump, "Document", "Источник", "Источник1"); err != nil {
		t.Fatalf("clone must be equivalent: %v", err)
	}
}

func TestValidIdentifierAndPlanPath(t *testing.T) {
	if !ValidIdentifier("УслугиОказанные1") || ValidIdentifier("..\\bad") || ValidIdentifier("1bad") {
		t.Fatal("identifier validation is incorrect")
	}
	manager := &Manager{WorkDir: t.TempDir()}
	if _, err := manager.planDir("..\\escape"); err == nil {
		t.Fatal("path traversal plan ID must be rejected")
	}
	if _, err := manager.planDir(strings.Repeat("a", 32)); err != nil {
		t.Fatalf("valid plan ID rejected: %v", err)
	}
}

func TestStaleFingerprintIsRejected(t *testing.T) {
	if err := validateFingerprint("before", "after"); err == nil || !strings.Contains(err.Error(), "configuration changed") {
		t.Fatalf("stale configuration must be rejected, got %v", err)
	}
	if err := validateFingerprint("same", "same"); err != nil {
		t.Fatalf("matching fingerprint rejected: %v", err)
	}
}

func mustWrite(t *testing.T, path, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}
