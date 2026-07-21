package dump

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

const cacheVersion = 2

type Index struct {
	dir  string
	docs []doc
	df   map[string]int
}

type doc struct {
	Path       string         `json:"path"`
	Text       string         `json:"text"`
	Terms      map[string]int `json:"terms"`
	Length     int            `json:"length"`
	ObjectType string         `json:"object_type,omitempty"`
	Module     string         `json:"module,omitempty"`
	Category   string         `json:"category,omitempty"`
	Size       int64          `json:"size"`
	ModTime    int64          `json:"mod_time"`
}

type cacheFile struct {
	Version int    `json:"version"`
	Root    string `json:"root"`
	Docs    []doc  `json:"docs"`
}

type Result struct {
	Path       string  `json:"path"`
	Score      float64 `json:"score"`
	Snippet    string  `json:"snippet"`
	Line       int     `json:"line,omitempty"`
	ObjectType string  `json:"object_type,omitempty"`
	Module     string  `json:"module,omitempty"`
	Category   string  `json:"category,omitempty"`
}

type SearchOptions struct {
	Query      string
	Limit      int
	Mode       string
	ObjectType string
	Module     string
	Category   string
}

type DocumentInfo struct {
	Path       string `json:"path"`
	Text       string `json:"-"`
	ObjectType string `json:"object_type,omitempty"`
	Module     string `json:"module,omitempty"`
	Category   string `json:"category,omitempty"`
}

func (i *Index) Documents() []DocumentInfo {
	if i == nil {
		return nil
	}
	result := make([]DocumentInfo, 0, len(i.docs))
	for _, candidate := range i.docs {
		result = append(result, DocumentInfo{Path: candidate.Path, Text: candidate.Text, ObjectType: candidate.ObjectType, Module: candidate.Module, Category: candidate.Category})
	}
	return result
}

func Open(dir, cacheDir string, reindex bool) (*Index, error) {
	root, err := secureRoot(dir)
	if err != nil {
		return nil, err
	}
	cachePath := ""
	if strings.TrimSpace(cacheDir) != "" {
		cachePath = indexCachePath(cacheDir, root)
	}
	if !reindex && cachePath != "" {
		if cached, err := loadCache(cachePath, root); err == nil {
			if fresh, err := cacheFresh(root, cached.docs); err == nil && fresh {
				return cached, nil
			}
		}
	}
	paths, err := collectPaths(root)
	if err != nil {
		return nil, err
	}
	docs := scanDocuments(root, paths)
	idx := buildIndex(root, docs)
	if cachePath != "" {
		if err := saveCache(cachePath, idx); err != nil {
			return nil, fmt.Errorf("save search cache: %w", err)
		}
	}
	return idx, nil
}

func (i *Index) Search(query string, limit int) []Result {
	results, _ := i.SearchAdvanced(SearchOptions{Query: query, Limit: limit, Mode: "smart"})
	return results
}

func (i *Index) SearchAdvanced(options SearchOptions) ([]Result, error) {
	if i == nil || strings.TrimSpace(options.Query) == "" {
		return nil, nil
	}
	if options.Limit <= 0 || options.Limit > 200 {
		options.Limit = 10
	}
	options.Mode = strings.ToLower(strings.TrimSpace(options.Mode))
	if options.Mode == "" {
		options.Mode = "smart"
	}
	if options.Mode != "smart" && options.Mode != "exact" && options.Mode != "regex" {
		return nil, errors.New("search mode must be smart, exact or regex")
	}
	var expression *regexp.Regexp
	var err error
	if options.Mode == "regex" {
		expression, err = regexp.Compile(options.Query)
		if err != nil {
			return nil, fmt.Errorf("invalid regular expression: %w", err)
		}
	}
	queryTerms := frequencies(expandSynonyms(tokenize(options.Query)))
	avgLen := i.avgDocLen()
	var results []Result
	for _, candidate := range i.docs {
		if !matchesFilter(candidate.ObjectType, options.ObjectType) ||
			!matchesFilter(candidate.Module, options.Module) ||
			!matchesFilter(candidate.Category, options.Category) {
			continue
		}
		score, line, excerpt := matchDocument(candidate, options, expression, queryTerms, i.df, len(i.docs), avgLen)
		if score <= 0 {
			continue
		}
		results = append(results, Result{
			Path: candidate.Path, Score: math.Round(score*1000) / 1000,
			Snippet: excerpt, Line: line, ObjectType: candidate.ObjectType,
			Module: candidate.Module, Category: candidate.Category,
		})
	}
	sort.Slice(results, func(a, b int) bool {
		if results[a].Score == results[b].Score {
			return results[a].Path < results[b].Path
		}
		return results[a].Score > results[b].Score
	})
	if len(results) > options.Limit {
		results = results[:options.Limit]
	}
	return results, nil
}

