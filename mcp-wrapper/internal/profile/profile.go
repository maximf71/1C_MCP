package profile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var validID = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,62}$`)

// Profile is a secret-free description of one fixed 1C target.  A running
// MCP process is always bound to exactly one profile; tools never accept a
// profile name or an arbitrary infobase path.
type Profile struct {
	SchemaVersion       int       `json:"schema_version"`
	ID                  string    `json:"id"`
	DisplayName         string    `json:"display_name,omitempty"`
	BaseKind            string    `json:"base_kind,omitempty"`
	Infobase            string    `json:"infobase,omitempty"`
	Platform            string    `json:"platform,omitempty"`
	PlatformVersion     string    `json:"platform_version,omitempty"`
	BaseURL             string    `json:"base_url,omitempty"`
	HTTPUserEnv         string    `json:"http_user_env,omitempty"`
	HTTPPasswordEnv     string    `json:"http_password_env,omitempty"`
	DBUserEnv           string    `json:"db_user_env,omitempty"`
	DBPasswordEnv       string    `json:"db_password_env,omitempty"`
	DumpDir             string    `json:"dump_dir,omitempty"`
	ComparisonDump      string    `json:"comparison_dump,omitempty"`
	CacheDir            string    `json:"cache_dir,omitempty"`
	WorkDir             string    `json:"work_dir,omitempty"`
	EDTWorkspace        string    `json:"edt_workspace,omitempty"`
	EDTBridge           string    `json:"edt_bridge,omitempty"`
	DitrixURL           string    `json:"ditrix_url,omitempty"`
	DitrixProject       string    `json:"ditrix_project,omitempty"`
	ExternalObjectsRoot string    `json:"external_objects_root,omitempty"`
	GitRoot             string    `json:"git_root,omitempty"`
	GitExecutable       string    `json:"git_executable,omitempty"`
	RequestTimeout      string    `json:"request_timeout,omitempty"`
	MaxResponseSize     int64     `json:"max_response_size,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

func (p *Profile) Normalize() error {
	p.ID = strings.ToLower(strings.TrimSpace(p.ID))
	if !validID.MatchString(p.ID) {
		return errors.New("profile id must match [a-z][a-z0-9_-]{1,62}")
	}
	if p.SchemaVersion == 0 {
		p.SchemaVersion = 1
	}
	if p.SchemaVersion != 1 {
		return fmt.Errorf("unsupported profile schema version %d", p.SchemaVersion)
	}
	p.BaseKind = strings.ToLower(strings.TrimSpace(p.BaseKind))
	if p.BaseKind == "" && p.Infobase != "" {
		p.BaseKind = "file"
	}
	if p.BaseKind != "" && p.BaseKind != "file" && p.BaseKind != "server" {
		return errors.New("base_kind must be file or server")
	}
	if p.BaseKind == "file" && p.Infobase != "" {
		absolute, err := filepath.Abs(p.Infobase)
		if err != nil {
			return fmt.Errorf("resolve infobase: %w", err)
		}
		p.Infobase = filepath.Clean(absolute)
	}
	for _, path := range []*string{&p.GitRoot, &p.GitExecutable} {
		if strings.TrimSpace(*path) == "" {
			continue
		}
		absolute, err := filepath.Abs(*path)
		if err != nil {
			return fmt.Errorf("resolve fixed tool path: %w", err)
		}
		*path = filepath.Clean(absolute)
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{"http_user_env", p.HTTPUserEnv}, {"http_password_env", p.HTTPPasswordEnv},
		{"db_user_env", p.DBUserEnv}, {"db_password_env", p.DBPasswordEnv},
	} {
		if field.value != "" && !validEnvironmentName(field.value) {
			return fmt.Errorf("%s is not a valid environment variable name", field.name)
		}
	}
	if p.RequestTimeout == "" {
		p.RequestTimeout = "5m"
	}
	if p.MaxResponseSize == 0 {
		p.MaxResponseSize = 128 << 20
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now().UTC()
	}
	p.UpdatedAt = time.Now().UTC()
	return nil
}

func validEnvironmentName(value string) bool {
	for index, r := range value {
		if !(r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || index > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return value != ""
}

func DefaultRoot() (string, error) {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		var err error
		base, err = os.UserConfigDir()
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(base, "mcp-1c", "profiles"), nil
}

type Store struct{ Root string }

func NewStore(root string) (*Store, error) {
	if strings.TrimSpace(root) == "" {
		var err error
		root, err = DefaultRoot()
		if err != nil {
			return nil, err
		}
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	return &Store{Root: filepath.Clean(absolute)}, nil
}

func (s *Store) Save(value Profile) error {
	if err := value.Normalize(); err != nil {
		return err
	}
	if err := os.MkdirAll(s.Root, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(s.path(value.ID), append(data, '\n'), 0o600)
}

func (s *Store) Load(id string) (Profile, error) {
	if !validID.MatchString(strings.ToLower(strings.TrimSpace(id))) {
		return Profile{}, errors.New("invalid profile id")
	}
	data, err := os.ReadFile(s.path(strings.ToLower(id)))
	if err != nil {
		return Profile{}, err
	}
	var value Profile
	if err := json.Unmarshal(data, &value); err != nil {
		return Profile{}, fmt.Errorf("decode profile: %w", err)
	}
	if err := value.Normalize(); err != nil {
		return Profile{}, err
	}
	return value, nil
}

func (s *Store) List() ([]Profile, error) {
	entries, err := os.ReadDir(s.Root)
	if os.IsNotExist(err) {
		return []Profile{}, nil
	}
	if err != nil {
		return nil, err
	}
	var result []Profile
	for _, entry := range entries {
		if entry.IsDir() || strings.ToLower(filepath.Ext(entry.Name())) != ".json" {
			continue
		}
		value, err := s.Load(strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())))
		if err != nil {
			return nil, fmt.Errorf("load %s: %w", entry.Name(), err)
		}
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func (s *Store) Remove(id string) error {
	if !validID.MatchString(strings.ToLower(strings.TrimSpace(id))) {
		return errors.New("invalid profile id")
	}
	return os.Remove(s.path(strings.ToLower(id)))
}

func (s *Store) Export(id, destination string) error {
	value, err := s.Load(id)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(destination, append(data, '\n'), 0o600)
}

func (s *Store) Import(source string) (Profile, error) {
	data, err := os.ReadFile(source)
	if err != nil {
		return Profile{}, err
	}
	var value Profile
	if err := json.Unmarshal(data, &value); err != nil {
		return Profile{}, err
	}
	if err := s.Save(value); err != nil {
		return Profile{}, err
	}
	return s.Load(value.ID)
}

func (s *Store) path(id string) string { return filepath.Join(s.Root, id+".json") }

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(absolute), ".mcp-1c-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(mode); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, absolute)
}
