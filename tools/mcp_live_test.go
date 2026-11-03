package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pijalu/agent-mcp/client"

	"github.com/pijalu/goa/internal"
)

// newLiveAgentMCPTool builds an mcp tool whose backend is the REAL
// agent-mcp client (NewWithManager) over an in-process MCP fixture server.
// This is the end-to-end proof: goa tool params -> agent-mcp client library
// -> MCP server -> shaped payload.
func newLiveAgentMCPTool(t *testing.T) *AgentMCPTool {
	t.Helper()
	mgr, err := client.NewSessionManager(
		map[string]client.ServerEntry{"alpha": {Command: "alpha-cmd"}},
		func(ctx context.Context, _ string, _ client.ServerEntry) (*mcp.ClientSession, error) {
			_, ct := newMCPFixtureServer(t)
			c := mcp.NewClient(&mcp.Implementation{Name: "goa-test", Version: "v0.0.1"}, nil)
			return c.Connect(ctx, ct, nil)
		})
	if err != nil {
		t.Fatalf("NewSessionManager: %v", err)
	}
	c := client.NewWithManager(mgr)
	tool := &AgentMCPTool{
		NewBackend: func(client.Options) (AgentMCPBackend, error) { return backendAdapter{c: c}, nil },
	}
	t.Cleanup(func() { _ = tool.Close() })
	return tool
}

// mcpFixtureServer is an in-process MCP server with one echo tool, one
// resource, and one prompt (ported from the agent-mcp session fixture).
type mcpFixtureArgs struct {
	Text string `json:"text"`
}

func newMCPFixtureServer(t *testing.T) (*mcp.Server, *mcp.InMemoryTransport) {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "goa-fixture", Version: "v0.0.1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "echoes text"},
		func(_ context.Context, _ *mcp.CallToolRequest, in mcpFixtureArgs) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: in.Text}},
			}, nil, nil
		})
	// doubled mirrors the shape spec-compliant servers send: the structured
	// payload serialized as text plus the parsed structuredContent. The
	// shaped payload the tool returns must carry the body once.
	mcp.AddTool(server, &mcp.Tool{Name: "doubled", Description: "returns a mirrored structured payload"},
		func(_ context.Context, _ *mcp.CallToolRequest, _ mcpFixtureArgs) (*mcp.CallToolResult, any, error) {
			body := map[string]any{
				"normalized_result": map[string]any{"pseudocode": strings.Repeat("ldr x0, [x1]; ", 700)},
				"analysis_profile":  map[string]any{"provider": map[string]any{"id": "fixture"}},
			}
			raw, err := json.Marshal(body)
			if err != nil {
				return nil, nil, err
			}
			return &mcp.CallToolResult{
				Content:           []mcp.Content{&mcp.TextContent{Text: string(raw)}},
				StructuredContent: body,
			}, nil, nil
		})
	server.AddResource(&mcp.Resource{URI: "mem://hello", Name: "hello"},
		func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{
				Contents: []*mcp.ResourceContents{{URI: "mem://hello", Text: "world"}},
			}, nil
		})
	server.AddPrompt(&mcp.Prompt{Name: "greet"},
		func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			return &mcp.GetPromptResult{
				Messages: []*mcp.PromptMessage{{
					Role:    "user",
					Content: &mcp.TextContent{Text: "hello " + req.Params.Arguments["name"]},
				}},
			}, nil
		})
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), serverTransport, nil); err != nil {
		t.Fatalf("fixture connect: %v", err)
	}
	return server, clientTransport
}

// TestAgentMCPLiveRoundTrip exercises every read op against the in-memory
// MCP server through the real agent-mcp client library.
func TestAgentMCPLiveRoundTrip(t *testing.T) {
	tool := newLiveAgentMCPTool(t)

	out, err := tool.Execute(`{"action":"list_servers"}`)
	if err != nil {
		t.Fatalf("list_servers: %v", err)
	}
	if !strings.Contains(out, "alpha") {
		t.Errorf("list_servers = %s, want alpha", out)
	}

	out, err = tool.Execute(`{"action":"list_tools","server":"alpha"}`)
	if err != nil {
		t.Fatalf("list_tools: %v", err)
	}
	if !strings.Contains(out, "echo") {
		t.Errorf("list_tools = %s, want echo", out)
	}

	out, err = tool.Execute(`{"action":"call_tool","server":"alpha","tool":"echo","args":{"text":"goa-live"}}`)
	if err != nil {
		t.Fatalf("call_tool: %v", err)
	}
	if !strings.Contains(out, "goa-live") {
		t.Errorf("call_tool = %s, want echoed text", out)
	}

	out, err = tool.Execute(`{"action":"read_resource","server":"alpha","uri":"mem://hello"}`)
	if err != nil {
		t.Fatalf("read_resource: %v", err)
	}
	if !strings.Contains(out, "world") {
		t.Errorf("read_resource = %s, want body", out)
	}

	out, err = tool.Execute(`{"action":"get_prompt","server":"alpha","prompt":"greet","args":{"name":"goa"}}`)
	if err != nil {
		t.Fatalf("get_prompt: %v", err)
	}
	if !strings.Contains(out, "hello goa") {
		t.Errorf("get_prompt = %s, want greeting", out)
	}
}

