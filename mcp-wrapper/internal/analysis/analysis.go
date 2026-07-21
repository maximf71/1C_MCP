package analysis

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"mcp-1c-analog/internal/dump"
)

type Diagnostic struct {
	Path     string `json:"path"`
	Line     int    `json:"line,omitempty"`
	Severity string `json:"severity"`
	Rule     string `json:"rule"`
	Message  string `json:"message"`
}

type Symbol struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Line int    `json:"line"`
	Kind string `json:"kind"`
}

type Call struct {
	Caller string `json:"caller"`
	Callee string `json:"callee"`
	Path   string `json:"path"`
	Line   int    `json:"line"`
}

type Report struct {
	Root        string       `json:"root"`
	Files       int          `json:"files"`
	Diagnostics []Diagnostic `json:"diagnostics"`
	Symbols     []Symbol     `json:"symbols"`
	Calls       []Call       `json:"calls"`
}

var declaration = regexp.MustCompile(`(?i)^\s*(процедура|функция|procedure|function)\s+([\p{L}_][\p{L}\p{N}_]*)\s*\(`)
var callExpression = regexp.MustCompile(`([\p{L}_][\p{L}\p{N}_]*)\s*\(`)

func AnalyzeDump(root string) (Report, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return Report{}, err
	}
	result := Report{Root: absolute}
	err = filepath.WalkDir(absolute, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return nil
		}
		extension := strings.ToLower(filepath.Ext(path))
		if extension != ".bsl" && extension != ".xml" {
			return nil
		}
		result.Files++
		relative, _ := filepath.Rel(absolute, path)
		if extension == ".xml" {
			result.Diagnostics = append(result.Diagnostics, validateXMLFile(path, filepath.ToSlash(relative))...)
		} else {
			diagnostics, symbols, calls := analyzeBSLFile(path, filepath.ToSlash(relative))
			result.Diagnostics = append(result.Diagnostics, diagnostics...)
			result.Symbols = append(result.Symbols, symbols...)
			result.Calls = append(result.Calls, calls...)
		}
		return nil
	})
	if err != nil {
		return Report{}, err
	}
	sort.Slice(result.Diagnostics, func(i, j int) bool {
		if result.Diagnostics[i].Path == result.Diagnostics[j].Path {
			return result.Diagnostics[i].Line < result.Diagnostics[j].Line
		}
		return result.Diagnostics[i].Path < result.Diagnostics[j].Path
	})
	sort.Slice(result.Symbols, func(i, j int) bool { return result.Symbols[i].Name < result.Symbols[j].Name })
	return result, nil
}

func ValidateXML(root string) ([]Diagnostic, error) {
	report, err := AnalyzeDump(root)
	if err != nil {
		return nil, err
	}
	var result []Diagnostic
	for _, diagnostic := range report.Diagnostics {
		if strings.HasPrefix(diagnostic.Rule, "xml-") {
			result = append(result, diagnostic)
		}
	}
	return result, nil
}

func LintBSL(root string) ([]Diagnostic, error) {
	report, err := AnalyzeDump(root)
	if err != nil {
		return nil, err
	}
	var result []Diagnostic
	for _, diagnostic := range report.Diagnostics {
		if strings.HasPrefix(diagnostic.Rule, "bsl-") {
			result = append(result, diagnostic)
		}
	}
	return result, nil
}

func validateXMLFile(path, relative string) []Diagnostic {
	file, err := os.Open(path)
	if err != nil {
		return []Diagnostic{{Path: relative, Severity: "error", Rule: "xml-read", Message: err.Error()}}
	}
	defer file.Close()
	decoder := xml.NewDecoder(file)
	for {
		_, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return []Diagnostic{{Path: relative, Severity: "error", Rule: "xml-well-formed", Message: err.Error()}}
		}
	}
}

