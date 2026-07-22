package techlog

import (
	"bufio"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const maxLineBytes = 4 << 20

type Manager struct {
	ConfigPath string
	LogRoot    string
	WorkDir    string
}

type Event struct {
	File       string            `json:"file"`
	Line       int               `json:"line"`
	Timestamp  string            `json:"timestamp"`
	Name       string            `json:"name"`
	DurationMS float64           `json:"duration_ms"`
	Properties map[string]string `json:"properties,omitempty"`
}

type Analysis struct {
	Files        int            `json:"files"`
	Events       int            `json:"events"`
	Returned     int            `json:"returned"`
	TotalMS      float64        `json:"total_duration_ms"`
	ByEvent      map[string]int `json:"by_event"`
	Longest      []Event        `json:"longest"`
	ParseErrors  int            `json:"parse_errors"`
	MinimumMS    float64        `json:"minimum_duration_ms"`
	ModuleFilter string         `json:"module_filter,omitempty"`
}

func (m *Manager) Status() map[string]any {
	_, configErr := os.Stat(m.ConfigPath)
	_, rootErr := os.Stat(m.LogRoot)
	return map[string]any{
		"configured": m.ConfigPath != "" && m.LogRoot != "", "enabled": configErr == nil,
		"config_path": m.ConfigPath, "log_root": m.LogRoot, "log_root_exists": rootErr == nil,
	}
}

func (m *Manager) Validate() error {
	if strings.TrimSpace(m.ConfigPath) == "" || strings.TrimSpace(m.LogRoot) == "" || strings.TrimSpace(m.WorkDir) == "" {
		return errors.New("technological log requires fixed config path, log root and work directory")
	}
	if filepath.Ext(m.ConfigPath) != ".xml" {
		return errors.New("technological log config must be an XML file")
	}
	return nil
}

func (m *Manager) Enable(preset string, minimumMS float64, historyHours int) (map[string]any, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	if historyHours <= 0 || historyHours > 168 {
		historyHours = 24
	}
	if minimumMS < 0 {
		return nil, errors.New("minimum duration must not be negative")
	}
	events, err := presetEvents(preset)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(m.ConfigPath), 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(m.LogRoot, 0o700); err != nil {
		return nil, err
	}
	backup := filepath.Join(m.WorkDir, "techlog", "previous-logcfg.xml")
	if data, readErr := os.ReadFile(m.ConfigPath); readErr == nil {
		if err := os.MkdirAll(filepath.Dir(backup), 0o700); err != nil {
			return nil, err
		}
		if _, statErr := os.Stat(backup); os.IsNotExist(statErr) {
			if err := os.WriteFile(backup, data, 0o600); err != nil {
				return nil, err
			}
		}
	} else if !os.IsNotExist(readErr) {
		return nil, readErr
	}
	data, err := renderConfig(m.LogRoot, events, minimumMS, historyHours)
	if err != nil {
		return nil, err
	}
	if err := writeAtomic(m.ConfigPath, data); err != nil {
		return nil, err
	}
	return map[string]any{"enabled": true, "preset": preset, "events": events, "minimum_duration_ms": minimumMS, "history_hours": historyHours, "restart_may_be_required": true}, nil
}

func (m *Manager) Disable() (map[string]any, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	backup := filepath.Join(m.WorkDir, "techlog", "previous-logcfg.xml")
	if data, err := os.ReadFile(backup); err == nil {
		if err := writeAtomic(m.ConfigPath, data); err != nil {
			return nil, err
		}
		if err := os.Remove(backup); err != nil {
			return nil, err
		}
		return map[string]any{"enabled": false, "restored_previous_config": true, "restart_may_be_required": true}, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if err := os.Remove(m.ConfigPath); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return map[string]any{"enabled": false, "restored_previous_config": false, "restart_may_be_required": true}, nil
}

func (m *Manager) Clear() (map[string]any, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(m.LogRoot)
	if os.IsNotExist(err) {
		return map[string]any{"removed_files": 0}, nil
	}
	if err != nil {
		return nil, err
	}
	removed := 0
	for _, entry := range entries {
		path := filepath.Join(m.LogRoot, entry.Name())
		if entry.IsDir() {
			continue
		}
		if strings.EqualFold(filepath.Ext(entry.Name()), ".log") {
			if err := os.Remove(path); err != nil {
				return nil, err
			}
			removed++
		}
	}
	return map[string]any{"removed_files": removed}, nil
}

func (m *Manager) Analyze(minimumMS float64, moduleFilter string, limit int) (Analysis, error) {
	if err := m.Validate(); err != nil {
		return Analysis{}, err
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 1000 {
		limit = 1000
	}
	result := Analysis{ByEvent: map[string]int{}, MinimumMS: minimumMS, ModuleFilter: moduleFilter}
	err := filepath.WalkDir(m.LogRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(path), ".log") {
			return nil
		}
		result.Files++
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 64*1024), maxLineBytes)
		line := 0
		for scanner.Scan() {
			line++
			event, ok := parseLine(scanner.Text())
			if !ok {
				result.ParseErrors++
				continue
			}
			event.File, _ = filepath.Rel(m.LogRoot, path)
			event.File = filepath.ToSlash(event.File)
			event.Line = line
			if event.DurationMS < minimumMS || (moduleFilter != "" && !containsProperty(event.Properties, moduleFilter)) {
				continue
			}
			result.Events++
			result.TotalMS += event.DurationMS
			result.ByEvent[event.Name]++
			result.Longest = append(result.Longest, event)
		}
		return scanner.Err()
	})
	if os.IsNotExist(err) {
		return result, nil
	}
	if err != nil {
		return Analysis{}, err
	}
	sort.Slice(result.Longest, func(i, j int) bool { return result.Longest[i].DurationMS > result.Longest[j].DurationMS })
	if len(result.Longest) > limit {
		result.Longest = result.Longest[:limit]
	}
	result.Returned = len(result.Longest)
	return result, nil
}

