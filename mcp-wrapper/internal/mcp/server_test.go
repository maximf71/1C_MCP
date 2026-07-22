package mcp

import (
	"bytes"
	"context"
	"strings"
	"testing"

	official "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestToolsList(t *testing.T) {
	server := NewServer("test", "dev")
	server.SetInstructions("prepare before apply")
	server.AddTool(Tool{
		Name:        "ping",
		Description: "Ping tool",
		InputSchema: map[string]any{"type": "object"},
		Annotations: &Annotations{ReadOnlyHint: true},
		Handler: func(context.Context, map[string]any) (any, error) {
			return map[string]string{"pong": "ok"}, nil
		},
	})
	input := strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}` + "\n" +
			`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n",
	)
	var output bytes.Buffer
	if err := server.ServeStdio(context.Background(), input, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"ping"`) {
		t.Fatalf("tools/list output does not contain tool name: %s", output.String())
	}
	if !strings.Contains(output.String(), `"instructions":"prepare before apply"`) {
		t.Fatalf("initialize output does not contain instructions: %s", output.String())
	}
	if !strings.Contains(output.String(), `"readOnlyHint":true`) {
		t.Fatalf("tools/list output does not contain annotations: %s", output.String())
	}
}

func TestOfficialSDKAdapter(t *testing.T) {
	ctx := context.Background()
	server := NewServer("test", "1")
	server.AddTool(Tool{Name: "ping", InputSchema: map[string]any{"type": "object"}, Handler: func(context.Context, map[string]any) (any, error) {
		return map[string]any{"pong": true}, nil
	}})
	serverTransport, clientTransport := official.NewInMemoryTransports()
	serverSession, err := server.officialServer().Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	client := official.NewClient(&official.Implementation{Name: "test-client", Version: "1"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()
	tools, err := clientSession.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 1 || tools.Tools[0].Name != "ping" {
		t.Fatalf("unexpected tools: %#v, %v", tools, err)
	}
	result, err := clientSession.CallTool(ctx, &official.CallToolParams{Name: "ping"})
	if err != nil || result.IsError || len(result.Content) != 1 {
		t.Fatalf("unexpected result: %#v, %v", result, err)
	}
}

func TestOfficialSDKAdapterPreservesEmbeddedResource(t *testing.T) {
	ctx := context.Background()
	server := NewServer("test", "1")
	server.AddTool(Tool{Name: "resource", InputSchema: map[string]any{"type": "object"}, Handler: func(context.Context, map[string]any) (any, error) {
		return RawToolResult{"content": []any{map[string]any{
			"type": "resource",
			"resource": map[string]any{
				"uri":      "edt://workspace/project/module",
				"mimeType": "text/markdown",
				"text":     "```bsl\nMessage(\"ok\");\n```",
			},
		}}}, nil
	}})
	serverTransport, clientTransport := official.NewInMemoryTransports()
	serverSession, err := server.officialServer().Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	client := official.NewClient(&official.Implementation{Name: "test-client", Version: "1"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()
	result, err := clientSession.CallTool(ctx, &official.CallToolParams{Name: "resource"})
	if err != nil || result.IsError || len(result.Content) != 1 {
		t.Fatalf("unexpected result: %#v, %v", result, err)
	}
	resource, ok := result.Content[0].(*official.EmbeddedResource)
	if !ok {
		t.Fatalf("expected embedded resource, got %T", result.Content[0])
	}
	if resource.Resource.URI != "edt://workspace/project/module" || resource.Resource.MIMEType != "text/markdown" || !strings.Contains(resource.Resource.Text, "Message") {
		t.Fatalf("unexpected embedded resource: %#v", resource.Resource)
	}
}
