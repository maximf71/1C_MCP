package edt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type bridgeInfo struct {
	Version int    `json:"version"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	Token   string `json:"token"`
}

type Client struct {
	bridgeFile string
	http       *http.Client
}

func New(bridgeFile string) *Client {
	return &Client{bridgeFile: bridgeFile, http: &http.Client{Timeout: 10 * time.Minute}}
}

func (c *Client) Health(ctx context.Context) (map[string]any, error) {
	return c.call(ctx, http.MethodGet, "/health", nil)
}

func (c *Client) List(ctx context.Context, metadataType string) (map[string]any, error) {
	return c.call(ctx, http.MethodPost, "/list", map[string]any{"type": emptyAsNil(metadataType)})
}

func (c *Client) Inspect(ctx context.Context, metadataType, name string) (map[string]any, error) {
	return c.call(ctx, http.MethodPost, "/inspect", map[string]any{"type": metadataType, "name": name})
}

func (c *Client) Prepare(ctx context.Context, metadataType, sourceName, targetName string) (map[string]any, error) {
	return c.call(ctx, http.MethodPost, "/prepare-clone", map[string]any{
		"type": metadataType, "source_name": sourceName, "target_name": targetName,
	})
}

func (c *Client) Apply(ctx context.Context, planID string) (map[string]any, error) {
	return c.call(ctx, http.MethodPost, "/apply-clone", map[string]any{"plan_id": planID})
}

func (c *Client) Verify(ctx context.Context, metadataType, name string) (map[string]any, error) {
	return c.call(ctx, http.MethodPost, "/verify", map[string]any{"type": metadataType, "name": name})
}

func (c *Client) Discard(ctx context.Context, planID string) (map[string]any, error) {
	return c.call(ctx, http.MethodPost, "/discard-plan", map[string]any{"plan_id": planID})
}

func (c *Client) ListBslModules(ctx context.Context, contains string, limit int) (map[string]any, error) {
	return c.call(ctx, http.MethodPost, "/bsl/list", map[string]any{
		"contains": emptyAsNil(contains), "limit": limit,
	})
}

func (c *Client) ReadBslModule(ctx context.Context, modulePath string, startLine, endLine int) (map[string]any, error) {
	return c.call(ctx, http.MethodPost, "/bsl/read", map[string]any{
		"module_path": modulePath, "start_line": startLine, "end_line": endLine,
	})
}

func (c *Client) SearchBslCode(ctx context.Context, query, pathContains string, limit int) (map[string]any, error) {
	return c.call(ctx, http.MethodPost, "/bsl/search", map[string]any{
		"query": query, "path_contains": emptyAsNil(pathContains), "limit": limit,
	})
}

func (c *Client) BslDiagnostics(ctx context.Context, modulePath string, limit int) (map[string]any, error) {
	return c.call(ctx, http.MethodPost, "/bsl/diagnostics", map[string]any{
		"module_path": emptyAsNil(modulePath), "limit": limit,
	})
}

func (c *Client) BslContentAssist(ctx context.Context, modulePath string, line, column int,
	contains string, limit int, includeDocumentation bool) (map[string]any, error) {
	return c.call(ctx, http.MethodPost, "/bsl/content-assist", map[string]any{
		"module_path": modulePath, "line": line, "column": column,
		"contains": emptyAsNil(contains), "limit": limit, "include_documentation": includeDocumentation,
	})
}

func (c *Client) ImportExternalObjectXML(ctx context.Context, projectName, sourceXML,
	platformVersion string) (map[string]any, error) {
	return c.call(ctx, http.MethodPost, "/external/import-xml", map[string]any{
		"project_name": projectName, "source_xml": sourceXML,
		"platform_version": emptyAsNil(platformVersion),
	})
}

func (c *Client) ListInfobases(ctx context.Context) (map[string]any, error) {
	return c.call(ctx, http.MethodPost, "/infobases/list", map[string]any{})
}

func (c *Client) BindInfobase(ctx context.Context, arguments map[string]any) (map[string]any, error) {
	return c.call(ctx, http.MethodPost, "/infobases/bind", arguments)
}

func (c *Client) UnbindInfobase(ctx context.Context, arguments map[string]any) (map[string]any, error) {
	return c.call(ctx, http.MethodPost, "/infobases/unbind", arguments)
}

func (c *Client) call(ctx context.Context, method, path string, payload any) (map[string]any, error) {
	bridge, err := c.readBridge()
	if err != nil {
		return nil, err
	}
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("encode EDT request: %w", err)
		}
		body = bytes.NewReader(data)
	}
	url := fmt.Sprintf("http://127.0.0.1:%d%s", bridge.Port, path)
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("create EDT request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+bridge.Token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("EDT bridge is unavailable: %w", err)
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, 8<<20)
	var result map[string]any
	if err := json.NewDecoder(limited).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode EDT response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := result["error"].(string)
		if message == "" {
			message = response.Status
		}
		return nil, fmt.Errorf("EDT bridge: %s", message)
	}
	return result, nil
}

func (c *Client) readBridge() (*bridgeInfo, error) {
	data, err := os.ReadFile(c.bridgeFile)
	if err != nil {
		return nil, fmt.Errorf("EDT bridge descriptor is unavailable (start EDT): %w", err)
	}
	var bridge bridgeInfo
	if err := json.Unmarshal(data, &bridge); err != nil {
		return nil, fmt.Errorf("invalid EDT bridge descriptor: %w", err)
	}
	if bridge.Version != 1 || bridge.Port < 1 || bridge.Port > 65535 || bridge.Token == "" {
		return nil, fmt.Errorf("invalid EDT bridge descriptor fields")
	}
	if bridge.Host != "127.0.0.1" && !strings.EqualFold(bridge.Host, "localhost") {
		return nil, fmt.Errorf("EDT bridge descriptor is not locked to loopback")
	}
	return &bridge, nil
}

func emptyAsNil(value string) any {
	if value == "" {
		return nil
	}
	return value
}