func matchDocument(candidate doc, options SearchOptions, expression *regexp.Regexp, query map[string]int, df map[string]int, count int, avg float64) (float64, int, string) {
	switch options.Mode {
	case "exact":
		line, excerpt := findLine(candidate.Text, func(line string) bool {
			return strings.Contains(strings.ToLower(line), strings.ToLower(options.Query))
		})
		if line == 0 {
			return 0, 0, ""
		}
		return 1000, line, excerpt
	case "regex":
		line, excerpt := findLine(candidate.Text, expression.MatchString)
		if line == 0 {
			return 0, 0, ""
		}
		return 1000, line, excerpt
	default:
		score := bm25(candidate, query, df, count, avg)
		if score <= 0 {
			return 0, 0, ""
		}
		line, excerpt := findLine(candidate.Text, func(line string) bool {
			lower := strings.ToLower(line)
			for term := range query {
				if strings.Contains(lower, term) {
					return true
				}
			}
			return false
		})
		if line == 0 {
			excerpt = trimRunes(strings.TrimSpace(candidate.Text), 220)
		}
		return score, line, excerpt
	}
}

func (i *Index) Count() int {
	if i == nil {
		return 0
	}
	return len(i.docs)
}

func (i *Index) avgDocLen() float64 {
	if len(i.docs) == 0 {
		return 1
	}
	total := 0
	for _, candidate := range i.docs {
		total += candidate.Length
	}
	return float64(total) / float64(len(i.docs))
}

func bm25(candidate doc, query map[string]int, df map[string]int, docCount int, avgLen float64) float64 {
	const k1 = 1.5
	const b = 0.75
	var score float64
	for term := range query {
		tf := candidate.Terms[term]
		if tf == 0 {
			continue
		}
		idf := math.Log(1 + (float64(docCount-df[term])+0.5)/(float64(df[term])+0.5))
		norm := float64(tf) + k1*(1-b+b*float64(candidate.Length)/avgLen)
		score += idf * (float64(tf) * (k1 + 1) / norm)
	}
	return score
}

func secureRoot(dir string) (string, error) {
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", absolute)
	}
	real, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	return filepath.Clean(real), nil
}

func collectPaths(root string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() || !entry.Type().IsRegular() || !isIndexable(path) {
			return nil
		}
		if _, err := containedPath(root, path); err != nil {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	sort.Strings(paths)
	return paths, err
}

func containedPath(root, path string) (string, error) {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(root, real)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) || filepath.IsAbs(relative) {
		return "", errors.New("path escapes configured dump root")
	}
	return real, nil
}

func scanDocuments(root string, paths []string) []doc {
	workers := runtime.GOMAXPROCS(0)
	if workers < 1 {
		workers = 1
	}
	if workers > 8 {
		workers = 8
	}
	jobs := make(chan string)
	results := make(chan doc)
	var group sync.WaitGroup
	for index := 0; index < workers; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for path := range jobs {
				if value, ok := scanDocument(root, path); ok {
					results <- value
				}
			}
		}()
	}
	go func() {
		for _, path := range paths {
			jobs <- path
		}
		close(jobs)
		group.Wait()
		close(results)
	}()
	var docs []doc
	for value := range results {
		docs = append(docs, value)
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].Path < docs[j].Path })
	return docs
}

func scanDocument(root, path string) (doc, bool) {
	secure, err := containedPath(root, path)
	if err != nil {
		return doc{}, false
	}
	info, err := os.Stat(secure)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 32<<20 {
		return doc{}, false
	}
	text, err := readText(secure)
	if err != nil {
		return doc{}, false
	}
	terms := frequencies(expandSynonyms(tokenize(text)))
	if len(terms) == 0 {
		return doc{}, false
	}
	relative, err := filepath.Rel(root, secure)
	if err != nil {
		return doc{}, false
	}
	objectType, module, category := classify(relative)
	return doc{Path: filepath.ToSlash(relative), Text: text, Terms: terms, Length: sum(terms),
		ObjectType: objectType, Module: module, Category: category, Size: info.Size(), ModTime: info.ModTime().UnixNano()}, true
}

