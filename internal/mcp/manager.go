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

// Manager manages MCP server connections and exposes their tools.
type Manager struct {
	mu      sync.RWMutex
	clients map[string]client.Client
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
	c := client.NewStdioClient(cfg.Command, cfg.Args)
	if err := c.Initialize(context.Background()); err != nil {
		return nil, err
	}
	return c, nil
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
		return fmt.Errorf("connect to %q: %w", cfg.Name, err)
	}

	toolsInfo, err := c.ListTools(ctx)
	if err != nil {
		_ = c.Close()
		return fmt.Errorf("list tools from %q: %w", cfg.Name, err)
	}

	m.mu.Lock()
	if old, ok := m.clients[cfg.Name]; ok {
		_ = old.Close()
	}
	m.clients[cfg.Name] = c
	m.mu.Unlock()

	m.registerTools(cfg.Name, toolsInfo)
	return nil
}

// Disconnect closes a server connection and unregisters its tools.
func (m *Manager) Disconnect(name string) {
	m.mu.Lock()
	if c, ok := m.clients[name]; ok {
		_ = c.Close()
		delete(m.clients, name)
	}
	m.mu.Unlock()
	if m.reg != nil {
		m.reg.UnregisterGroup(toolPrefix(name))
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

func (m *Manager) registerTools(server string, toolsInfo []client.ToolInfo) {
	if m.reg == nil {
		return
	}
	prefix := toolPrefix(server)
	var toolList []agentic.Tool
	for _, info := range toolsInfo {
		t := &mcpTool{
			server: server,
			name:   info.Name,
			desc:   info.Description,
			schema: info.InputSchema,
			mgr:    m,
		}
		toolList = append(toolList, t)
	}
	m.reg.RegisterGroup(prefix, toolList)
}

func toolPrefix(server string) string {
	return fmt.Sprintf("mcp__%s__", server)
}

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

func (t *mcpTool) Execute(input string) (string, error) {
	var args map[string]any
	if err := json.Unmarshal([]byte(input), &args); err != nil {
		return "", &internal.ToolError{Tool: t.Schema().Name, Type: "invalid_input", Detail: err.Error(), HintText: "Provide valid JSON arguments."}
	}
	res, err := t.mgr.Call(context.Background(), t.server, t.name, args)
	if err != nil {
		return "", &internal.ToolError{Tool: t.Schema().Name, Type: "mcp_call_failed", Detail: err.Error(), HintText: "Check the MCP server is running and arguments are valid."}
	}
	return res, nil
}

func toolName(server, name string) string {
	return fmt.Sprintf("mcp__%s__%s", server, name)
}
