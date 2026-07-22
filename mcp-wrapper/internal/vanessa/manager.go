package vanessa

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var stepLine = regexp.MustCompile(`(?i)^\s*(?:дано|когда|тогда|и|но|given|when|then|and|but)\s+(.+?)\s*$`)

type Manager struct {
	Platform     string
	Infobase     string
	Runner       string
	FeaturesRoot string
	StepsRoot    string
	WorkDir      string
	Timeout      time.Duration
	start        func(context.Context, string, ...string) (*exec.Cmd, error)
}

type SyntaxIssue struct {
	File        string   `json:"file"`
	Line        int      `json:"line"`
	Step        string   `json:"step"`
	Suggestions []string `json:"suggestions,omitempty"`
}

func (m *Manager) Status() map[string]any {
	return map[string]any{"configured": m.Platform != "" && m.Infobase != "" && m.Runner != "" && m.FeaturesRoot != "", "platform": m.Platform, "infobase": m.Infobase, "runner": m.Runner, "features_root": m.FeaturesRoot, "steps_root": m.stepsRoot()}
}

func (m *Manager) Steps(query string, limit int) (map[string]any, error) {
	steps, err := collectSteps(m.stepsRoot())
	if err != nil {
		return nil, err
	}
	query = strings.ToLower(strings.TrimSpace(query))
	filtered := make([]string, 0, len(steps))
	for _, step := range steps {
		if query == "" || strings.Contains(strings.ToLower(step), query) {
			filtered = append(filtered, step)
		}
	}
	if limit <= 0 {
		limit = 200
	}
	if limit > 2000 {
		limit = 2000
	}
	total := len(filtered)
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}
	return map[string]any{"root": m.stepsRoot(), "total": total, "returned": len(filtered), "steps": filtered}, nil
}

func (m *Manager) CheckSyntax(relative string) (map[string]any, error) {
	path, err := below(m.FeaturesRoot, relative)
	if err != nil {
		return nil, err
	}
	known, err := collectSteps(m.stepsRoot())
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var issues []SyntaxIssue
	checked := 0
	scanner := bufio.NewScanner(file)
	line := 0
	for scanner.Scan() {
		line++
		match := stepLine.FindStringSubmatch(scanner.Text())
		if len(match) != 2 {
			continue
		}
		checked++
		step := strings.TrimSpace(match[1])
		if matchesKnown(step, known) {
			continue
		}
		issues = append(issues, SyntaxIssue{File: filepath.ToSlash(relative), Line: line, Step: step, Suggestions: suggest(step, known, 3)})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return map[string]any{"valid": len(issues) == 0, "checked_steps": checked, "issues": issues}, nil
}

func (m *Manager) Run(ctx context.Context, relative string, tags, ignoreTags []string, keepOpen, screenshot bool) (map[string]any, error) {
	if err := m.validateRun(); err != nil {
		return nil, err
	}
	feature, err := below(m.FeaturesRoot, relative)
	if err != nil {
		return nil, err
	}
	if _, err = os.Stat(feature); err != nil {
		return nil, err
	}
	runDir := filepath.Join(m.WorkDir, "vanessa", time.Now().UTC().Format("20060102T150405.000000000"))
	if err = os.MkdirAll(runDir, 0o700); err != nil {
		return nil, err
	}
	params := map[string]any{"featurepath": feature, "filtertags": tags, "ignoretags": ignoreTags, "junitcreatereport": true, "junitpath": filepath.Join(runDir, "junit"), "onerrorscreenshot": screenshot, "outputscreenshot": filepath.Join(runDir, "screenshots"), "projectpath": m.FeaturesRoot}
	data, _ := json.MarshalIndent(params, "", "  ")
	paramsPath := filepath.Join(runDir, "VAParams.json")
	if err = os.WriteFile(paramsPath, data, 0o600); err != nil {
		return nil, err
	}
	args := []string{"ENTERPRISE", "/F", m.Infobase, "/TestManager", "/Execute", m.Runner, "/C", "StartFeaturePlayer;VAParams=" + paramsPath}
	timeout := m.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Minute
	}
	parent := ctx
	if keepOpen {
		parent = context.Background()
	}
	runCtx, cancel := context.WithTimeout(parent, timeout)
	cmd, err := m.command(runCtx, m.Platform, args...)
	if err != nil {
		cancel()
		return nil, err
	}
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err = cmd.Start(); err != nil {
		cancel()
		return nil, err
	}
	if keepOpen {
		go func() { _ = cmd.Wait(); cancel() }()
		return map[string]any{"started": true, "pid": cmd.Process.Pid, "keep_open": true, "run_directory": runDir}, nil
	}
	err = cmd.Wait()
	cancel()
	result := map[string]any{"started": true, "keep_open": false, "run_directory": runDir, "exit_code": cmd.ProcessState.ExitCode(), "junit_directory": filepath.Join(runDir, "junit")}
	if runCtx.Err() == context.DeadlineExceeded {
		return result, fmt.Errorf("Vanessa Automation timed out after %s", timeout)
	}
	if err != nil {
		return result, fmt.Errorf("Vanessa Automation failed: %w", err)
	}
	return result, nil
}

