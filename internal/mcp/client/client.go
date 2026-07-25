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
	// Instructions returns the server's usage instructions from the handshake
	// (empty when the server provides none).
	Instructions() string
	// Close shuts down the client.
	Close() error
}

// NotificationHandler is an optional extension a Client can implement to
// receive server-initiated lifecycle events.
type NotificationHandler interface {
	// SetToolListChangedHandler registers a callback invoked when the server
	// sends notifications/tools/list_changed. The client should re-list tools
	// and swap the registered group.
	SetToolListChangedHandler(fn func(ctx context.Context))
	// SetLoggingHandler registers a callback invoked when the server sends a
	// log notification (notifications/message).
	SetLoggingHandler(fn func(ctx context.Context, level, logger, message string))
	// AddRoot advertises a filesystem root to the server. The SDK auto-answers
	// roots/list requests with the registered roots.
	AddRoot(uri string)
}
