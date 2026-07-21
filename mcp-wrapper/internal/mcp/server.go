package mcp

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"sync"

	official "github.com/modelcontextprotocol/go-sdk/mcp"
)

type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations *Annotations   `json:"annotations,omitempty"`
	Handler     Handler        `json:"-"`
}

// ServeOfficialStdio runs the production transport through the official Go
// MCP SDK. ServeStdio remains as an injectable protocol harness for unit tests.
func (s *Server) ServeOfficialStdio(ctx context.Context) error {
	return s.officialServer().Run(ctx, &official.StdioTransport{})
}

func (s *Server) officialServer() *official.Server {
	s.mu.RLock()
	tools := append([]Tool(nil), s.tools...)
	instructions := s.instructions
	s.mu.RUnlock()
	server := official.NewServer(&official.Implementation{Name: s.name, Version: s.version}, &official.ServerOptions{Instructions: instructions})
	for _, registered := range tools {
		registered := registered
		toolDefinition := &official.Tool{Name: registered.Name, Description: registered.Description, InputSchema: registered.InputSchema}
		if registered.Annotations != nil {
			toolDefinition.Annotations = &official.ToolAnnotations{
				Title: registered.Annotations.Title, ReadOnlyHint: registered.Annotations.ReadOnlyHint,
				DestructiveHint: boolPointer(registered.Annotations.DestructiveHint),
				IdempotentHint:  registered.Annotations.IdempotentHint,
				OpenWorldHint:   boolPointer(registered.Annotations.OpenWorldHint),
			}
		}
		server.AddTool(toolDefinition, func(callContext context.Context, request *official.CallToolRequest) (*official.CallToolResult, error) {
			arguments := map[string]any{}
			if len(request.Params.Arguments) > 0 {
				if err := json.Unmarshal(request.Params.Arguments, &arguments); err != nil {
					return errorToolResult("invalid arguments: " + err.Error()), nil
				}
			}
			value, err := registered.Handler(callContext, arguments)
			if err != nil {
				return errorToolResult(err.Error()), nil
			}
			return officialToolResult(value), nil
		})
	}
	return server
}

func boolPointer(value bool) *bool { return &value }

func errorToolResult(message string) *official.CallToolResult {
	return &official.CallToolResult{Content: []official.Content{&official.TextContent{Text: message}}, IsError: true}
}

func officialToolResult(value any) *official.CallToolResult {
	if raw, ok := value.(RawToolResult); ok {
		result := &official.CallToolResult{StructuredContent: raw["structuredContent"]}
		if isError, ok := raw["isError"].(bool); ok {
			result.IsError = isError
		}
		if blocks, ok := raw["content"].([]any); ok {
			for _, block := range blocks {
				item, _ := block.(map[string]any)
				switch item["type"] {
				case "text":
					result.Content = append(result.Content, &official.TextContent{Text: fmt.Sprint(item["text"])})
				case "image":
					data, err := base64.StdEncoding.DecodeString(fmt.Sprint(item["data"]))
					if err == nil {
						result.Content = append(result.Content, &official.ImageContent{Data: data, MIMEType: fmt.Sprint(item["mimeType"])})
					}
				default:
					encoded, _ := json.Marshal(item)
					result.Content = append(result.Content, &official.TextContent{Text: string(encoded)})
				}
			}
		}
		if len(result.Content) == 0 {
			encoded, _ := json.MarshalIndent(raw, "", "  ")
			result.Content = []official.Content{&official.TextContent{Text: string(encoded)}}
		}
		return result
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		data = []byte(fmt.Sprint(value))
	}
	structured := any(nil)
	if _, ok := value.(map[string]any); ok {
		structured = value
	}
	return &official.CallToolResult{Content: []official.Content{&official.TextContent{Text: string(data)}}, StructuredContent: structured}
}

type Annotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    bool   `json:"readOnlyHint"`
	DestructiveHint bool   `json:"destructiveHint"`
	IdempotentHint  bool   `json:"idempotentHint"`
	OpenWorldHint   bool   `json:"openWorldHint"`
}

type Handler func(context.Context, map[string]any) (any, error)

type Server struct {
	name         string
	version      string
	instructions string
	mu           sync.RWMutex
	tools        []Tool
}

func NewServer(name, version string) *Server {
	return &Server{name: name, version: version}
}

func (s *Server) SetInstructions(instructions string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.instructions = instructions
}

func (s *Server) AddTool(tool Tool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tools = append(s.tools, tool)
}

// HasTool reports whether a tool name is already registered. It is primarily
// used by optional backends so that the server's native, more constrained
// implementation always wins over a proxied implementation with the same name.
func (s *Server) HasTool(name string) bool {
	_, found := s.findTool(name)
	return found
}

func (s *Server) ServeStdio(ctx context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	enc := json.NewEncoder(out)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		var req request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			log.Printf("invalid json-rpc request: %v", err)
			continue
		}
		if req.ID == nil {
			continue
		}
		resp := s.handle(ctx, req)
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func (s *Server) handle(ctx context.Context, req request) response {
	switch req.Method {
	case "initialize":
		s.mu.RLock()
		instructions := s.instructions
		s.mu.RUnlock()
		return ok(req.ID, map[string]any{
			"protocolVersion": "2024-11-05",
			"serverInfo": map[string]any{
				"name":    s.name,
				"version": s.version,
			},
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
			"instructions": instructions,
		})
	case "tools/list":
		s.mu.RLock()
		defer s.mu.RUnlock()
		tools := make([]Tool, len(s.tools))
		copy(tools, s.tools)
		return ok(req.ID, map[string]any{"tools": tools})
	case "tools/call":
		var params callParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return fail(req.ID, -32602, "invalid params")
		}
		tool, found := s.findTool(params.Name)
		if !found {
			return fail(req.ID, -32601, "unknown tool: "+params.Name)
		}
		result, err := tool.Handler(ctx, params.Arguments)
		if err != nil {
			return fail(req.ID, -32000, err.Error())
		}
		return ok(req.ID, toolResult(result))
	default:
		return fail(req.ID, -32601, "method not found: "+req.Method)
	}
}

func (s *Server) findTool(name string) (Tool, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, tool := range s.tools {
		if tool.Name == name {
			return tool, true
		}
	}
	return Tool{}, false
}

// RawToolResult lets a proxy preserve a complete MCP tool result, including
// image content and structured content, instead of wrapping it in text.
type RawToolResult map[string]any

func toolResult(value any) map[string]any {
	if raw, ok := value.(RawToolResult); ok {
		return map[string]any(raw)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		data = []byte(fmt.Sprint(value))
	}
	return map[string]any{
		"content": []map[string]string{
			{"type": "text", "text": string(data)},
		},
	}
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string  `json:"jsonrpc"`
	ID      any     `json:"id,omitempty"`
	Result  any     `json:"result,omitempty"`
	Error   *errObj `json:"error,omitempty"`
}

type errObj struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type callParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

func ok(id, result any) response {
	return response{JSONRPC: "2.0", ID: id, Result: result}
}

func fail(id any, code int, message string) response {
	return response{JSONRPC: "2.0", ID: id, Error: &errObj{Code: code, Message: message}}
}
