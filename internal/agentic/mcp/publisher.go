// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package mcp

import (
	"encoding/json"

	"github.com/pijalu/goa/internal/agentic"
)

// MCPToolPublisher wraps a ToolRegistry and exposes tools via MCP protocol.
type MCPToolPublisher struct {
	registry *agentic.ToolRegistry
	server   *MCPServer
}

// NewMCPToolPublisher creates a new MCPToolPublisher from a ToolRegistry.
func NewMCPToolPublisher(registry *agentic.ToolRegistry) *MCPToolPublisher {
	server := NewMCPServer(nil)

	// Register all tools from the registry
	schemas := registry.Schemas()
	for _, schema := range schemas {
		tool, ok := registry.Get(schema.Name)
		if !ok {
			continue
		}

		// Convert agentic.ToolSchema to mcp.ToolSchema
		mcpSchema := ToolSchema{
			Name:        schema.Name,
			Description: schema.Description,
			InputSchema: schema.Schema,
		}

		// Create handler that wraps tool.Execute
		handler := func(args json.RawMessage) (string, error) {
			return tool.Execute(string(args))
		}

		server.RegisterTool(schema.Name, mcpSchema, handler)
	}

	return &MCPToolPublisher{
		registry: registry,
		server:   server,
	}
}

// HandleHTTP processes an HTTP request containing JSON-RPC.
func (p *MCPToolPublisher) HandleHTTP(body []byte) ([]byte, error) {
	return p.server.HandleHTTP(body)
}

// HandleRequest processes a JSON-RPC request directly.
func (p *MCPToolPublisher) HandleRequest(req JSONRPCRequest) JSONRPCResponse {
	return p.server.HandleRequest(req)
}

// GetServer returns the underlying MCPServer.
func (p *MCPToolPublisher) GetServer() *MCPServer {
	return p.server
}
