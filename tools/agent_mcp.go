// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/pijalu/agent-mcp/client"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
)

// shutdownWait is how long the shutdown action waits for the daemon's port
// to stop accepting connections, mirroring `agent-mcp server shutdown`.
const shutdownWait = 10 * time.Second

// AgentMCPBackend is the library surface agent_mcp drives; the real backend
// is an *agent-mcp client.Client, and tests inject fakes. The interface is
// declared here so the tool never names the concrete type.
type AgentMCPBackend interface {
	Do(ctx context.Context, r client.Request) (any, error)
	ShutdownDaemon(ctx context.Context, wait time.Duration) (client.ShutdownResult, error)
	Close() error
	// UpsertServerEntry adds or replaces one server in the agent-mcp
	// config; part of the surface so config_add needs no second handle.
	UpsertServerEntry(path, name string, e client.ServerEntry) (client.UpsertResult, error)
}

// backendAdapter wraps the concrete client so the interface stays honest:
// client.Client satisfies it structurally.
type backendAdapter struct{ c *client.Client }

func (b backendAdapter) Do(ctx context.Context, r client.Request) (any, error) {
	return b.c.Do(ctx, r)
}

func (b backendAdapter) ShutdownDaemon(ctx context.Context, wait time.Duration) (client.ShutdownResult, error) {
	return b.c.ShutdownDaemon(ctx, wait)
}

func (b backendAdapter) Close() error { return b.c.Close() }

func (b backendAdapter) UpsertServerEntry(path, name string, e client.ServerEntry) (client.UpsertResult, error) {
	return client.UpsertServerEntry(path, name, e)
}

// AgentMCPTool drives MCP servers through the agent-mcp client library:
// discovery, tool calls, resources, prompts, config management, and daemon
// lifecycle — against the shared agent-mcp daemon so warm sessions and MCP
// child processes are reused across goa runs.
type AgentMCPTool struct {
	// Options builds the real client; zero value means daemon defaults
	// (connect, auto-start, default config path).
	Options client.Options
	// NewBackend builds the backend; nil means a real client.Client from
	// Options. Tests inject fakes here.
	NewBackend func(client.Options) (AgentMCPBackend, error)

	once    sync.Once
	be      AgentMCPBackend
	initErr error

	mu     sync.Mutex
	notice strings.Builder
}

// Schema returns the tool schema for agent_mcp.
func (t *AgentMCPTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name: "agent_mcp",
		Description: "Drive MCP servers via the agent-mcp daemon: list servers/tools/resources/prompts, " +
			"call tools, read resources, get prompts, add servers to the config, shut the daemon down.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type": "string",
					"enum": []string{"list_servers", "list_tools", "call_tool", "list_resources",
						"read_resource", "list_prompts", "get_prompt", "config_add", "shutdown"},
					"description": "operation to run",
				},
				"server": map[string]any{"type": "string", "description": "MCP server name (required except list_servers/shutdown/config_add)"},
				"tool":   map[string]any{"type": "string", "description": "tool name (call_tool)"},
				"args":   map[string]any{"type": "object", "description": "arguments: JSON object for call_tool, string map for get_prompt"},
				"uri":    map[string]any{"type": "string", "description": "resource URI (read_resource)"},
				"prompt": map[string]any{"type": "string", "description": "prompt name (get_prompt)"},
				"cursor": map[string]any{"type": "string", "description": "pagination cursor from a previous list result"},
				"search": map[string]any{"type": "string", "description": "filter discovery lists by name/description/title/URI"},
				"limit":  map[string]any{"type": "integer", "description": "cap discovery list entries (0 = all)"},
				"raw":    map[string]any{"type": "boolean", "description": "return the untouched MCP payload (no totals, no metadata strip)"},
				"name":   map[string]any{"type": "string", "description": "server name (config_add)"},
				"argv":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "stdio server executable arguments (config_add)"},
				"command": map[string]any{
					"type":        "string",
					"description": "stdio server executable (config_add)",
				},
				"env":       map[string]any{"type": "object", "description": "stdio server environment (config_add)"},
				"url":       map[string]any{"type": "string", "description": "remote server URL (config_add; alternative to command)"},
				"transport": map[string]any{"type": "string", "enum": []string{"sse", "streamable"}, "description": "remote transport (config_add; default streamable)"},
				"headers":   map[string]any{"type": "object", "description": "remote server headers (config_add)"},
				"config_path": map[string]any{
					"type":        "string",
					"description": "agent-mcp config.toml path (defaults to the agent-mcp default location)",
				},
			},
			"required": []string{"action"},
		},
	}
}

