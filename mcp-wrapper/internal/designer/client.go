package designer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Client invokes the 1C Designer directly. Credentials are held in memory and
// are never included in returned errors or log messages.
type Client struct {
	Platform string
	Infobase string
	User     string
	Password string
	WorkDir  string
	Timeout  time.Duration
	run      func(context.Context, string, ...string) error
}

type Status struct {
	Platform            string `json:"platform"`
	Infobase            string `json:"infobase"`
	PlatformExists      bool   `json:"platform_exists"`
	InfobaseExists      bool   `json:"infobase_exists"`
	UserConfigured      bool   `json:"user_configured"`
	PasswordConfigured  bool   `json:"password_configured"`
	AuthenticationProbe string `json:"authentication_probe"`
}

func New(platform, infobase, user, password, workDir string) *Client {
	return &Client{
		Platform: platform,
		Infobase: infobase,
		User:     user,
		Password: password,
		WorkDir:  workDir,
		Timeout:  30 * time.Minute,
	}
}

func (c *Client) Validate() error {
	if c.Platform == "" || c.Infobase == "" || c.WorkDir == "" {
		return errors.New("platform, infobase and work directory are required")
	}
	info, err := os.Stat(c.Platform)
	if err != nil {
		return fmt.Errorf("1C platform is unavailable: %w", err)
	}
	if info.IsDir() {
		return errors.New("1C platform path points to a directory")
	}
	info, err = os.Stat(c.Infobase)
	if err != nil {
		return fmt.Errorf("1C infobase is unavailable: %w", err)
	}
	if !info.IsDir() {
		return errors.New("1C infobase path is not a directory")
	}
	return os.MkdirAll(c.WorkDir, 0o700)
}

func (c *Client) Status(ctx context.Context, probe bool) Status {
	status := Status{
		Platform:            c.Platform,
		Infobase:            c.Infobase,
		UserConfigured:      c.User != "",
		PasswordConfigured:  c.Password != "",
		AuthenticationProbe: "not_requested",
	}
	if info, err := os.Stat(c.Platform); err == nil && !info.IsDir() {
		status.PlatformExists = true
	}
	if info, err := os.Stat(c.Infobase); err == nil && info.IsDir() {
		status.InfobaseExists = true
	}
	if !probe {
		return status
	}
	probeDir, err := os.MkdirTemp(c.WorkDir, "auth-probe-*")
	if err != nil {
		status.AuthenticationProbe = "failed: " + err.Error()
		return status
	}
	defer os.RemoveAll(probeDir)
	if err := c.DumpConfig(ctx, filepath.Join(probeDir, "dump")); err != nil {
		status.AuthenticationProbe = "failed: " + err.Error()
	} else {
		status.AuthenticationProbe = "ok"
	}
	return status
}

func (c *Client) DumpConfig(ctx context.Context, destination string) error {
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return err
	}
	return c.designer(ctx, "dump-config", "/DumpConfigToFiles", destination, "-Format", "Hierarchical")
}

func (c *Client) LoadConfigFromFiles(ctx context.Context, source string) error {
	return c.designer(ctx, "load-config", "/LoadConfigFromFiles", source, "-Format", "Hierarchical")
}

func (c *Client) UpdateDBCfg(ctx context.Context) error {
	return c.designer(ctx, "update-db", "/UpdateDBCfg")
}

func (c *Client) DumpCfg(ctx context.Context, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	return c.designer(ctx, "backup-cf", "/DumpCfg", destination)
}

// DumpExtensionCfg exports one installed configuration extension to a CFE
// file through Designer's non-interactive -Extension selector.
func (c *Client) DumpExtensionCfg(ctx context.Context, destination, extensionName string) error {
	if strings.TrimSpace(extensionName) == "" {
		return errors.New("extension name is required")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	return c.designer(ctx, "backup-cfe", "/DumpCfg", destination, "-Extension", extensionName)
}

func (c *Client) LoadCfg(ctx context.Context, source string) error {
	return c.designer(ctx, "restore-cf", "/LoadCfg", source)
}

func (c *Client) CreateInfobase(ctx context.Context, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	args := []string{"CREATEINFOBASE", "File=" + destination, "/DisableStartupDialogs", "/DisableStartupMessages"}
	return c.execute(ctx, "create-infobase", args, "")
}

func (c *Client) ForInfobase(path string) *Client {
	clone := *c
	clone.Infobase = path
	clone.User = ""
	clone.Password = ""
	return &clone
}

func (c *Client) designer(ctx context.Context, operation string, args ...string) error {
	logDir := filepath.Join(c.WorkDir, "logs")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return err
	}
	logPath := filepath.Join(logDir, fmt.Sprintf("%s-%d.log", operation, time.Now().UnixNano()))
	full := []string{"DESIGNER", "/F", c.Infobase}
	full = append(full, args...)
	full = append(full, "/Out", logPath, "/DisableStartupDialogs", "/DisableStartupMessages")
	if c.User != "" {
		full = append(full, "/N", c.User)
	}
	if c.Password != "" {
		full = append(full, "/P", c.Password)
	}
	return c.execute(ctx, operation, full, logPath)
}

func (c *Client) execute(parent context.Context, operation string, args []string, logPath string) error {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	var err error
	if c.run != nil {
		err = c.run(ctx, c.Platform, args...)
	} else {
		cmd := exec.CommandContext(ctx, c.Platform, args...)
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		err = cmd.Run()
	}
	logText := readLog(logPath)
	if c.Password != "" {
		logText = strings.ReplaceAll(logText, c.Password, "[REDACTED]")
	}
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("%s timed out", operation)
	}
	if err != nil {
		if logText == "" {
			return fmt.Errorf("%s failed: %w", operation, err)
		}
		return fmt.Errorf("%s failed: %w; 1C log: %s", operation, err, logText)
	}
	if hasLogError(logText) {
		return fmt.Errorf("%s reported an error: %s", operation, logText)
	}
	return nil
}

func readLog(path string) string {
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if len(data) > 16*1024 {
		data = data[len(data)-16*1024:]
	}
	return strings.TrimSpace(string(data))
}

func hasLogError(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "\ufeff"))
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "ошибка") ||
			strings.HasPrefix(lower, "фатальная ошибка") ||
			strings.HasPrefix(lower, "error") ||
			strings.Contains(lower, "пользователь иб не идентифицирован") {
			return true
		}
	}
	return false
}
