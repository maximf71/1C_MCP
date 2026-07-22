package configmerge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"mcp-1c-analog/internal/designer"
)

type Manager struct {
	SourceRoot string
	WorkDir    string
	Designer   *designer.Client
	mu         sync.Mutex
	last       map[string]any
}

type Difference struct {
	Path         string `json:"path"`
	FQN          string `json:"fqn,omitempty"`
	Scope        string `json:"scope"`
	MainHash     string `json:"main_hash,omitempty"`
	OtherHash    string `json:"other_hash,omitempty"`
	AncestorHash string `json:"ancestor_hash,omitempty"`
	Binary       bool   `json:"binary"`
}
type Plan struct {
	ID               string       `json:"plan_id"`
	MainRoot         string       `json:"main_root"`
	OtherRoot        string       `json:"other_root"`
	AncestorRoot     string       `json:"ancestor_root,omitempty"`
	CreatedAt        time.Time    `json:"created_at"`
	MainFingerprint  string       `json:"main_fingerprint"`
	OtherFingerprint string       `json:"other_fingerprint"`
	Differences      []Difference `json:"differences"`
}
type Rule struct {
	Path string `json:"path,omitempty"`
	FQN  string `json:"fqn,omitempty"`
	Rule string `json:"rule"`
}

func (m *Manager) Validate() error {
	if m.SourceRoot == "" || m.WorkDir == "" {
		return errors.New("configuration update requires fixed source root and work directory")
	}
	if info, err := os.Stat(m.SourceRoot); err != nil || !info.IsDir() {
		return errors.New("fixed configuration source root is unavailable")
	}
	return os.MkdirAll(filepath.Join(m.WorkDir, "configuration-update"), 0o700)
}

func (m *Manager) PrepareSource(ctx context.Context, source string) (map[string]any, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	path, err := below(m.SourceRoot, source)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	prepared := sourceTree(path)
	if !info.IsDir() {
		if !strings.EqualFold(filepath.Ext(path), ".cf") {
			return nil, errors.New("source must be a directory, EDT project or full .cf file")
		}
		if m.Designer == nil {
			return nil, errors.New("preparing .cf requires configured Designer (--platform and --infobase)")
		}
		hash, err := fileHash(path)
		if err != nil {
			return nil, err
		}
		prepared = filepath.Join(m.WorkDir, "configuration-update", "prepared", hash)
		if _, err := os.Stat(prepared); os.IsNotExist(err) {
			base := filepath.Join(m.WorkDir, "configuration-update", "bases", hash)
			if err := m.Designer.CreateInfobase(ctx, base); err != nil {
				return nil, err
			}
			client := m.Designer.ForInfobase(base)
			if err := client.LoadCfg(ctx, path); err != nil {
				return nil, err
			}
			if err := client.DumpConfig(ctx, prepared); err != nil {
				return nil, err
			}
		}
	}
	fingerprint, _, err := scan(prepared)
	if err != nil {
		return nil, err
	}
	m.setLast(map[string]any{"operation": "prepareSource", "state": "completed", "source": source, "prepared_root": prepared, "fingerprint": fingerprint})
	return m.Status(), nil
}

