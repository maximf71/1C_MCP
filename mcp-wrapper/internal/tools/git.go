package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"mcp-1c-analog/internal/mcp"
)

const maxGitOutput = 1 << 20

var safeGitRef = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,199}$`)

type GitOptions struct {
	Root       string
	Executable string
	WorkDir    string
}

func RegisterGit(server *mcp.Server, options GitOptions) error {
	root, err := filepath.Abs(strings.TrimSpace(options.Root))
	if err != nil {
		return fmt.Errorf("resolve git root: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("git root is unavailable: %w", err)
	}
	if info, statErr := os.Stat(filepath.Join(root, ".git")); statErr != nil || (!info.IsDir() && !info.Mode().IsRegular()) {
		return fmt.Errorf("git root is not a repository: %s", root)
	}
	executable := strings.TrimSpace(options.Executable)
	if executable == "" {
		executable, err = exec.LookPath("git")
	} else {
		executable, err = exec.LookPath(executable)
	}
	if err != nil {
		return fmt.Errorf("git executable is unavailable: %w", err)
	}
	hooksDir := filepath.Join(options.WorkDir, "empty-git-hooks")
	if strings.TrimSpace(options.WorkDir) == "" {
		return errors.New("git tool requires a fixed work directory")
	}
	if err := os.MkdirAll(hooksDir, 0o700); err != nil {
		return fmt.Errorf("create empty git hooks directory: %w", err)
	}
	runner := gitRunner{root: root, executable: executable, hooksDir: hooksDir}
	server.AddTool(metadataTool("git", "Operate on one fixed Git repository without shell execution or arbitrary arguments. Mutating operations require confirm=true; hooks and interactive credential prompts are disabled.", schema(map[string]any{
		"operation": enumField("Operation: status, log, diff, branches, switch, commit, push, pull, merge or restore.", "status", "log", "diff", "branches", "switch", "commit", "push", "pull", "merge", "restore"),
		"limit":     field("number", "Commit limit for log; default 20, maximum 200."),
		"paths":     map[string]any{"type": "array", "description": "Optional repository-relative paths.", "items": map[string]any{"type": "string"}, "maxItems": 200},
		"cached":    field("boolean", "Show staged changes for diff, or restore the index for restore."),
		"branch":    field("string", "Existing or new local branch name."),
		"create":    field("boolean", "Create branch during switch."),
		"message":   field("string", "Commit message, maximum 200 characters."),
		"all":       field("boolean", "Stage all tracked and untracked changes before commit."),
		"confirm":   field("boolean", "Required true for every mutating operation."),
	}, "operation"), &mcp.Annotations{Title: "Scoped Git repository operations", ReadOnlyHint: false, DestructiveHint: true, IdempotentHint: false, OpenWorldHint: true}, func(ctx context.Context, args map[string]any) (any, error) {
		return runner.execute(ctx, args)
	}))
	return nil
}

type gitRunner struct {
	root       string
	executable string
	hooksDir   string
}

func (g gitRunner) execute(ctx context.Context, args map[string]any) (any, error) {
	operation := strings.ToLower(strings.TrimSpace(requiredString(args, "operation")))
	paths, err := g.paths(args["paths"])
	if err != nil {
		return nil, err
	}
	readOnlyOperation := operation == "status" || operation == "log" || operation == "diff" || operation == "branches"
	confirmed, _ := args["confirm"].(bool)
	if !readOnlyOperation && !confirmed {
		return nil, errors.New("confirm=true is required for mutating Git operations")
	}
	var command []string
	switch operation {
	case "status":
		command = []string{"status", "--short", "--branch"}
	case "log":
		command = []string{"log", "-n", strconvString(clamp(intArg(args, "limit", 20), 1, 200)), "--date=iso-strict", "--pretty=format:%H%x09%ad%x09%an%x09%s"}
	case "diff":
		command = []string{"diff", "--no-ext-diff", "--no-textconv", "--no-color"}
		if cached, _ := args["cached"].(bool); cached {
			command = append(command, "--cached")
		}
		command = appendPathspec(command, paths)
	case "branches":
		command = []string{"branch", "--format=%(refname:short)\t%(upstream:short)\t%(objectname:short)\t%(subject)"}
	case "switch":
		branch, refErr := requireGitRef(requiredString(args, "branch"))
		if refErr != nil {
			return nil, refErr
		}
		command = []string{"switch"}
		if create, _ := args["create"].(bool); create {
			command = append(command, "-c")
		}
		command = append(command, branch)
	case "commit":
		message := strings.TrimSpace(requiredString(args, "message"))
		if message == "" || len(message) > 200 || strings.ContainsAny(message, "\r\n") {
			return nil, errors.New("message must contain 1-200 characters on one line")
		}
		all, _ := args["all"].(bool)
		if all && len(paths) > 0 {
			return nil, errors.New("use either all=true or explicit paths, not both")
		}
		if !all && len(paths) == 0 {
			return nil, errors.New("commit requires all=true or at least one path")
		}
		if all {
			if _, err := g.run(ctx, "add", "--all"); err != nil {
				return nil, err
			}
		} else {
			if _, err := g.run(ctx, appendPathspec([]string{"add"}, paths)...); err != nil {
				return nil, err
			}
		}
		command = []string{"commit", "-m", message}
	case "push":
		command = []string{"push"}
	case "pull":
		command = []string{"pull", "--ff-only"}
	case "merge":
		branch, refErr := requireGitRef(requiredString(args, "branch"))
		if refErr != nil {
			return nil, refErr
		}
		command = []string{"merge", "--ff-only", branch}
	case "restore":
		if len(paths) == 0 {
			return nil, errors.New("restore requires at least one explicit path")
		}
		command = []string{"restore", "--worktree"}
		if cached, _ := args["cached"].(bool); cached {
			command = append(command, "--staged")
		}
		command = appendPathspec(command, paths)
	default:
		return nil, errors.New("unsupported Git operation")
	}
	output, err := g.run(ctx, command...)
	return map[string]any{"operation": operation, "repository": g.root, "output": output, "success": err == nil}, err
}

func (g gitRunner) run(parent context.Context, arguments ...string) (string, error) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	base := []string{"--no-pager", "-c", "core.hooksPath=" + g.hooksDir, "-c", "commit.gpgSign=false", "-c", "merge.gpgSign=false", "-C", g.root}
	command := exec.CommandContext(ctx, g.executable, append(base, arguments...)...)
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=Never")
	output, err := command.CombinedOutput()
	if len(output) > maxGitOutput {
		output = append(output[:maxGitOutput], []byte("\n... [truncated]")...)
	}
	text := strings.TrimSpace(string(output))
	if ctx.Err() == context.DeadlineExceeded {
		return text, errors.New("Git operation timed out")
	}
	if err != nil {
		if text == "" {
			return text, fmt.Errorf("git %s failed: %w", arguments[0], err)
		}
		return text, fmt.Errorf("git %s failed: %w; output: %s", arguments[0], err, text)
	}
	return text, nil
}

func (g gitRunner) paths(raw any) ([]string, error) {
	items, _ := raw.([]any)
	if raw != nil && items == nil {
		return nil, errors.New("paths must be an array")
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		value := strings.TrimSpace(fmt.Sprint(item))
		if value == "" || filepath.IsAbs(value) {
			return nil, errors.New("paths must contain non-empty repository-relative paths")
		}
		clean := filepath.Clean(value)
		if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("path escapes the fixed repository: %s", value)
		}
		candidate := filepath.Join(g.root, clean)
		if resolved, err := filepath.EvalSymlinks(candidate); err == nil && !isWithin(g.root, resolved) {
			return nil, fmt.Errorf("path resolves outside the fixed repository: %s", value)
		}
		result = append(result, filepath.ToSlash(clean))
	}
	return result, nil
}

func appendPathspec(command, paths []string) []string {
	if len(paths) == 0 {
		return command
	}
	command = append(command, "--")
	return append(command, paths...)
}

func requireGitRef(value string) (string, error) {
	value = strings.TrimSpace(value)
	if !safeGitRef.MatchString(value) || strings.Contains(value, "..") || strings.Contains(value, "@{") || strings.HasSuffix(value, ".lock") || strings.HasSuffix(value, "/") {
		return "", errors.New("branch is not a safe Git reference name")
	}
	return value, nil
}

func strconvString(value int) string { return fmt.Sprintf("%d", value) }
