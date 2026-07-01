// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package client

import "context"

// ToolInfo describes an MCP tool.
type ToolInfo struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// Client is an MCP client connection.
type Client interface {
	// Initialize performs the MCP handshake.
	Initialize(ctx context.Context) error
	// ListTools returns the tools exposed by the server.
	ListTools(ctx context.Context) ([]ToolInfo, error)
	// CallTool invokes a tool with the given arguments.
	CallTool(ctx context.Context, name string, args map[string]any) (string, error)
	// Close shuts down the client.
	Close() error
}