func (m *Manager) Compare(mainRoot, source, ancestor string) (Plan, error) {
	if err := m.Validate(); err != nil {
		return Plan{}, err
	}
	main := sourceTree(mainRoot)
	otherPath, err := below(m.SourceRoot, source)
	if err != nil {
		return Plan{}, err
	}
	other := sourceTree(otherPath)
	if strings.EqualFold(filepath.Ext(otherPath), ".cf") {
		hash, hashErr := fileHash(otherPath)
		if hashErr != nil {
			return Plan{}, hashErr
		}
		other = filepath.Join(m.WorkDir, "configuration-update", "prepared", hash)
	}
	ancestorRoot := ""
	if strings.TrimSpace(ancestor) != "" {
		a, err := below(m.SourceRoot, ancestor)
		if err != nil {
			return Plan{}, err
		}
		ancestorRoot = sourceTree(a)
	}
	mainFP, mainFiles, err := scan(main)
	if err != nil {
		return Plan{}, fmt.Errorf("scan main project: %w", err)
	}
	otherFP, otherFiles, err := scan(other)
	if err != nil {
		return Plan{}, fmt.Errorf("scan source: %w", err)
	}
	ancestorFiles := map[string]fileInfo{}
	if ancestorRoot != "" {
		_, ancestorFiles, err = scan(ancestorRoot)
		if err != nil {
			return Plan{}, fmt.Errorf("scan ancestor: %w", err)
		}
	}
	keys := map[string]bool{}
	for key := range mainFiles {
		keys[key] = true
	}
	for key := range otherFiles {
		keys[key] = true
	}
	paths := make([]string, 0, len(keys))
	for key := range keys {
		paths = append(paths, key)
	}
	sort.Strings(paths)
	var differences []Difference
	for _, path := range paths {
		left, lok := mainFiles[path]
		right, rok := otherFiles[path]
		if lok && rok && left.Hash == right.Hash {
			continue
		}
		d := Difference{Path: path, FQN: pathToFQN(path)}
		if lok {
			d.MainHash = left.Hash
			d.Binary = left.Binary
		}
		if rok {
			d.OtherHash = right.Hash
			d.Binary = d.Binary || right.Binary
		}
		if a, ok := ancestorFiles[path]; ok {
			d.AncestorHash = a.Hash
		}
		switch {
		case lok && !rok:
			d.Scope = "onlyInMain"
		case !lok && rok:
			d.Scope = "onlyInOther"
		default:
			d.Scope = "changed"
		}
		differences = append(differences, d)
	}
	id := planID(mainFP, otherFP, ancestorRoot)
	plan := Plan{ID: id, MainRoot: main, OtherRoot: other, AncestorRoot: ancestorRoot, CreatedAt: time.Now().UTC(), MainFingerprint: mainFP, OtherFingerprint: otherFP, Differences: differences}
	if err := m.savePlan(plan); err != nil {
		return Plan{}, err
	}
	m.setLast(map[string]any{"operation": "compare", "state": "completed", "plan_id": id, "differences": len(differences)})
	return plan, nil
}

func (m *Manager) Differences(id, scope string, offset, limit int) (map[string]any, error) {
	plan, err := m.loadPlan(id)
	if err != nil {
		return nil, err
	}
	var selected []Difference
	for _, d := range plan.Differences {
		if scope == "" || d.Scope == scope {
			selected = append(selected, d)
		}
	}
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	if offset > len(selected) {
		offset = len(selected)
	}
	end := offset + limit
	if end > len(selected) {
		end = len(selected)
	}
	return map[string]any{"plan_id": id, "total": len(selected), "offset": offset, "returned": end - offset, "differences": selected[offset:end]}, nil
}

