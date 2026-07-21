package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Template struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`
}

var builtins = []Template{
	{Name: "read_only_query", Description: "Read-only 1C query skeleton", Body: "ВЫБРАТЬ\n    {{fields}}\nИЗ\n    {{source}} КАК Источник\nГДЕ\n    {{condition}}"},
	{Name: "bsl_exported_procedure", Description: "Exported BSL procedure skeleton", Body: "Процедура {{name}}({{parameters}}) Экспорт\n\n    {{body}}\n\nКонецПроцедуры"},
	{Name: "external_object_spec", Description: "Specification for a managed EPF/ERF project", Body: "Объект: {{name}}\nТип: {{kind}}\nНазначение: {{purpose}}\nФормы: {{forms}}\nКоманды: {{commands}}"},
}

func Templates() []Template { return append([]Template(nil), builtins...) }

func RenderTemplate(name string, variables map[string]any) (string, error) {
	var selected *Template
	for index := range builtins {
		if builtins[index].Name == name {
			selected = &builtins[index]
			break
		}
	}
	if selected == nil {
		return "", fmt.Errorf("unknown template %q", name)
	}
	result := selected.Body
	for key, value := range variables {
		result = strings.ReplaceAll(result, "{{"+key+"}}", fmt.Sprint(value))
	}
	return result, nil
}

type MemoryEntry struct {
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Memory struct{ Root string }

func (m Memory) List() ([]MemoryEntry, error) {
	values, err := m.read()
	if err != nil {
		return nil, err
	}
	var result []MemoryEntry
	for _, value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Key < result[j].Key })
	return result, nil
}

func (m Memory) Put(key, value string) (MemoryEntry, error) {
	key = strings.TrimSpace(key)
	if key == "" || len(key) > 120 || strings.ContainsAny(key, "\r\n/\\") {
		return MemoryEntry{}, errors.New("memory key must be a single safe line up to 120 characters")
	}
	if len(value) > 64*1024 {
		return MemoryEntry{}, errors.New("memory value is larger than 64 KiB")
	}
	values, err := m.read()
	if err != nil {
		return MemoryEntry{}, err
	}
	entry := MemoryEntry{Key: key, Value: value, UpdatedAt: time.Now().UTC()}
	values[key] = entry
	if err := m.write(values); err != nil {
		return MemoryEntry{}, err
	}
	return entry, nil
}

func (m Memory) path() string { return filepath.Join(m.Root, "memory.json") }

func (m Memory) read() (map[string]MemoryEntry, error) {
	values := map[string]MemoryEntry{}
	data, err := os.ReadFile(m.path())
	if os.IsNotExist(err) {
		return values, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &values); err != nil {
		return nil, err
	}
	return values, nil
}

func (m Memory) write(values map[string]MemoryEntry) error {
	if strings.TrimSpace(m.Root) == "" {
		return errors.New("memory root is not configured")
	}
	if err := os.MkdirAll(m.Root, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(m.Root, ".memory-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(append(data, '\n')); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, m.path())
}