func buildIndex(root string, docs []doc) *Index {
	index := &Index{dir: root, docs: docs, df: map[string]int{}}
	for _, candidate := range docs {
		for term := range candidate.Terms {
			index.df[term]++
		}
	}
	return index
}

func classify(path string) (string, string, string) {
	normalized := filepath.ToSlash(path)
	parts := strings.Split(normalized, "/")
	objectType := "Configuration"
	if len(parts) > 1 {
		objectType = strings.TrimSuffix(parts[0], "s")
	}
	module := ""
	category := "metadata"
	if strings.EqualFold(filepath.Ext(normalized), ".bsl") {
		module = strings.TrimSuffix(filepath.Base(normalized), filepath.Ext(normalized))
		category = "module"
	} else if strings.Contains(strings.ToLower(normalized), "/forms/") {
		category = "form"
	} else if strings.Contains(strings.ToLower(normalized), "/templates/") {
		category = "template"
	}
	return objectType, module, category
}

func cacheFresh(root string, docs []doc) (bool, error) {
	paths, err := collectPaths(root)
	if err != nil || len(paths) != len(docs) {
		return false, err
	}
	byPath := make(map[string]doc, len(docs))
	for _, candidate := range docs {
		byPath[candidate.Path] = candidate
	}
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return false, err
		}
		relative, _ := filepath.Rel(root, path)
		candidate, ok := byPath[filepath.ToSlash(relative)]
		if !ok || candidate.Size != info.Size() || candidate.ModTime != info.ModTime().UnixNano() {
			return false, nil
		}
	}
	return true, nil
}

func indexCachePath(cacheDir, root string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(root)))
	return filepath.Join(cacheDir, "dump-index-"+hex.EncodeToString(sum[:8])+".json")
}

func loadCache(path, root string) (*Index, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cached cacheFile
	if err := json.Unmarshal(data, &cached); err != nil {
		return nil, err
	}
	if cached.Version != cacheVersion || !strings.EqualFold(filepath.Clean(cached.Root), root) {
		return nil, errors.New("stale search cache")
	}
	return buildIndex(root, cached.Docs), nil
}

func saveCache(path string, index *Index) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(cacheFile{Version: cacheVersion, Root: index.dir, Docs: index.docs})
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".dump-index-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func isIndexable(path string) bool {
	extension := strings.ToLower(filepath.Ext(path))
	return extension == ".bsl" || extension == ".xml"
}

func readText(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(data) {
		data = bytesWithoutNUL(data)
	}
	return string(data), nil
}

func bytesWithoutNUL(data []byte) []byte {
	output := make([]byte, 0, len(data))
	for _, value := range data {
		if value != 0 {
			output = append(output, value)
		}
	}
	return output
}

func tokenize(value string) []string {
	var terms []string
	var builder strings.Builder
	flush := func() {
		if builder.Len() > 1 {
			terms = append(terms, strings.ToLower(builder.String()))
		}
		builder.Reset()
	}
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			builder.WriteRune(unicode.ToLower(r))
		} else {
			flush()
		}
	}
	flush()
	return terms
}

var bslSynonyms = map[string][]string{
	"procedure": {"процедура"}, "процедура": {"procedure"},
	"function": {"функция"}, "функция": {"function"},
	"query": {"запрос"}, "запрос": {"query"},
	"select": {"выбрать"}, "выбрать": {"select"},
	"catalog": {"справочник"}, "справочник": {"catalog"},
	"document": {"документ"}, "документ": {"document"},
	"form": {"форма"}, "форма": {"form"},
}

func expandSynonyms(terms []string) []string {
	result := append([]string(nil), terms...)
	for _, term := range terms {
		result = append(result, bslSynonyms[term]...)
	}
	return result
}

func frequencies(terms []string) map[string]int {
	result := map[string]int{}
	for _, term := range terms {
		result[term]++
	}
	return result
}

func sum(values map[string]int) int {
	total := 0
	for _, value := range values {
		total += value
	}
	return total
}

func findLine(text string, matches func(string) bool) (int, string) {
	scanner := bufio.NewScanner(strings.NewReader(text))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		value := strings.TrimSpace(scanner.Text())
		if matches(value) {
			return line, trimRunes(value, 220)
		}
	}
	return 0, ""
}

func trimRunes(value string, maximum int) string {
	runes := []rune(value)
	if len(runes) <= maximum {
		return value
	}
	return string(runes[:maximum]) + "..."
}

func matchesFilter(value, filter string) bool {
	return strings.TrimSpace(filter) == "" || strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(filter))
}