func (m *Manager) Merge(id string, rules []Rule, acceptAll, dryRun, confirm bool) (map[string]any, error) {
	plan, err := m.loadPlan(id)
	if err != nil {
		return nil, err
	}
	current, _, err := scan(plan.MainRoot)
	if err != nil {
		return nil, err
	}
	if current != plan.MainFingerprint {
		return nil, errors.New("main project changed after comparison; create a new plan")
	}
	ruleMap := map[string]string{}
	for _, rule := range rules {
		key := strings.TrimSpace(rule.Path)
		if key == "" {
			for _, d := range plan.Differences {
				if d.FQN == rule.FQN {
					key = d.Path
					break
				}
			}
		}
		if key == "" {
			return nil, errors.New("each merge rule requires a known path or FQN")
		}
		normalized, err := normalizeRule(rule.Rule)
		if err != nil {
			return nil, err
		}
		ruleMap[filepath.ToSlash(key)] = normalized
	}
	var actions []map[string]any
	for _, d := range plan.Differences {
		rule := ruleMap[d.Path]
		if acceptAll {
			rule = "takeOther"
		}
		if rule == "" {
			rule = "keepMain"
		}
		actions = append(actions, map[string]any{"path": d.Path, "scope": d.Scope, "rule": rule})
	}
	if dryRun || !confirm {
		return map[string]any{"dry_run": true, "plan_id": id, "actions": actions, "changed": false}, nil
	}
	changed := 0
	for _, action := range actions {
		if action["rule"].(string) != "keepMain" {
			changed++
		}
	}
	if changed == 0 {
		return map[string]any{"dry_run": false, "changed": false, "plan_id": id, "actions": actions}, nil
	}
	snapshot := filepath.Join(m.WorkDir, "configuration-update", "snapshots", id+"-"+time.Now().UTC().Format("20060102T150405"))
	if err := copyTree(plan.MainRoot, snapshot); err != nil {
		return nil, err
	}
	for _, action := range actions {
		path := action["path"].(string)
		rule := action["rule"].(string)
		if rule == "keepMain" {
			continue
		}
		target := filepath.Join(plan.MainRoot, filepath.FromSlash(path))
		source := filepath.Join(plan.OtherRoot, filepath.FromSlash(path))
		scope := action["scope"].(string)
		if scope == "onlyInMain" {
			if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
				return nil, err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return nil, err
		}
		if rule == "mergeOther" || rule == "mergeMain" {
			merged, err := m.mergeText(plan, path, rule)
			if err != nil {
				return nil, err
			}
			if err := os.WriteFile(target, merged, 0o600); err != nil {
				return nil, err
			}
			continue
		}
		if err := copyFile(source, target); err != nil {
			return nil, err
		}
	}
	m.setLast(map[string]any{"operation": "merge", "state": "completed", "plan_id": id, "snapshot": snapshot})
	return map[string]any{"dry_run": false, "changed": true, "plan_id": id, "actions": actions, "snapshot": snapshot,
		"merge_semantics": "three-way only when ancestor is configured and one side is unchanged; otherwise the selected priority side wins"}, nil
}

func (m *Manager) ExportDifferences(id string) (map[string]any, error) {
	plan, err := m.loadPlan(id)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(m.WorkDir, "configuration-update", "exports", id+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	data, _ := json.MarshalIndent(plan, "", "  ")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return nil, err
	}
	return map[string]any{"plan_id": id, "path": path, "differences": len(plan.Differences)}, nil
}
func (m *Manager) Status() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.last == nil {
		return map[string]any{"state": "idle"}
	}
	result := map[string]any{}
	for k, v := range m.last {
		result[k] = v
	}
	return result
}
func (m *Manager) Cancel() map[string]any {
	return map[string]any{"state": "idle", "cancelled": false, "message": "operations are synchronous; no background operation is running"}
}
func (m *Manager) Cleanup() (map[string]any, error) {
	removed := 0
	updateRoot := filepath.Join(m.WorkDir, "configuration-update")
	for _, category := range []string{"prepared", "bases"} {
		root := filepath.Join(updateRoot, category)
		entries, err := os.ReadDir(root)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			target := filepath.Join(root, entry.Name())
			rel, _ := filepath.Rel(updateRoot, target)
			if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return nil, errors.New("cleanup target escaped work directory")
			}
			if err := os.RemoveAll(target); err != nil {
				return nil, err
			}
			removed++
		}
	}
	return map[string]any{"removed": removed}, nil
}

type fileInfo struct {
	Hash   string
	Binary bool
}

