// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package client

import (
	"context"
	"encoding/json"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// sdkClient adapts an official-go-sdk MCP client session to the client.Client
// interface used by the Manager. Both transports (stdio via CommandTransport,
// remote via StreamableClientTransport with SSE fallback) share this adapter;
// only the transport construction differs (newSDKClientForConfig).
type sdkClient struct {
	client  *sdk.Client
	session *sdk.ClientSession
}

// clientInfo identifies Goa to MCP servers during the handshake.
var clientInfo = &sdk.Implementation{Name: "goa", Version: "0.1"}

// connect establishes the session over the given transport.
func (c *sdkClient) connect(ctx context.Context, t sdk.Transport) error {
	c.client = sdk.NewClient(clientInfo, nil)
	session, err := c.client.Connect(ctx, t, nil)
	if err != nil {
		return err
	}
	c.session = session
	return nil
}

// Initialize implements Client. The go-sdk performs the MCP handshake during
// Connect, so this is a no-op kept for interface compatibility.
func (c *sdkClient) Initialize(ctx context.Context) error { return nil }

// ListTools implements Client, following cursor pagination via the SDK's
// Tools iterator.
func (c *sdkClient) ListTools(ctx context.Context) ([]ToolInfo, error) {
	if c.session == nil {
		return nil, fmt.Errorf("mcp client not connected")
	}
	var out []ToolInfo
	for tool, err := range c.session.Tools(ctx, nil) {
		if err != nil {
			return nil, fmt.Errorf("list tools: %w", err)
		}
		out = append(out, ToolInfo{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: schemaToMap(tool.InputSchema),
		})
	}
	return out, nil
}

// CallTool implements Client, converting the MCP content result to text.
func (c *sdkClient) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	if c.session == nil {
		return "", fmt.Errorf("mcp client not connected")
	}
	res, err := c.session.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return "", err
	}
	text := resultText(res)
	if res.IsError {
		if text == "" {
			text = "MCP tool returned an error"
		}
		return "", fmt.Errorf("%s", text)
	}
	return text, nil
}

// Close implements Client.
func (c *sdkClient) Close() error {
	if c.session == nil {
		return nil
	}
	return c.session.Close()
}

// schemaToMap converts the SDK's InputSchema (any, typically a JSON Schema
// object) into the map[string]any the agentic layer expects.
func schemaToMap(s any) map[string]any {
	if s == nil {
		return nil
	}
	if m, ok := s.(map[string]any); ok {
		return m
	}
	data, err := json.Marshal(s)
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil
	}
	return m
}

// resultText flattens a CallToolResult into a single string: it concatenates
// text content blocks; when there are none but StructuredContent is present it
// returns the JSON encoding of the structured content (OpenCode convertTool
// parity, mirrored in catalog.Text for the hand-rolled path).
func resultText(res *sdk.CallToolResult) string {
	var text string
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			text += tc.Text
		}
	}
	if text != "" {
		return text
	}
	if res.StructuredContent != nil {
		if data, err := json.Marshal(res.StructuredContent); err == nil {
			return string(data)
		}
	}
	return ""
}
