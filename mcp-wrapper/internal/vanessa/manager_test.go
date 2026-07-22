package vanessa

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStepsAndSyntax(t *testing.T) {
	root := t.TempDir()
	steps := filepath.Join(root, "steps")
	features := filepath.Join(root, "features")
	_ = os.MkdirAll(steps, 0o700)
	_ = os.MkdirAll(features, 0o700)
	if err := os.WriteFile(filepath.Join(steps, "library.feature"), []byte("Сценарий: шаги\n Дано я открываю форму \"Имя\"\n Тогда число 10 равно 10\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(features, "test.feature"), []byte("Функционал: тест\n Дано я открываю форму \"Контрагенты\"\n Тогда неизвестный шаг\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := &Manager{FeaturesRoot: features, StepsRoot: steps}
	result, err := m.CheckSyntax("test.feature")
	if err != nil {
		t.Fatal(err)
	}
	issues := result["issues"].([]SyntaxIssue)
	if result["valid"].(bool) || len(issues) != 1 || issues[0].Line != 3 {
		t.Fatalf("unexpected result: %#v", result)
	}
	listed, err := m.Steps("открываю", 10)
	if err != nil {
		t.Fatal(err)
	}
	if listed["returned"].(int) != 1 {
		t.Fatalf("unexpected steps: %#v", listed)
	}
}

func TestPathEscapeRejected(t *testing.T) {
	m := &Manager{FeaturesRoot: t.TempDir()}
	if _, err := m.CheckSyntax("..\\secret.feature"); err == nil {
		t.Fatal("expected path escape error")
	}
}

func TestRunCreatesLockedVAParams(t *testing.T) {
	root := t.TempDir()
	features := filepath.Join(root, "features")
	base := filepath.Join(root, "base")
	_ = os.MkdirAll(features, 0o700)
	_ = os.MkdirAll(base, 0o700)
	feature := filepath.Join(features, "smoke.feature")
	runner := filepath.Join(root, "vanessa.epf")
	platform := filepath.Join(root, "1cv8.exe")
	for _, path := range []string{feature, runner, platform} {
		_ = os.WriteFile(path, []byte("test"), 0o600)
	}
	m := &Manager{Platform: platform, Infobase: base, Runner: runner, FeaturesRoot: features, WorkDir: filepath.Join(root, "work"), Timeout: time.Second,
		start: func(ctx context.Context, _ string, _ ...string) (*exec.Cmd, error) {
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestVanessaHelperProcess")
			cmd.Env = append(os.Environ(), "GO_WANT_VANESSA_HELPER=1")
			return cmd, nil
		}}
	result, err := m.Run(context.Background(), "smoke.feature", []string{"smoke"}, nil, false, true)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(result["run_directory"].(string), "VAParams.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"smoke.feature", "junitcreatereport", "onerrorscreenshot"} {
		if !strings.Contains(string(data), marker) {
			t.Fatalf("VAParams has no %q: %s", marker, data)
		}
	}
}

func TestVanessaHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_VANESSA_HELPER") != "1" {
		return
	}
	os.Exit(0)
}
