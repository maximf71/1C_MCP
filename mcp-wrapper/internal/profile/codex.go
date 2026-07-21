package profile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const markerPrefix = "mcp-1c-analog profile "

// UpdateCodexConfig appends or replaces only blocks previously managed by
// this application.  An unmanaged section with the same name is refused.
func UpdateCodexConfig(path, executable string, value Profile) (string, error) {
	if err := value.Normalize(); err != nil {
		return "", err
	}
	absoluteExecutable, err := filepath.Abs(executable)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	content := string(data)
	begin := "# BEGIN " + markerPrefix + value.ID
	end := "# END " + markerPrefix + value.ID
	content, managed := removeManagedBlock(content, begin, end)
	for _, name := range []string{value.ID + "_db", value.ID + "_edt"} {
		if !managed && strings.Contains(content, "[mcp_servers."+tomlKey(name)+"]") {
			return "", fmt.Errorf("Codex MCP server %q already exists and is not managed by mcp-1c-analog", name)
		}
	}
	block := renderCodexBlock(absoluteExecutable, value, begin, end)
	trimmed := strings.TrimRight(content, "\r\n")
	if trimmed != "" {
		trimmed += "\r\n\r\n"
	}
	updated := trimmed + block + "\r\n"
	backup := ""
	if len(data) > 0 {
		backup = path + ".mcp-1c-backup-" + time.Now().Format("20060102-150405")
		if err := os.WriteFile(backup, data, 0o600); err != nil {
			return "", fmt.Errorf("write Codex config backup: %w", err)
		}
	}
	if err := atomicWrite(path, []byte(updated), 0o600); err != nil {
		return "", err
	}
	return backup, nil
}

func RemoveCodexProfile(path, id string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	begin := "# BEGIN " + markerPrefix + id
	end := "# END " + markerPrefix + id
	updated, found := removeManagedBlock(string(data), begin, end)
	if !found {
		return errors.New("managed Codex profile block not found")
	}
	return atomicWrite(path, []byte(strings.TrimRight(updated, "\r\n")+"\r\n"), 0o600)
}

func removeManagedBlock(content, begin, end string) (string, bool) {
	start := strings.Index(content, begin)
	if start < 0 {
		return content, false
	}
	finishRelative := strings.Index(content[start:], end)
	if finishRelative < 0 {
		return content, false
	}
	finish := start + finishRelative + len(end)
	for finish < len(content) && (content[finish] == '\r' || content[finish] == '\n') {
		finish++
	}
	return content[:start] + content[finish:], true
}

func renderCodexBlock(executable string, value Profile, begin, end string) string {
	var builder strings.Builder
	builder.WriteString(begin + "\r\n")
	if value.BaseURL != "" || value.Infobase != "" || value.DumpDir != "" {
		writeServer(&builder, value.ID+"_db", executable, []string{"serve", "--profile", value.ID, "--mode", "db"})
	}
	if value.EDTBridge != "" || value.DitrixURL != "" {
		writeServer(&builder, value.ID+"_edt", executable, []string{"serve", "--profile", value.ID, "--mode", "edt"})
	}
	builder.WriteString(end)
	return builder.String()
}

func writeServer(builder *strings.Builder, name, executable string, args []string) {
	builder.WriteString("[mcp_servers." + tomlKey(name) + "]\r\n")
	builder.WriteString("command = " + tomlString(executable) + "\r\n")
	builder.WriteString("args = [")
	for index, arg := range args {
		if index > 0 {
			builder.WriteString(", ")
		}
		builder.WriteString(tomlString(arg))
	}
	builder.WriteString("]\r\n")
	builder.WriteString("default_tools_approval_mode = \"writes\"\r\n\r\n")
}

func tomlKey(value string) string {
	for _, r := range value {
		if !(r == '_' || r == '-' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return tomlString(value)
		}
	}
	return value
}

func tomlString(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\"", "\\\"")
	value = strings.ReplaceAll(value, "\r", "\\r")
	value = strings.ReplaceAll(value, "\n", "\\n")
	return "\"" + value + "\""
}