// TestAgentMCPLiveUnknownServer checks the taxonomy through the real path:
// an unknown server is a parameter mistake (invalid_params), matching the
// agent-mcp exit-code taxonomy.
func TestAgentMCPLiveUnknownServer(t *testing.T) {
	tool := newLiveAgentMCPTool(t)
	_, err := tool.Execute(`{"action":"call_tool","server":"ghost","tool":"echo"}`)
	if err == nil {
		t.Fatal("want error, got nil")
	}
	var te *internal.ToolError
	if !errors.As(err, &te) {
		t.Fatalf("error %v is not a ToolError", err)
	}
	if te.Type != "invalid_params" {
		t.Errorf("type = %s, want invalid_params (unknown server is a caller mistake)", te.Type)
	}
	if !strings.Contains(err.Error(), "unknown server") {
		t.Errorf("error = %v, want unknown server", err)
	}
}

// TestAgentMCPLiveToolErrorClass checks that a tool-reported MCP error
// surfaces as mcp_error, not as a success payload.
func TestAgentMCPLiveToolErrorClass(t *testing.T) {
	mgr, err := client.NewSessionManager(
		map[string]client.ServerEntry{"alpha": {Command: "alpha-cmd"}},
		func(ctx context.Context, _ string, _ client.ServerEntry) (*mcp.ClientSession, error) {
			server := mcp.NewServer(&mcp.Implementation{Name: "goa-fixture", Version: "v0.0.1"}, nil)
			mcp.AddTool(server, &mcp.Tool{Name: "fail"},
				func(_ context.Context, _ *mcp.CallToolRequest, _ mcpFixtureArgs) (*mcp.CallToolResult, any, error) {
					return &mcp.CallToolResult{
						Content: []mcp.Content{&mcp.TextContent{Text: "boom"}},
						IsError: true,
					}, nil, nil
				})
			st, ct := mcp.NewInMemoryTransports()
			if _, err := server.Connect(context.Background(), st, nil); err != nil {
				return nil, err
			}
			c := mcp.NewClient(&mcp.Implementation{Name: "goa-test", Version: "v0.0.1"}, nil)
			return c.Connect(ctx, ct, nil)
		})
	if err != nil {
		t.Fatalf("NewSessionManager: %v", err)
	}
	tool := &AgentMCPTool{
		NewBackend: func(client.Options) (AgentMCPBackend, error) {
			return backendAdapter{c: client.NewWithManager(mgr)}, nil
		},
	}
	_, err = tool.Execute(`{"action":"call_tool","server":"alpha","tool":"fail"}`)
	if err == nil {
		t.Fatal("want error, got nil")
	}
	var te *internal.ToolError
	if !errors.As(err, &te) || te.Type != "mcp_error" {
		t.Fatalf("error = %v, want mcp_error", err)
	}
}

// TestAgentMCPLiveMirroredPayloadOnce is the byte-count assertion for the
// doubled-payload bug: a server that mirrors its structured payload as text
// content must reach the tool output once, in both the default and the raw
// pass, with the raw pass keeping every structured field intact.
func TestAgentMCPLiveMirroredPayloadOnce(t *testing.T) {
	tool := newLiveAgentMCPTool(t)

	out, err := tool.Execute(`{"action":"call_tool","server":"alpha","tool":"doubled","args":{"text":"x"}}`)
	if err != nil {
		t.Fatalf("call_tool doubled: %v", err)
	}
	// One copy is ~9.8 KB of pseudocode plus envelope; the pre-fix doubled
	// output was ~2.0x the payload. Allow generous overhead but never two.
	if got := len(out); got > 2*9800 {
		t.Errorf("output is %d bytes; the mirrored copy survived", got)
	}
	if strings.Count(out, "ldr x0, [x1]") < 700 {
		t.Errorf("output lost the structured payload body")
	}
	if strings.Count(out, `"structuredContent"`) != 1 {
		t.Errorf("output carries the payload %d times, want once: %d bytes", strings.Count(out, `"structuredContent"`), len(out))
	}
	if strings.Contains(out, `"content"`) {
		t.Errorf("output kept the mirrored content copy: %.200s", out)
	}

	rawOut, err := tool.Execute(`{"action":"call_tool","server":"alpha","tool":"doubled","args":{"text":"x"},"raw":true}`)
	if err != nil {
		t.Fatalf("call_tool doubled raw: %v", err)
	}
	if strings.Count(rawOut, "ldr x0, [x1]") < 700 {
		t.Errorf("raw output lost the structured payload body")
	}
	if strings.Contains(rawOut, `"content"`) {
		t.Errorf("raw output kept the mirrored content copy: %.200s", rawOut)
	}
	if !strings.Contains(rawOut, "analysis_profile") {
		t.Errorf("raw output lost the server's own structured fields: %.200s", rawOut)
	}
}
