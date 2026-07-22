package designer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPasswordIsRedactedAndArgumentsStaySeparate(t *testing.T) {
	client := New("1cv8.exe", `C:\Bases\Base With Spaces`, "Администратор", "very-secret", t.TempDir())
	var captured []string
	client.run = func(ctx context.Context, name string, args ...string) error {
		captured = append([]string(nil), args...)
		for index, arg := range args {
			if arg == "/Out" && index+1 < len(args) {
				_ = os.MkdirAll(filepath.Dir(args[index+1]), 0o700)
				_ = os.WriteFile(args[index+1], []byte("Ошибка: very-secret"), 0o600)
			}
		}
		return errors.New("exit status 1")
	}
	err := client.DumpConfig(context.Background(), filepath.Join(t.TempDir(), "dump"))
	if err == nil {
		t.Fatal("expected Designer failure")
	}
	if strings.Contains(err.Error(), "very-secret") {
		t.Fatalf("password leaked in error: %v", err)
	}
	foundBase := false
	for _, value := range captured {
		if value == client.Infobase {
			foundBase = true
		}
	}
	if !foundBase {
		t.Fatal("infobase path was not passed as one argument")
	}
}

func TestHasLogErrorDoesNotMatchMetadataNames(t *testing.T) {
	success := "Новый объект: РегламентноеЗадание.СборИОтправкаОтчетовОбОшибках\nОбновление конфигурации успешно завершено"
	if hasLogError(success) {
		t.Fatal("successful UpdateDBCfg log was classified as an error")
	}
	if !hasLogError("Ошибка блокировки информационной базы для конфигурирования.") {
		t.Fatal("real Designer error was not detected")
	}
}

func TestDumpExtensionCfgUsesSeparateExtensionArgument(t *testing.T) {
	client := New("1cv8.exe", `C:\Bases\Base`, "", "", t.TempDir())
	var captured []string
	client.run = func(ctx context.Context, name string, args ...string) error {
		captured = append([]string(nil), args...)
		return nil
	}
	destination := filepath.Join(t.TempDir(), "extension.cfe")
	if err := client.DumpExtensionCfg(context.Background(), destination, "SafeExtension"); err != nil {
		t.Fatal(err)
	}
	found := false
	for index, arg := range captured {
		if arg == "-Extension" && index+1 < len(captured) && captured[index+1] == "SafeExtension" {
			found = true
		}
	}
	if !found {
		t.Fatalf("extension selector was not passed safely: %#v", captured)
	}
}