func analyzeBSLFile(path, relative string) ([]Diagnostic, []Symbol, []Call) {
	file, err := os.Open(path)
	if err != nil {
		return []Diagnostic{{Path: relative, Severity: "error", Rule: "bsl-read", Message: err.Error()}}, nil, nil
	}
	defer file.Close()
	var diagnostics []Diagnostic
	var symbols []Symbol
	var calls []Call
	current := "<module>"
	procedureDepth := 0
	tryDepth := 0
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if len([]rune(line)) > 160 {
			diagnostics = append(diagnostics, Diagnostic{Path: relative, Line: lineNumber, Severity: "warning", Rule: "bsl-line-length", Message: "line is longer than 160 characters"})
		}
		if match := declaration.FindStringSubmatch(trimmed); len(match) == 3 {
			current = match[2]
			procedureDepth++
			kind := "procedure"
			if strings.EqualFold(match[1], "функция") || strings.EqualFold(match[1], "function") {
				kind = "function"
			}
			symbols = append(symbols, Symbol{Name: current, Path: relative, Line: lineNumber, Kind: kind})
		}
		if strings.HasPrefix(lower, "конецпроцедуры") || strings.HasPrefix(lower, "конецфункции") || strings.HasPrefix(lower, "endprocedure") || strings.HasPrefix(lower, "endfunction") {
			procedureDepth--
			if procedureDepth < 0 {
				diagnostics = append(diagnostics, Diagnostic{Path: relative, Line: lineNumber, Severity: "error", Rule: "bsl-unbalanced-method", Message: "method terminator without declaration"})
				procedureDepth = 0
			}
			current = "<module>"
		}
		if lower == "попытка" || lower == "try" {
			tryDepth++
		}
		if strings.HasPrefix(lower, "конецпопытки") || strings.HasPrefix(lower, "endtry") {
			tryDepth--
		}
		if !strings.HasPrefix(trimmed, "//") && declaration.FindString(trimmed) == "" {
			for _, match := range callExpression.FindAllStringSubmatch(trimmed, -1) {
				if len(match) == 2 && !bslKeyword(match[1]) {
					calls = append(calls, Call{Caller: current, Callee: match[1], Path: relative, Line: lineNumber})
				}
			}
		}
	}
	if procedureDepth != 0 {
		diagnostics = append(diagnostics, Diagnostic{Path: relative, Severity: "error", Rule: "bsl-unbalanced-method", Message: "method declaration is not closed"})
	}
	if tryDepth != 0 {
		diagnostics = append(diagnostics, Diagnostic{Path: relative, Severity: "error", Rule: "bsl-unbalanced-try", Message: "try block is not balanced"})
	}
	return diagnostics, symbols, calls
}

func bslKeyword(value string) bool {
	switch strings.ToLower(value) {
	case "если", "иначеесли", "для", "пока", "новый", "возврат", "if", "for", "while", "return", "new":
		return true
	default:
		return false
	}
}

type QueryAnalysis struct {
	Valid       bool         `json:"valid"`
	Diagnostics []Diagnostic `json:"diagnostics"`
	Suggestions []string     `json:"suggestions"`
}

func AnalyzeQuery(text string) QueryAnalysis {
	trimmed := strings.TrimSpace(text)
	result := QueryAnalysis{Valid: trimmed != ""}
	upper := strings.ToUpper(trimmed)
	if trimmed == "" {
		result.Diagnostics = append(result.Diagnostics, Diagnostic{Severity: "error", Rule: "query-empty", Message: "query text is empty"})
		return result
	}
	if !strings.HasPrefix(upper, "ВЫБРАТЬ") && !strings.HasPrefix(upper, "SELECT") {
		result.Valid = false
		result.Diagnostics = append(result.Diagnostics, Diagnostic{Severity: "error", Rule: "query-read-only", Message: "only SELECT/ВЫБРАТЬ queries are supported"})
	}
	if strings.Contains(upper, "ВЫБРАТЬ *") || strings.Contains(upper, "SELECT *") {
		result.Suggestions = append(result.Suggestions, "List required fields explicitly to reduce transfer and schema coupling.")
	}
	if strings.Contains(upper, "ПОДОБНО \"%") || strings.Contains(upper, "LIKE \"%") {
		result.Suggestions = append(result.Suggestions, "A leading wildcard usually prevents index use.")
	}
	if strings.Contains(upper, "ЛЕВОЕ СОЕДИНЕНИЕ") || strings.Contains(upper, "LEFT JOIN") {
		result.Suggestions = append(result.Suggestions, "Verify that joined fields are selective and indexed; remove unused joins.")
	}
	if !strings.Contains(upper, "ГДЕ") && !strings.Contains(upper, "WHERE") {
		result.Suggestions = append(result.Suggestions, "The query has no WHERE/ГДЕ filter; verify the expected data volume.")
	}
	return result
}

type SemanticResult struct {
	Path  string  `json:"path"`
	Score float64 `json:"score"`
}

func SemanticSearch(index *dump.Index, query string, limit int) []SemanticResult {
	if index == nil || strings.TrimSpace(query) == "" {
		return nil
	}
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	queryVector := embedding(query)
	var result []SemanticResult
	for _, document := range index.Documents() {
		score := cosine(queryVector, embedding(document.Text))
		if score > 0 {
			result = append(result, SemanticResult{Path: document.Path, Score: math.Round(score*10000) / 10000})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Score > result[j].Score })
	if len(result) > limit {
		result = result[:limit]
	}
	return result
}