// agentMCPParams holds the parsed input for AgentMCPTool.
type agentMCPParams struct {
	Action     string            `json:"action"`
	Server     string            `json:"server"`
	Tool       string            `json:"tool"`
	Args       json.RawMessage   `json:"args"`
	URI        string            `json:"uri"`
	Prompt     string            `json:"prompt"`
	Cursor     string            `json:"cursor"`
	Search     string            `json:"search"`
	Limit      int               `json:"limit"`
	Raw        bool              `json:"raw"`
	Name       string            `json:"name"`
	Command    string            `json:"command"`
	Argv       []string          `json:"argv"`
	Env        map[string]string `json:"env"`
	URL        string            `json:"url"`
	Transport  string            `json:"transport"`
	Headers    map[string]string `json:"headers"`
	ConfigPath string            `json:"config_path"`
}

// agentMCPDispatch maps the generic discovery/invoke actions onto library
// ops; list_servers, config_add and shutdown need bespoke request wiring.
var agentMCPDispatch = map[string]client.Op{
	"list_tools":     client.OpListTools,
	"call_tool":      client.OpCallTool,
	"list_resources": client.OpListResources,
	"read_resource":  client.OpReadResource,
	"list_prompts":   client.OpListPrompts,
	"get_prompt":     client.OpGetPrompt,
}

// Execute runs the requested agent-mcp action.
func (t *AgentMCPTool) Execute(input string) (string, error) {
	var p agentMCPParams
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		return "", &internal.ToolError{
			Tool: "agent_mcp", Type: "invalid_input",
			Detail:   fmt.Sprintf("Cannot parse parameters: %v", err),
			HintText: "Ensure your input is valid JSON with the required fields.",
		}
	}
	be, err := t.ensureBackend()
	if err != nil {
		return "", t.toolError(err)
	}
	ctx := context.Background()
	if op, ok := agentMCPDispatch[p.Action]; ok {
		return t.run(ctx, be, t.request(p, op))
	}
	switch p.Action {
	case "list_servers":
		return t.run(ctx, be, client.Request{Op: client.OpListServers, Search: p.Search, Limit: p.Limit, Raw: p.Raw})
	case "config_add":
		return t.configAdd(be, p)
	case "shutdown":
		return t.shutdown(ctx, be)
	case "":
		return "", t.badAction("Missing action")
	}
	return "", t.badAction(fmt.Sprintf("Unknown action: %s", p.Action))
}

// badAction reports an unknown or missing action.
func (t *AgentMCPTool) badAction(detail string) error {
	return &internal.ToolError{
		Tool: "agent_mcp", Type: "invalid_input",
		Detail: detail,
		HintText: "Use one of: list_servers, list_tools, call_tool, list_resources, " +
			"read_resource, list_prompts, get_prompt, config_add, shutdown",
	}
}

// request maps parsed params onto a library request.
func (t *AgentMCPTool) request(p agentMCPParams, op client.Op) client.Request {
	return client.Request{
		Op:     op,
		Server: p.Server,
		Tool:   p.Tool,
		URI:    p.URI,
		Prompt: p.Prompt,
		Args:   p.Args,
		Cursor: p.Cursor,
		Search: p.Search,
		Limit:  p.Limit,
		Raw:    p.Raw,
	}
}

// run executes one library request and renders the shaped payload as JSON.
func (t *AgentMCPTool) run(ctx context.Context, be AgentMCPBackend, r client.Request) (string, error) {
	result, err := be.Do(ctx, r)
	if err != nil {
		return "", t.toolError(err)
	}
	body, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", &internal.ToolError{
			Tool: "agent_mcp", Type: "invalid_input",
			Detail:   fmt.Sprintf("Cannot encode result: %v", err),
			HintText: "Retry with the raw flag if the payload contains non-JSON data.",
		}
	}
	return t.tagResult(string(body)), nil
}

