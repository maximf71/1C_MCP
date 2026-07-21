// Package ditrix implements the small MCP-over-HTTP client used to integrate
// the separately installed DitriX EDT-MCP plug-in. The plug-in remains a
// separate AGPL-3.0 program; this package only speaks its public MCP protocol.
package ditrix

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

const maxResponseSize = 32 << 20

const (
	projectBuildRetryTimeout  = 30 * time.Second
	projectBuildRetryInterval = 500 * time.Millisecond
)

type Client struct {
	endpoint   string
	httpClient *http.Client
	nextID     atomic.Int64
}

type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations *Annotations   `json:"annotations,omitempty"`
}

type Annotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    bool   `json:"readOnlyHint"`
	DestructiveHint bool   `json:"destructiveHint"`
	IdempotentHint  bool   `json:"idempotentHint"`
	OpenWorldHint   bool   `json:"openWorldHint"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func New(endpoint string) (*Client, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid DitriX EDT-MCP URL: %w", err)
	}
	if u.Scheme != "http" {
		return nil, errors.New("DitriX EDT-MCP URL must use loopback HTTP")
	}
	host := strings.ToLower(u.Hostname())
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return nil, errors.New("DitriX EDT-MCP URL must point to localhost or a loopback IP")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("DitriX EDT-MCP URL must not contain credentials, query, or fragment")
	}
	if u.Path != "/mcp" {
		return nil, errors.New("DitriX EDT-MCP URL path must be /mcp")
	}
	client := &Client{
		endpoint: endpoint,
		httpClient: &http.Client{
			Timeout: 10 * time.Minute,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return errors.New("redirects are disabled for the local EDT-MCP endpoint")
			},
		},
	}
	client.nextID.Store(100)
	return client, nil
}

func (c *Client) Initialize(ctx context.Context) (ServerInfo, error) {
	var result struct {
		ServerInfo ServerInfo `json:"serverInfo"`
	}
	err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "mcp-1c-analog-locked-proxy",
			"version": "1",
		},
	}, &result)
	return result.ServerInfo, err
}

func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	var result struct {
		Tools []Tool `json:"tools"`
	}
	if err := c.call(ctx, "tools/list", map[string]any{}, &result); err != nil {
		return nil, err
	}
	return result.Tools, nil
}

func (c *Client) CallTool(ctx context.Context, name string, arguments map[string]any) (map[string]any, error) {
	return c.callToolWithRetry(ctx, name, arguments, projectBuildRetryTimeout, projectBuildRetryInterval)
}

func (c *Client) callToolWithRetry(ctx context.Context, name string, arguments map[string]any,
	retryTimeout, retryInterval time.Duration,
) (map[string]any, error) {
	deadline := time.Now().Add(retryTimeout)
	for {
		result, err := c.callToolOnce(ctx, name, arguments)
		if err == nil {
			return result, nil
		}
		if !isProjectBuildingError(err.Error()) || retryTimeout <= 0 || time.Now().Add(retryInterval).After(deadline) {
			return nil, err
		}
		timer := time.NewTimer(retryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (c *Client) callToolOnce(ctx context.Context, name string, arguments map[string]any) (map[string]any, error) {
	var result map[string]any
	if err := c.call(ctx, "tools/call", map[string]any{"name": name, "arguments": arguments}, &result); err != nil {
		return nil, err
	}
	if failed, _ := result["isError"].(bool); failed {
		return nil, fmt.Errorf("EDT-MCP tool %s failed: %s", name, textContent(result))
	}
	return result, nil
}

func isProjectBuildingError(message string) bool {
	normalized := strings.ToLower(message)
	return strings.Contains(normalized, "project is building") ||
		strings.Contains(normalized, "derived data not complete") ||
		strings.Contains(normalized, "project build in progress")
}

func (c *Client) call(ctx context.Context, method string, params any, target any) error {
	id := c.nextID.Add(1)
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("DitriX EDT-MCP request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil {
		return err
	}
	if len(body) > maxResponseSize {
		return errors.New("DitriX EDT-MCP response is too large")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("DitriX EDT-MCP returned HTTP %d", resp.StatusCode)
	}
	raw, err := decodeMCPResponse(body, resp.Header.Get("Content-Type"))
	if err != nil {
		return err
	}
	var envelope rpcResponse
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("invalid DitriX EDT-MCP JSON-RPC response: %w", err)
	}
	if envelope.Error != nil {
		return fmt.Errorf("DitriX EDT-MCP error %d: %s", envelope.Error.Code, envelope.Error.Message)
	}
	if len(envelope.Result) == 0 {
		return errors.New("DitriX EDT-MCP response has no result")
	}
	if err := json.Unmarshal(envelope.Result, target); err != nil {
		return fmt.Errorf("invalid DitriX EDT-MCP result: %w", err)
	}
	return nil
}

func decodeMCPResponse(body []byte, contentType string) ([]byte, error) {
	if strings.Contains(strings.ToLower(contentType), "text/event-stream") || bytes.HasPrefix(bytes.TrimSpace(body), []byte("event:")) {
		scanner := bufio.NewScanner(bytes.NewReader(body))
		scanner.Buffer(make([]byte, 0, 64*1024), maxResponseSize)
		var data []string
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data:") {
				data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
		}
		if err := scanner.Err(); err != nil {
			return nil, err
		}
		if len(data) == 0 {
			return nil, errors.New("DitriX EDT-MCP SSE response has no data event")
		}
		return []byte(strings.Join(data, "\n")), nil
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, errors.New("DitriX EDT-MCP returned an empty response")
	}
	return trimmed, nil
}

func textContent(result map[string]any) string {
	items, _ := result["content"].([]any)
	var parts []string
	for _, item := range items {
		entry, _ := item.(map[string]any)
		if text, _ := entry["text"].(string); text != "" {
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		return "unknown error"
	}
	return strings.Join(parts, "; ")
}