func embedding(text string) []float64 {
	const dimensions = 256
	vector := make([]float64, dimensions)
	for _, term := range words(text) {
		hash := fnv.New32a()
		_, _ = hash.Write([]byte(term))
		index := int(hash.Sum32() % dimensions)
		vector[index]++
	}
	return vector
}

func words(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' })
}

func cosine(left, right []float64) float64 {
	var dot, leftNorm, rightNorm float64
	for index := range left {
		dot += left[index] * right[index]
		leftNorm += left[index] * left[index]
		rightNorm += right[index] * right[index]
	}
	if leftNorm == 0 || rightNorm == 0 {
		return 0
	}
	return dot / (math.Sqrt(leftNorm) * math.Sqrt(rightNorm))
}

func CallGraph(report Report, symbol string) []Call {
	if strings.TrimSpace(symbol) == "" {
		return report.Calls
	}
	var result []Call
	for _, call := range report.Calls {
		if strings.EqualFold(call.Caller, symbol) || strings.EqualFold(call.Callee, symbol) {
			result = append(result, call)
		}
	}
	return result
}

func ArchitectureMermaid(report Report) string {
	var builder strings.Builder
	builder.WriteString("flowchart LR\n")
	seen := map[string]bool{}
	for _, call := range report.Calls {
		key := call.Caller + "\x00" + call.Callee
		if seen[key] || call.Caller == "<module>" {
			continue
		}
		seen[key] = true
		builder.WriteString(fmt.Sprintf("  %s[\"%s\"] --> %s[\"%s\"]\n", mermaidID(call.Caller), escapeMermaid(call.Caller), mermaidID(call.Callee), escapeMermaid(call.Callee)))
		if len(seen) >= 500 {
			break
		}
	}
	return builder.String()
}

func Documentation(report Report) string {
	var builder strings.Builder
	builder.WriteString("# Документация конфигурации\n\n")
	builder.WriteString(fmt.Sprintf("Проанализировано файлов: %d. Найдено методов: %d. Диагностик: %d.\n\n", report.Files, len(report.Symbols), len(report.Diagnostics)))
	byPath := map[string][]Symbol{}
	for _, symbol := range report.Symbols {
		byPath[symbol.Path] = append(byPath[symbol.Path], symbol)
	}
	var paths []string
	for path := range byPath {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		builder.WriteString("## " + path + "\n\n")
		for _, symbol := range byPath[path] {
			builder.WriteString(fmt.Sprintf("- `%s` (%s, строка %d)\n", symbol.Name, symbol.Kind, symbol.Line))
		}
		builder.WriteByte('\n')
	}
	return builder.String()
}

type Difference struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

func CompareDirectories(left, right string) ([]Difference, error) {
	leftFiles, err := fileHashes(left)
	if err != nil {
		return nil, err
	}
	rightFiles, err := fileHashes(right)
	if err != nil {
		return nil, err
	}
	paths := map[string]bool{}
	for path := range leftFiles {
		paths[path] = true
	}
	for path := range rightFiles {
		paths[path] = true
	}
	var result []Difference
	for path := range paths {
		leftHash, inLeft := leftFiles[path]
		rightHash, inRight := rightFiles[path]
		status := "modified"
		if !inLeft {
			status = "added"
		} else if !inRight {
			status = "removed"
		} else if leftHash == rightHash {
			continue
		}
		result = append(result, Difference{Path: path, Status: status})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, nil
}

func APICompatibility(current, baseline Report) map[string]any {
	available := map[string]bool{}
	for _, symbol := range current.Symbols {
		available[strings.ToLower(symbol.Path+"\x00"+symbol.Name)] = true
	}
	var removed []Symbol
	for _, symbol := range baseline.Symbols {
		if !available[strings.ToLower(symbol.Path+"\x00"+symbol.Name)] {
			removed = append(removed, symbol)
		}
	}
	return map[string]any{"compatible": len(removed) == 0, "removed": removed, "count": len(removed)}
}

func fileHashes(root string) (map[string]string, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	result := map[string]string{}
	err = filepath.WalkDir(absolute, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		relative, _ := filepath.Rel(absolute, path)
		result[filepath.ToSlash(relative)] = hex.EncodeToString(sum[:])
		return nil
	})
	return result, err
}

func mermaidID(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "n" + hex.EncodeToString(sum[:6])
}

func escapeMermaid(value string) string {
	return strings.ReplaceAll(value, "\"", "'")
}