func scan(root string) (string, map[string]fileInfo, error) {
	files := map[string]fileInfo{}
	err := filepath.WalkDir(root, func(path string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		files[filepath.ToSlash(rel)] = fileInfo{Hash: hex.EncodeToString(sum[:]), Binary: containsZero(data)}
		return nil
	})
	if err != nil {
		return "", nil, err
	}
	keys := make([]string, 0, len(files))
	for key := range files {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	hash := sha256.New()
	for _, key := range keys {
		io.WriteString(hash, key+"\x00"+files[key].Hash+"\n")
	}
	return hex.EncodeToString(hash.Sum(nil)), files, nil
}
func (m *Manager) savePlan(plan Plan) error {
	path := filepath.Join(m.WorkDir, "configuration-update", "plans", plan.ID+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(plan, "", "  ")
	return os.WriteFile(path, data, 0o600)
}
func (m *Manager) loadPlan(id string) (Plan, error) {
	if id == "" || strings.ContainsAny(id, "/\\.") {
		return Plan{}, errors.New("valid plan_id is required")
	}
	data, err := os.ReadFile(filepath.Join(m.WorkDir, "configuration-update", "plans", id+".json"))
	if err != nil {
		return Plan{}, err
	}
	var plan Plan
	if err = json.Unmarshal(data, &plan); err != nil {
		return Plan{}, err
	}
	return plan, nil
}
func (m *Manager) setLast(value map[string]any) { m.mu.Lock(); defer m.mu.Unlock(); m.last = value }
func (m *Manager) mergeText(plan Plan, path, priority string) ([]byte, error) {
	main, err := os.ReadFile(filepath.Join(plan.MainRoot, filepath.FromSlash(path)))
	if err != nil {
		return nil, err
	}
	other, err := os.ReadFile(filepath.Join(plan.OtherRoot, filepath.FromSlash(path)))
	if err != nil {
		return nil, err
	}
	if plan.AncestorRoot != "" {
		ancestor, _ := os.ReadFile(filepath.Join(plan.AncestorRoot, filepath.FromSlash(path)))
		if string(main) == string(ancestor) {
			return other, nil
		}
		if string(other) == string(ancestor) {
			return main, nil
		}
	}
	if priority == "mergeOther" {
		return other, nil
	}
	return main, nil
}
func normalizeRule(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "взятьизновой", "takeother":
		return "takeOther", nil
	case "оставитьсвою", "keepmain":
		return "keepMain", nil
	case "объединитьприоритетновой", "mergeother":
		return "mergeOther", nil
	case "объединитьприоритетсвоей", "mergemain":
		return "mergeMain", nil
	default:
		return "", fmt.Errorf("unknown merge rule %q", value)
	}
}
func sourceTree(root string) string {
	if info, err := os.Stat(filepath.Join(root, "src")); err == nil && info.IsDir() {
		return filepath.Join(root, "src")
	}
	return root
}
func below(root, relative string) (string, error) {
	if filepath.IsAbs(relative) {
		return "", errors.New("source must be relative to the fixed source root")
	}
	clean := filepath.Clean(relative)
	if clean == "." || clean == "" || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("source escapes the fixed source root")
	}
	target := filepath.Join(root, clean)
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("source escapes the fixed source root")
	}
	return target, nil
}
func fileHash(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err = io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
func planID(parts ...string) string {
	hash := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(hash[:12])
}
func containsZero(data []byte) bool {
	limit := len(data)
	if limit > 8192 {
		limit = 8192
	}
	for _, b := range data[:limit] {
		if b == 0 {
			return true
		}
	}
	return false
}
func copyFile(source, target string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.Create(target)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
func copyTree(source, target string) error {
	return filepath.WalkDir(source, func(path string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(source, path)
		destination := filepath.Join(target, rel)
		if e.IsDir() {
			return os.MkdirAll(destination, 0o700)
		}
		return copyFile(path, destination)
	})
}
func pathToFQN(path string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	if len(parts) < 2 {
		return ""
	}
	types := map[string]string{"Catalogs": "Catalog", "Documents": "Document", "CommonModules": "CommonModule", "Reports": "Report", "DataProcessors": "DataProcessor", "Subsystems": "Subsystem", "Roles": "Role", "InformationRegisters": "InformationRegister", "AccumulationRegisters": "AccumulationRegister"}
	kind := types[parts[0]]
	if kind == "" {
		return ""
	}
	name := strings.TrimSuffix(parts[1], filepath.Ext(parts[1]))
	return kind + "." + name
}