// configAdd upserts one server entry in the agent-mcp config.
func (t *AgentMCPTool) configAdd(be AgentMCPBackend, p agentMCPParams) (string, error) {
	if p.Name == "" || (p.Command == "" && p.URL == "") {
		return "", &internal.ToolError{
			Tool: "agent_mcp", Type: "invalid_params",
			Detail:   "config_add requires name and either command or url",
			HintText: "Pass name plus command/argv (stdio) or url/transport/headers (remote).",
		}
	}
	res, err := be.UpsertServerEntry(p.ConfigPath, p.Name, client.ServerEntry{
		Command:   p.Command,
		Args:      p.Argv,
		Env:       p.Env,
		URL:       p.URL,
		Transport: p.Transport,
		Headers:   p.Headers,
	})
	if err != nil {
		return "", t.toolError(err)
	}
	body, _ := json.MarshalIndent(res, "", "  ")
	return t.tagResult(string(body)), nil
}

// shutdown asks the daemon to tear down; idempotent when absent.
func (t *AgentMCPTool) shutdown(ctx context.Context, be AgentMCPBackend) (string, error) {
	res, err := be.ShutdownDaemon(ctx, shutdownWait)
	if err != nil {
		return "", t.toolError(err)
	}
	body, _ := json.MarshalIndent(res, "", "  ")
	return t.tagResult(string(body)), nil
}

// tagResult appends any buffered daemon notices so the agent sees the
// lifecycle hint (auto-start) inline with the result.
func (t *AgentMCPTool) tagResult(body string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.notice.Len() == 0 {
		return body
	}
	out := body + "\n\n" + strings.TrimRight(t.notice.String(), "\n")
	t.notice.Reset()
	return out
}

// ensureBackend builds or returns the backend, once. Options.Stderr is
// always rewired to the tool's capture first, so every backend — default or
// injected — feeds daemon notices into results.
func (t *AgentMCPTool) ensureBackend() (AgentMCPBackend, error) {
	t.once.Do(func() {
		newBackend := t.NewBackend
		if newBackend == nil {
			newBackend = func(o client.Options) (AgentMCPBackend, error) {
				c, err := client.New(o)
				if err != nil {
					return nil, err
				}
				return backendAdapter{c: c}, nil
			}
		}
		t.Options.Stderr = &agentMCPNotice{t: t}
		t.be, t.initErr = newBackend(t.Options)
	})
	return t.be, t.initErr
}

// agentMCPNotice captures the client library's stderr notices.
type agentMCPNotice struct{ t *AgentMCPTool }

func (w *agentMCPNotice) Write(p []byte) (int, error) {
	w.t.mu.Lock()
	defer w.t.mu.Unlock()
	return w.t.notice.Write(p)
}

// Close tears down the backend's sessions/connections. Safe to call even
// when the backend never initialized; also safe to call more than once.
func (t *AgentMCPTool) Close() error {
	be, err := t.ensureBackend()
	if err != nil {
		return err
	}
	return be.Close()
}

// toolError maps the library's error taxonomy onto goa's tool errors.
func (t *AgentMCPTool) toolError(err error) error {
	kind := "mcp_error"
	hint := "Check that the MCP server is configured and reachable: use action list_servers."
	switch {
	case errors.Is(err, client.ErrUsage):
		kind = "invalid_params"
		hint = "Fix the parameters: server, tool, uri, and prompt are required per action; config_add needs name plus command or url."
	case errors.Is(err, client.ErrInternal):
		kind = "internal_error"
		hint = "The agent-mcp daemon or client failed unexpectedly; retry or use the agent-mcp CLI to diagnose."
	}
	return &internal.ToolError{
		Tool: "agent_mcp", Type: kind,
		Detail:   err.Error(),
		HintText: hint,
	}
}

func (t *AgentMCPTool) IsRetryable(err error) bool { return false }

//go:embed agent_mcp.short.md agent_mcp.long.md
var agentMCPDocs embed.FS

func (t *AgentMCPTool) ShortDoc() string { return readDoc(agentMCPDocs, "agent_mcp.short.md") }
func (t *AgentMCPTool) LongDoc() string  { return readDoc(agentMCPDocs, "agent_mcp.long.md") }

func (t *AgentMCPTool) Examples() []string {
	return []string{
		`{"action": "list_servers"}`,
		`{"action": "list_tools", "server": "github"}`,
		`{"action": "call_tool", "server": "github", "tool": "search_issues", "args": {"query": "resize bug"}}`,
		`{"action": "read_resource", "server": "docs", "uri": "file:///docs/README.md"}`,
		`{"action": "get_prompt", "server": "docs", "prompt": "summarize", "args": {"topic": "resize"}}`,
		`{"action": "config_add", "name": "github", "command": "npx", "argv": ["-y", "@modelcontextprotocol/server-github"]}`,
	}
}
