package tools

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pijalu/agent-mcp/client"
)

func TestAgentMCPStdioDaemonFixture(t *testing.T) {
	if os.Getenv("GOA_AGENT_MCP_FIXTURE") != "1" {
		return
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "goa-embedded-fixture", Version: "v1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "echoes text"},
		func(_ context.Context, _ *mcp.CallToolRequest, in struct {
			Text string `json:"text"`
		}) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: in.Text}},
			}, nil, nil
		})
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		t.Fatalf("fixture server: %v", err)
	}
}

func TestAgentMCPEmbeddedDaemonWithoutExecutable(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the real Goa binary")
	}
	goaBinary := buildGoaForMCP(t)
	configPath := writeMCPDaemonConfig(t)
	address := reserveMCPAddress(t)
	t.Setenv("PATH", t.TempDir()) // no agent-mcp executable or other helper
	tool := &AgentMCPTool{Options: client.Options{
		ConfigPath:     configPath,
		ConnectAddr:    address,
		APIKey:         "integration-key",
		ConnectTimeout: 5 * time.Second,
		SpawnDaemon: func(o client.Options) (client.DaemonChild, error) {
			return client.SpawnDaemonWithCommand(o, goaBinary, "mcp", "daemon")
		},
	}}
	defer tool.Close()
	t.Cleanup(func() { _, _ = tool.Execute(`{"action":"shutdown"}`) })

	out := executeMCPForTest(t, tool, `{"action":"list_tools","server":"chrome","limit":0}`)
	assertMCPOutputContains(t, out, "echo", "embedded daemon")
	out = executeMCPForTest(t, tool, `{"action":"call_tool","server":"chrome","tool":"echo","args":{"text":"embedded-goa"}}`)
	assertMCPOutputContains(t, out, "embedded-goa")
	out = executeMCPForTest(t, tool, `{"action":"shutdown"}`)
	assertMCPOutputContains(t, out, `"shutdown": true`)
	out = executeMCPForTest(t, tool, `{"action":"shutdown"}`)
	assertMCPOutputContains(t, out, `"shutdown": false`)

	// The long-lived tool must recover after its cached daemon transport dies.
	// Reconnect happens before invocation, then auto-starts a fresh daemon.
	out = executeMCPForTest(t, tool, `{"action":"list_tools","server":"chrome","limit":0}`)
	assertMCPOutputContains(t, out, "echo", "embedded daemon")
	out = executeMCPForTest(t, tool, `{"action":"call_tool","server":"chrome","tool":"echo","args":{"text":"reconnected-goa"}}`)
	assertMCPOutputContains(t, out, "reconnected-goa")
	out = executeMCPForTest(t, tool, `{"action":"shutdown"}`)
	assertMCPOutputContains(t, out, `"shutdown": true`)
}

func buildGoaForMCP(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	goaBinary := filepath.Join(t.TempDir(), "goa")
	build := exec.Command("go", "build", "-o", goaBinary, "./cmd/goa")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build Goa: %v\n%s", err, output)
	}
	return goaBinary
}

func writeMCPDaemonConfig(t *testing.T) string {
	t.Helper()
	fixtureBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "config.toml")
	config := fmt.Sprintf("[mcpServers.chrome]\ncommand = %q\nargs = [%q]\n", fixtureBinary, "-test.run=^TestAgentMCPStdioDaemonFixture$")
	config += "env = { GOA_AGENT_MCP_FIXTURE = \"1\" }\n"
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	return configPath
}

func executeMCPForTest(t *testing.T, tool *AgentMCPTool, input string) string {
	t.Helper()
	out, err := tool.Execute(input)
	if err != nil {
		t.Fatalf("Execute(%s): %v", input, err)
	}
	return out
}

func assertMCPOutputContains(t *testing.T, out string, parts ...string) {
	t.Helper()
	for _, part := range parts {
		if !strings.Contains(out, part) {
			t.Fatalf("output = %s, want %q", out, part)
		}
	}
}

func reserveMCPAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}
