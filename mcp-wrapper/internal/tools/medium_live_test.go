package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"mcp-1c-analog/internal/ditrix"
	"mcp-1c-analog/internal/mcp"
)

// TestMediumToolsLive is opt-in because it requires a running EDT, DitriX
// EDT-MCP and a separately installed BSL Language Server. It performs only
// read operations and dry-runs.
func TestMediumToolsLive(t *testing.T) {
	endpoint := os.Getenv("LIVE_EDT_MCP")
	project := os.Getenv("LIVE_EDT_PROJECT")
	if endpoint == "" || project == "" {
		t.Skip("LIVE_EDT_MCP and LIVE_EDT_PROJECT are not set")
	}
	client, err := ditrix.New(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	server := mcp.NewServer("live-test", "test")
	_, err = RegisterDitrixEDTWithOptions(context.Background(), server, client, project, DitrixRegistrationOptions{
		WorkDir: os.Getenv("LIVE_WORK_DIR"), BSLLanguageServer: os.Getenv("LIVE_BSL_SERVER"),
		JavaExecutable: os.Getenv("LIVE_JAVA"),
	})
	if err != nil {
		t.Fatal(err)
	}
	requests := []map[string]any{
		{"name": "launch_debugger", "arguments": map[string]any{"operation": "status"}},
		{"name": "launch_debugger", "arguments": map[string]any{"operation": "listBreakpoints"}},
		{"name": "edit_metadata", "arguments": map[string]any{"operation": "setDcs", "arguments": map[string]any{"objectFqns": []string{"Report.NonexistentDryRun"}}}},
		{"name": "edit_metadata", "arguments": map[string]any{"operation": "help"}},
		{"name": "diagnostics", "arguments": map[string]any{"operation": "status"}},
		{"name": "vanessa", "arguments": map[string]any{"operation": "status"}},
		{"name": "update_configuration", "arguments": map[string]any{"operation": "help"}},
	}
	if helpFQN := os.Getenv("LIVE_HELP_FQN"); helpFQN != "" {
		requests = append(requests, map[string]any{"name": "get_object_help", "arguments": map[string]any{"object_fqn": helpFQN}})
	}
	if module := os.Getenv("LIVE_BSL_MODULE"); module != "" && os.Getenv("LIVE_BSL_SERVER") != "" {
		requests = append(requests, map[string]any{"name": "code_review", "arguments": map[string]any{
			"module_paths": []string{module}, "minimum_severity": "warning", "limit": 20,
		}})
	}
	var input strings.Builder
	for index, request := range requests {
		payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": index + 1, "method": "tools/call", "params": request})
		input.Write(payload)
		input.WriteByte('\n')
	}
	var output bytes.Buffer
	if err := server.ServeStdio(context.Background(), strings.NewReader(input.String()), &output); err != nil {
		t.Fatal(err)
	}
	responses := output.String()
	t.Log(responses)
	if strings.Contains(responses, `"error":`) {
		t.Fatalf("live MCP call failed: %s", responses)
	}
	markers := []string{"dry_run", "performance_backend", "guarded-source-tree", "provider_support_metadata"}
	if os.Getenv("LIVE_HELP_FQN") != "" {
		markers = append(markers, "help_file")
	}
	if os.Getenv("LIVE_BSL_MODULE") != "" && os.Getenv("LIVE_BSL_SERVER") != "" {
		markers = append(markers, "total_diagnostics")
	}
	for _, marker := range markers {
		if !strings.Contains(strings.ToLower(responses), marker) {
			t.Fatalf("live response has no %q marker: %s", marker, responses)
		}
	}
}
