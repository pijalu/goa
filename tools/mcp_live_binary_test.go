package tools

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pijalu/agent-mcp/client"
)

// pngFixture is a minimal (invalid-header-faithful) PNG byte slice used to
// prove bytes survive the whole pipeline unchanged.
var pngFixture = []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0xFA, 0xCE, 0x57}

// emptyArgs gives the binary fixtures a no-argument input schema.
type emptyArgs struct{}

// newBinaryFixtureTool spins an in-process MCP server whose tools return
// real binary content (image part + embedded blob resource) and wires the
// mcp tool to it through the real agent-mcp client library — the same path
// a chrome take_screenshot result takes.
func newBinaryFixtureTool(t *testing.T) *AgentMCPTool {
	t.Helper()
	pngRaw := pngFixture // SDK marshals Data/Blob to base64 itself
	pdfRaw := []byte("%PDF-1.4 live")
	mgr, err := client.NewSessionManager(
		map[string]client.ServerEntry{"bin": {Command: "bin-cmd"}},
		func(ctx context.Context, _ string, _ client.ServerEntry) (*mcp.ClientSession, error) {
			server := mcp.NewServer(&mcp.Implementation{Name: "goa-bin-fixture", Version: "v0.0.1"}, nil)
			mcp.AddTool(server, &mcp.Tool{Name: "shot"},
				func(_ context.Context, _ *mcp.CallToolRequest, _ emptyArgs) (*mcp.CallToolResult, any, error) {
					return &mcp.CallToolResult{
						Content: []mcp.Content{&mcp.ImageContent{Data: pngRaw, MIMEType: "image/png"}},
					}, nil, nil
				})
			mcp.AddTool(server, &mcp.Tool{Name: "doc"},
				func(_ context.Context, _ *mcp.CallToolRequest, _ emptyArgs) (*mcp.CallToolResult, any, error) {
					return &mcp.CallToolResult{
						Content: []mcp.Content{&mcp.EmbeddedResource{
							Resource: &mcp.ResourceContents{URI: "file:///x.pdf", MIMEType: "application/pdf", Blob: pdfRaw},
						}},
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
	t.Cleanup(func() { _ = tool.Close() })
	return tool
}

// TestAgentMCPLiveBinaryContent is the end-to-end proof: a typed MCP image
// result travels client library -> Shape -> extractor and comes out as a
// file + marker, with the image path attached for the vision pipeline.
func TestAgentMCPLiveBinaryContent(t *testing.T) {
	tool := newBinaryFixtureTool(t)
	tool.ExtractDir = t.TempDir()

	res, err := tool.ExecuteContextWithResult(context.Background(),
		`{"action":"call_tool","server":"bin","tool":"shot"}`)
	if err != nil {
		t.Fatalf("call_tool shot: %v", err)
	}
	if len(res.Images) != 1 {
		t.Fatalf("images = %v, want 1 extracted png", res.Images)
	}
	got, err := os.ReadFile(res.Images[0])
	if err != nil {
		t.Fatalf("read extracted image: %v", err)
	}
	if string(got) != string(pngFixture) {
		t.Fatalf("extracted bytes mismatch: got %x want %x", got, pngFixture)
	}
	if filepath.Ext(res.Images[0]) != ".png" {
		t.Fatalf("extension = %q, want .png", filepath.Ext(res.Images[0]))
	}
	if strings.Contains(res.Output, base64.StdEncoding.EncodeToString(pngFixture)) {
		t.Fatal("output still carries base64")
	}
	if !strings.Contains(res.Output, "[binary: image/png → "+res.Images[0]) {
		t.Fatalf("output missing marker:\n%s", res.Output)
	}

	res, err = tool.ExecuteContextWithResult(context.Background(),
		`{"action":"call_tool","server":"bin","tool":"doc"}`)
	if err != nil {
		t.Fatalf("call_tool doc: %v", err)
	}
	if len(res.Images) != 0 {
		t.Fatalf("pdf leaked into Images: %v", res.Images)
	}
	if !strings.Contains(res.Output, "[binary: application/pdf → ") {
		t.Fatalf("output missing pdf marker:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, ".pdf") {
		t.Fatalf("pdf marker lacks extension:\n%s", res.Output)
	}
}
