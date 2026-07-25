// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/mcp/client"
	"github.com/pijalu/goa/tools"
)

// ServerState is the lifecycle state of one MCP server connection.
type ServerState string

const (
	// StateConnected means the client is live and its tools are registered.
	StateConnected ServerState = "connected"
	// StateFailed means the last connect/list attempt failed.
	StateFailed ServerState = "failed"
	// StateDisabled means the server is configured but not connected.
	StateDisabled ServerState = "disabled"
)

// ServerStatus reports the runtime status of one MCP server.
type ServerStatus struct {
	State ServerState
	// Err is the last error message when State is StateFailed.
	Err string
	// Tools is the number of tools currently registered for the server.
	Tools int
}

// Manager manages MCP server connections and exposes their tools.
type Manager struct {
	mu      sync.RWMutex
	clients map[string]client.Client
	status  map[string]ServerStatus
	reg     *tools.ToolRegistry
	factory ClientFactory
	logger  *agentic.Logger
}

// ClientFactory creates a client for a server config.
type ClientFactory func(cfg ServerConfig) (client.Client, error)

// NewManager creates an MCP manager.
func NewManager(reg *tools.ToolRegistry) *Manager {
	return &Manager{
		clients: make(map[string]client.Client),
		status:  make(map[string]ServerStatus),
		reg:     reg,
		factory: defaultFactory,
	}
}

// SetLogger configures a logger used to surface non-fatal errors such as
// server Close() failures. Passing nil disables logging.
func (m *Manager) SetLogger(l *agentic.Logger) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logger = l
}

func defaultFactory(cfg ServerConfig) (client.Client, error) {
	c := newClient(cfg)
	if err := c.Initialize(context.Background()); err != nil {
		return nil, err
	}
	return c, nil
}

// newClient builds an uninitialized client for the server's transport.
func newClient(cfg ServerConfig) client.Client {
	if cfg.IsRemote() {
		return client.NewHTTPClient(cfg.URL, client.HTTPOptions{
			Headers: cfg.Headers,
			Timeout: cfg.EffectiveTimeout(),
		})
	}
	return client.NewStdioClient(cfg.Command, cfg.Args,
		client.StdioOptions{Cwd: cfg.Cwd, Env: cfg.Env})
}

// SetClientFactory overrides the client factory (useful for tests).
func (m *Manager) SetClientFactory(f ClientFactory) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.factory = f
}

// Connect starts a server connection and registers its tools.
func (m *Manager) Connect(ctx context.Context, cfg ServerConfig) error {
	m.mu.Lock()
	factory := m.factory
	m.mu.Unlock()

	c, err := factory(cfg)
	if err != nil {
		m.setStatus(cfg.Name, ServerStatus{State: StateFailed, Err: err.Error()})
		return fmt.Errorf("connect to %q: %w", cfg.Name, err)
	}

	toolsInfo, err := c.ListTools(ctx)
	if err != nil {
		_ = c.Close()
		m.setStatus(cfg.Name, ServerStatus{State: StateFailed, Err: err.Error()})
		return fmt.Errorf("list tools from %q: %w", cfg.Name, err)
	}

	m.mu.Lock()
	if old, ok := m.clients[cfg.Name]; ok {
		_ = old.Close()
	}
	m.clients[cfg.Name] = c
	m.mu.Unlock()

	m.registerTools(cfg.Name, toolsInfo)
	m.setStatus(cfg.Name, ServerStatus{State: StateConnected, Tools: len(toolsInfo)})
	return nil
}

// Disconnect closes a server connection and unregisters its tools.
func (m *Manager) Disconnect(name string) {
	m.mu.Lock()
	if c, ok := m.clients[name]; ok {
		_ = c.Close()
		delete(m.clients, name)
	}
	m.status[name] = ServerStatus{State: StateDisabled}
	m.mu.Unlock()
	if m.reg != nil {
		m.reg.UnregisterGroup(toolPrefix(name))
	}
}

// Close shuts down every connected server and unregisters all MCP tools.
func (m *Manager) Close() {
	m.mu.Lock()
	names := make([]string, 0, len(m.clients))
	for n := range m.clients {
		names = append(names, n)
	}
	m.mu.Unlock()
	for _, n := range names {
		m.Disconnect(n)
	}
}

// Call invokes an MCP tool by server and tool name.
func (m *Manager) Call(ctx context.Context, server, toolName string, args map[string]any) (string, error) {
	m.mu.RLock()
	c, ok := m.clients[server]
	m.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("mcp server %q not connected", server)
	}
	return c.CallTool(ctx, toolName, args)
}

// ServerNames returns connected server names.
func (m *Manager) ServerNames() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, 0, len(m.clients))
	for n := range m.clients {
		names = append(names, n)
	}
	return names
}

// Status returns a snapshot of per-server runtime status.
func (m *Manager) Status() map[string]ServerStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]ServerStatus, len(m.status))
	for n, s := range m.status {
		out[n] = s
	}
	return out
}

func (m *Manager) setStatus(name string, s ServerStatus) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status[name] = s
}

func (m *Manager) registerTools(server string, toolsInfo []client.ToolInfo) {
	if m.reg == nil {
		return
	}
	prefix := toolPrefix(server)
	toolList := make([]agentic.Tool, 0, len(toolsInfo))
	for _, info := range toolsInfo {
		toolList = append(toolList, &mcpTool{
			server: server,
			name:   info.Name,
			desc:   info.Description,
			schema: info.InputSchema,
			mgr:    m,
		})
	}
	m.reg.RegisterGroup(prefix, toolList)
}

func toolPrefix(server string) string {
	return fmt.Sprintf("mcp__%s__", server)
}

// mcpTool adapts one MCP tool to the agentic.Tool interface. It implements
// agentic.ContextTool so the agent's turn context (Stop / Ctrl+C) cancels
// in-flight MCP calls.
type mcpTool struct {
	agentic.BaseTool
	server string
	name   string
	desc   string
	schema map[string]any
	mgr    *Manager
}

func (t *mcpTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        toolName(t.server, t.name),
		Description: t.desc,
		Schema:      t.schema,
	}
}

// Execute runs the tool with a background context (base Tool contract).
func (t *mcpTool) Execute(input string) (string, error) {
	return t.ExecuteContext(context.Background(), input)
}

// ExecuteContext runs the tool, propagating ctx cancellation into the MCP call.
func (t *mcpTool) ExecuteContext(ctx context.Context, input string) (string, error) {
	var args map[string]any
	if err := json.Unmarshal([]byte(input), &args); err != nil {
		return "", &internal.ToolError{Tool: t.Schema().Name, Type: "invalid_input", Detail: err.Error(), HintText: "Provide valid JSON arguments."}
	}
	res, err := t.mgr.Call(ctx, t.server, t.name, args)
	if err != nil {
		return "", &internal.ToolError{Tool: t.Schema().Name, Type: "mcp_call_failed", Detail: err.Error(), HintText: "Check the MCP server is running and arguments are valid."}
	}
	return res, nil
}

func toolName(server, name string) string {
	return fmt.Sprintf("mcp__%s__%s", server, name)
}
