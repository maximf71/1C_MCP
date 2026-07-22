package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	maximumModuleBytes = 16 << 20
	maximumProcessLog  = 1 << 20
)

type bslReviewOptions struct {
	ProjectRoot     string
	WorkDir         string
	Executable      string
	JavaExecutable  string
	Config          string
	ModulePaths     []string
	MinimumSeverity string
	Limit           int
}

type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

type Diagnostic struct {
	Path     string `json:"path"`
	Range    Range  `json:"range"`
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Source   string `json:"source"`
	Message  string `json:"message"`
}

type bslReviewResult struct {
	Analyzer         string         `json:"analyzer"`
	Scope            string         `json:"scope"`
	Modules          []string       `json:"modules,omitempty"`
	MinimumSeverity  string         `json:"minimum_severity"`
	TotalDiagnostics int            `json:"total_diagnostics"`
	Returned         int            `json:"returned"`
	Truncated        bool           `json:"truncated"`
	Summary          map[string]int `json:"summary"`
	Diagnostics      []Diagnostic   `json:"diagnostics"`
	DurationMillis   int64          `json:"duration_ms"`
}

type report struct {
	FileInfos []fileInfo `json:"fileinfos"`
}

type fileInfo struct {
	Path        string          `json:"path"`
	Diagnostics []rawDiagnostic `json:"diagnostics"`
}

type rawDiagnostic struct {
	Range    Range  `json:"range"`
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Source   string `json:"source"`
	Message  string `json:"message"`
}

func runBSLReview(ctx context.Context, options bslReviewOptions) (bslReviewResult, error) {
	if strings.TrimSpace(options.ProjectRoot) == "" || !filepath.IsAbs(options.ProjectRoot) {
		return bslReviewResult{}, errors.New("project root must be absolute")
	}
	executable, err := validateFile(options.Executable, "BSL Language Server")
	if err != nil {
		return bslReviewResult{}, err
	}
	projectSource, err := below(options.ProjectRoot, "src")
	if err != nil {
		return bslReviewResult{}, err
	}
	if info, err := os.Stat(projectSource); err != nil || !info.IsDir() {
		return bslReviewResult{}, errors.New("fixed EDT project has no src directory")
	}
	temporary, err := os.MkdirTemp(options.WorkDir, "bsl-review-")
	if err != nil {
		return bslReviewResult{}, fmt.Errorf("create code review workspace: %w", err)
	}
	defer os.RemoveAll(temporary)
	source := projectSource
	modules, err := normalizeModules(options.ModulePaths)
	if err != nil {
		return bslReviewResult{}, err
	}
	if len(modules) > 0 {
		source = filepath.Join(temporary, "source")
		for _, module := range modules {
			from, err := below(projectSource, filepath.FromSlash(module))
			if err != nil {
				return bslReviewResult{}, err
			}
			to := filepath.Join(source, filepath.FromSlash(module))
			if err := copyModule(from, to); err != nil {
				return bslReviewResult{}, fmt.Errorf("copy module %s: %w", module, err)
			}
		}
	}
	workspace := filepath.Join(temporary, "workspace")
	output := filepath.Join(temporary, "output")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		return bslReviewResult{}, err
	}
	if err := os.MkdirAll(output, 0o700); err != nil {
		return bslReviewResult{}, err
	}
	command, commandArgs, err := analyzerCommand(executable, options.JavaExecutable, source, workspace, output, options.Config)
	if err != nil {
		return bslReviewResult{}, err
	}
	started := time.Now()
	cmd := exec.CommandContext(ctx, command, commandArgs...)
	cmd.Dir = temporary
	var processLog limitedBuffer
	cmd.Stdout, cmd.Stderr = &processLog, &processLog
	if err := cmd.Run(); err != nil {
		return bslReviewResult{}, fmt.Errorf("BSL Language Server failed: %w; output: %s", err, processLog.String())
	}
	data, err := os.ReadFile(filepath.Join(output, "bsl-json.json"))
	if err != nil {
		return bslReviewResult{}, fmt.Errorf("read BSL Language Server report: %w; output: %s", err, processLog.String())
	}
	minimum := normalizeSeverity(options.MinimumSeverity)
	limit := options.Limit
	if limit <= 0 {
		limit = 200
	}
	result, err := parseReport(data, source, minimum, limit)
	if err != nil {
		return bslReviewResult{}, err
	}
	result.Analyzer = filepath.Base(executable)
	result.MinimumSeverity = minimum
	result.DurationMillis = time.Since(started).Milliseconds()
	if len(modules) == 0 {
		result.Scope = "project"
	} else {
		result.Scope = "modules"
		result.Modules = modules
		normalizeSelectedModulePaths(result.Diagnostics, modules)
	}
	return result, nil
}

func normalizeSelectedModulePaths(diagnostics []Diagnostic, modules []string) {
	for index := range diagnostics {
		current := filepath.ToSlash(diagnostics[index].Path)
		var matches []string
		for _, module := range modules {
			if strings.EqualFold(current, module) || strings.HasSuffix(strings.ToLower(current), strings.ToLower("/"+module)) ||
				strings.EqualFold(filepath.Base(current), filepath.Base(module)) {
				matches = append(matches, module)
			}
		}
		if len(matches) == 1 {
			diagnostics[index].Path = matches[0]
		}
	}
}