func presetEvents(preset string) ([]string, error) {
	switch strings.ToLower(strings.TrimSpace(preset)) {
	case "", "performance":
		return []string{"CALL", "DBMSSQL", "DBPOSTGRS", "DBORACLE", "SDBL"}, nil
	case "exceptions":
		return []string{"EXCP"}, nil
	case "locks":
		return []string{"TLOCK", "TDEADLOCK", "TTIMEOUT"}, nil
	case "server":
		return []string{"CALL", "SCALL"}, nil
	case "all":
		return []string{"CALL", "DBMSSQL", "DBPOSTGRS", "DBORACLE", "SDBL", "EXCP", "TLOCK", "TDEADLOCK", "TTIMEOUT", "SCALL"}, nil
	default:
		return nil, fmt.Errorf("unknown technological log preset %q", preset)
	}
}

type logConfig struct {
	XMLName xml.Name   `xml:"config"`
	XMLNS   string     `xml:"xmlns,attr"`
	Logs    []logEntry `xml:"log"`
}
type logEntry struct {
	Location   string        `xml:"location,attr"`
	History    int           `xml:"history,attr"`
	Events     []logEvent    `xml:"event"`
	Properties []logProperty `xml:"property"`
}
type logEvent struct {
	Conditions []logCondition `xml:",any"`
}
type logCondition struct {
	XMLName  xml.Name
	Property string `xml:"property,attr"`
	Value    string `xml:"value,attr"`
}
type logProperty struct {
	Name string `xml:"name,attr"`
}

func renderConfig(root string, events []string, minimumMS float64, history int) ([]byte, error) {
	entry := logEntry{Location: root, History: history, Properties: []logProperty{{Name: "all"}}}
	for _, name := range events {
		conditions := []logCondition{{XMLName: xml.Name{Local: "eq"}, Property: "name", Value: name}}
		if minimumMS > 0 {
			conditions = append(conditions, logCondition{XMLName: xml.Name{Local: "gt"}, Property: "duration", Value: strconv.FormatInt(int64(minimumMS*1000), 10)})
		}
		entry.Events = append(entry.Events, logEvent{Conditions: conditions})
	}
	data, err := xml.MarshalIndent(logConfig{XMLNS: "http://v8.1c.ru/v8/tech-log", Logs: []logEntry{entry}}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), data...), nil
}

func parseLine(line string) (Event, bool) {
	parts := splitCSV(line)
	if len(parts) < 3 {
		return Event{}, false
	}
	header := strings.SplitN(parts[0], "-", 2)
	duration := 0.0
	if len(header) == 2 {
		raw, err := strconv.ParseFloat(header[1], 64)
		if err == nil {
			duration = raw / 1000
		}
	}
	e := Event{Timestamp: header[0], Name: strings.TrimSpace(parts[1]), DurationMS: duration, Properties: map[string]string{}}
	for _, item := range parts[3:] {
		pair := strings.SplitN(item, "=", 2)
		if len(pair) == 2 {
			e.Properties[strings.TrimSpace(pair[0])] = strings.Trim(strings.TrimSpace(pair[1]), "'")
		}
	}
	return e, e.Name != ""
}

func splitCSV(value string) []string {
	var result []string
	var b strings.Builder
	quoted := false
	for _, r := range value {
		if r == '\'' {
			quoted = !quoted
		}
		if r == ',' && !quoted {
			result = append(result, b.String())
			b.Reset()
		} else {
			b.WriteRune(r)
		}
	}
	return append(result, b.String())
}

func containsProperty(values map[string]string, needle string) bool {
	needle = strings.ToLower(needle)
	for key, value := range values {
		if strings.Contains(strings.ToLower(key+"="+value), needle) {
			return true
		}
	}
	return false
}

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".logcfg-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(name, path); err == nil {
		return nil
	}
	if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
		return err
	}
	return os.Rename(name, path)
}
