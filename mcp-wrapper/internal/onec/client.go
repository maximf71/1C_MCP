package onec

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const DefaultMaxResponseBytes int64 = 128 << 20

const DefaultRequestTimeout = 5 * time.Minute

type Client struct {
	BaseURL          string
	User             string
	Password         string
	HTTPClient       *http.Client
	MaxResponseBytes int64
}

func NewClient(baseURL, user, password string) *Client {
	return NewClientWithOptions(baseURL, user, password, DefaultRequestTimeout, DefaultMaxResponseBytes)
}

func NewClientWithOptions(baseURL, user, password string, timeout time.Duration, maxResponseBytes int64) *Client {
	if timeout <= 0 {
		timeout = DefaultRequestTimeout
	}
	if maxResponseBytes <= 0 {
		maxResponseBytes = DefaultMaxResponseBytes
	}
	return &Client{
		BaseURL:          strings.TrimRight(baseURL, "/"),
		User:             user,
		Password:         password,
		HTTPClient:       &http.Client{Timeout: timeout},
		MaxResponseBytes: maxResponseBytes,
	}
}

func (c *Client) Get(ctx context.Context, endpoint string, result any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+endpoint, nil)
	if err != nil {
		return err
	}
	return c.do(req, result)
}

func (c *Client) Post(ctx context.Context, endpoint string, body any, result any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req, result)
}

func (c *Client) do(req *http.Request, result any) error {
	if c.User != "" {
		req.SetBasicAuth(c.User, c.Password)
	}
	req.Close = true
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("1C request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("1C returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	maximum := c.MaxResponseBytes
	if maximum <= 0 {
		maximum = DefaultMaxResponseBytes
	}
	limited := io.LimitReader(resp.Body, maximum+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return err
	}
	if int64(len(data)) > maximum {
		return fmt.Errorf("1C response is larger than %d bytes", maximum)
	}
	if err := json.Unmarshal(data, result); err != nil {
		return fmt.Errorf("decode 1C JSON response: %w", err)
	}
	return nil
}