func analyzerCommand(executable, java, source, workspace, output, config string) (string, []string, error) {
	arguments := []string{"analyze", "-s", source, "-w", workspace, "-o", output, "-r", "json", "-q"}
	if strings.TrimSpace(config) != "" {
		configPath, err := validateFile(config, "BSL Language Server configuration")
		if err != nil {
			return "", nil, err
		}
		arguments = append(arguments, "-c", configPath)
	}
	if strings.EqualFold(filepath.Ext(executable), ".jar") {
		if strings.TrimSpace(java) == "" {
			java = "java"
		} else {
			var err error
			java, err = validateFile(java, "Java executable")
			if err != nil {
				return "", nil, err
			}
		}
		arguments = append([]string{"-jar", executable}, arguments...)
		return java, arguments, nil
	}
	return executable, arguments, nil
}

func parseReport(data []byte, sourceRoot, minimum string, limit int) (bslReviewResult, error) {
	var decoded report
	if err := json.Unmarshal(data, &decoded); err != nil {
		return bslReviewResult{}, fmt.Errorf("decode BSL Language Server report: %w", err)
	}
	threshold := severityRank(minimum)
	result := bslReviewResult{Summary: map[string]int{"error": 0, "warning": 0, "information": 0, "hint": 0}}
	for _, file := range decoded.FileInfos {
		path := relativeReportPath(sourceRoot, file.Path)
		for _, item := range file.Diagnostics {
			severity := normalizeSeverity(item.Severity)
			result.Summary[severity]++
			if severityRank(severity) < threshold {
				continue
			}
			result.TotalDiagnostics++
			result.Diagnostics = append(result.Diagnostics, Diagnostic{
				Path: path, Range: item.Range, Severity: severity, Code: item.Code,
				Source: item.Source, Message: item.Message,
			})
		}
	}
	sort.SliceStable(result.Diagnostics, func(i, j int) bool {
		if severityRank(result.Diagnostics[i].Severity) != severityRank(result.Diagnostics[j].Severity) {
			return severityRank(result.Diagnostics[i].Severity) > severityRank(result.Diagnostics[j].Severity)
		}
		if result.Diagnostics[i].Path != result.Diagnostics[j].Path {
			return result.Diagnostics[i].Path < result.Diagnostics[j].Path
		}
		return result.Diagnostics[i].Range.Start.Line < result.Diagnostics[j].Range.Start.Line
	})
	if len(result.Diagnostics) > limit {
		result.Diagnostics = result.Diagnostics[:limit]
	}
	result.Returned = len(result.Diagnostics)
	result.Truncated = result.Returned < result.TotalDiagnostics
	return result, nil
}

func normalizeModules(items []string) ([]string, error) {
	seen := map[string]bool{}
	result := make([]string, 0, len(items))
	for _, item := range items {
		item = filepath.ToSlash(filepath.Clean(strings.TrimSpace(item)))
		if item == "." || strings.HasPrefix(item, "../") || strings.HasPrefix(item, "/") || !strings.EqualFold(filepath.Ext(item), ".bsl") {
			return nil, fmt.Errorf("unsafe module path %q", item)
		}
		key := strings.ToLower(item)
		if !seen[key] {
			seen[key] = true
			result = append(result, item)
		}
	}
	sort.Strings(result)
	return result, nil
}

func copyModule(from, to string) error {
	info, err := os.Stat(from)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > maximumModuleBytes {
		return errors.New("module is not a regular file or is too large")
	}
	input, err := os.Open(from)
	if err != nil {
		return err
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return err
	}
	output, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer output.Close()
	_, err = io.Copy(output, io.LimitReader(input, maximumModuleBytes+1))
	return err
}

func below(root, relative string) (string, error) {
	target := filepath.Clean(filepath.Join(root, relative))
	rel, err := filepath.Rel(filepath.Clean(root), target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes the fixed project")
	}
	return target, nil
}

func validateFile(value, label string) (string, error) {
	value = filepath.Clean(strings.TrimSpace(value))
	if !filepath.IsAbs(value) {
		return "", fmt.Errorf("%s path must be absolute", label)
	}
	info, err := os.Stat(value)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s file is unavailable", label)
	}
	return value, nil
}

func normalizeSeverity(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "error":
		return "error"
	case "warning", "warn":
		return "warning"
	case "hint":
		return "hint"
	default:
		return "information"
	}
}

func severityRank(value string) int {
	return map[string]int{"hint": 1, "information": 2, "warning": 3, "error": 4}[normalizeSeverity(value)]
}

func relativeReportPath(root, value string) string {
	normalized := strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(value)), "./")
	if strings.HasPrefix(strings.ToLower(normalized), "file:") {
		if parsed, err := url.Parse(normalized); err == nil {
			path := parsed.Path
			if len(path) >= 3 && path[0] == '/' && path[2] == ':' {
				path = path[1:]
			}
			value = filepath.FromSlash(path)
		}
	}
	value = filepath.Clean(value)
	if !filepath.IsAbs(value) {
		return filepath.ToSlash(value)
	}
	relative, err := filepath.Rel(root, value)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		slash := filepath.ToSlash(value)
		if marker := strings.LastIndex(strings.ToLower(slash), "/source/"); marker >= 0 {
			return slash[marker+len("/source/"):]
		}
		return filepath.Base(value)
	}
	return filepath.ToSlash(relative)
}

type limitedBuffer struct {
	data bytes.Buffer
}

func (b *limitedBuffer) Write(value []byte) (int, error) {
	original := len(value)
	remaining := maximumProcessLog - b.data.Len()
	if remaining > 0 {
		if len(value) > remaining {
			value = value[:remaining]
		}
		_, _ = b.data.Write(value)
	}
	return original, nil
}

func (b *limitedBuffer) String() string {
	return strings.TrimSpace(b.data.String())
}