func (m *Manager) validateRun() error {
	for name, path := range map[string]string{"platform": m.Platform, "infobase": m.Infobase, "runner": m.Runner, "features root": m.FeaturesRoot, "work directory": m.WorkDir} {
		if strings.TrimSpace(path) == "" {
			return fmt.Errorf("Vanessa Automation %s is not configured", name)
		}
	}
	for name, path := range map[string]string{"platform": m.Platform, "infobase": m.Infobase, "runner": m.Runner, "features root": m.FeaturesRoot} {
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("Vanessa Automation %s is unavailable: %w", name, err)
		}
	}
	return nil
}

func (m *Manager) command(ctx context.Context, name string, args ...string) (*exec.Cmd, error) {
	if m.start != nil {
		return m.start(ctx, name, args...)
	}
	return exec.CommandContext(ctx, name, args...), nil
}
func (m *Manager) stepsRoot() string {
	if m.StepsRoot != "" {
		return m.StepsRoot
	}
	return m.FeaturesRoot
}

func collectSteps(root string) ([]string, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("Vanessa steps root is not configured")
	}
	unique := map[string]string{}
	err := filepath.WalkDir(root, func(path string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() || !strings.EqualFold(filepath.Ext(path), ".feature") {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		s := bufio.NewScanner(f)
		for s.Scan() {
			match := stepLine.FindStringSubmatch(s.Text())
			if len(match) == 2 {
				step := strings.TrimSpace(match[1])
				unique[normalize(step)] = step
			}
		}
		return s.Err()
	})
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(unique))
	for _, step := range unique {
		result = append(result, step)
	}
	sort.Strings(result)
	return result, nil
}
func normalize(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	quoted := regexp.MustCompile(`"[^"]*"`).ReplaceAllString(value, "{string}")
	numbers := regexp.MustCompile(`\b\d+(?:[.,]\d+)?\b`).ReplaceAllString(quoted, "{number}")
	return strings.Join(strings.Fields(numbers), " ")
}
func matchesKnown(step string, known []string) bool {
	value := normalize(step)
	for _, candidate := range known {
		if value == normalize(candidate) {
			return true
		}
	}
	return false
}
func suggest(step string, known []string, limit int) []string {
	type scored struct {
		s string
		n int
	}
	needle := strings.ToLower(step)
	all := make([]scored, 0, len(known))
	for _, candidate := range known {
		score := commonPrefix(needle, strings.ToLower(candidate))
		if strings.Contains(strings.ToLower(candidate), firstWord(needle)) {
			score += 10
		}
		all = append(all, scored{candidate, score})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].n > all[j].n })
	var result []string
	for _, item := range all {
		if item.n <= 0 || len(result) >= limit {
			break
		}
		result = append(result, item.s)
	}
	return result
}
func commonPrefix(a, b string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}
func firstWord(value string) string {
	parts := strings.Fields(value)
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}
func below(root, relative string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", errors.New("fixed Vanessa features root is not configured")
	}
	if filepath.IsAbs(relative) {
		return "", errors.New("feature path must be relative to the fixed root")
	}
	clean := filepath.Clean(relative)
	if clean == "." || clean == "" || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("feature path escapes the fixed root")
	}
	target := filepath.Join(root, clean)
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("feature path escapes the fixed root")
	}
	return target, nil
}
