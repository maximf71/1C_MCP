package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestScopedGitRunner(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is unavailable")
	}
	repository := t.TempDir()
	for _, args := range [][]string{{"init"}, {"config", "user.email", "test@example.invalid"}, {"config", "user.name", "MCP Test"}} {
		command := exec.Command(git, append([]string{"-C", repository}, args...)...)
		if output, runErr := command.CombinedOutput(); runErr != nil {
			t.Fatalf("git %v: %v: %s", args, runErr, output)
		}
	}
	if err := os.WriteFile(filepath.Join(repository, "file.txt"), []byte("value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := gitRunner{root: repository, executable: git, hooksDir: t.TempDir()}
	if _, err := runner.execute(context.Background(), map[string]any{"operation": "commit", "all": true, "message": "initial"}); err == nil {
		t.Fatal("mutation without confirm was accepted")
	}
	result, err := runner.execute(context.Background(), map[string]any{"operation": "commit", "all": true, "message": "initial", "confirm": true})
	if err != nil {
		t.Fatal(err)
	}
	if result.(map[string]any)["success"] != true {
		t.Fatalf("commit result=%#v", result)
	}
	if _, err := runner.execute(context.Background(), map[string]any{"operation": "diff", "paths": []any{"../escape"}}); err == nil {
		t.Fatal("escaping path was accepted")
	}
	if _, err := runner.execute(context.Background(), map[string]any{"operation": "status"}); err != nil {
		t.Fatal(err)
	}
}

func TestRequireGitRefRejectsOptionAndRevisionSyntax(t *testing.T) {
	for _, value := range []string{"-danger", "main..other", "main@{1}", "bad branch", "topic/"} {
		if _, err := requireGitRef(value); err == nil {
			t.Fatalf("unsafe ref accepted: %q", value)
		}
	}
}
