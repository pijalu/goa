// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package client

import (
	"context"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// newTestServerClient builds an in-process MCP server with one "greet" tool
// and an sdkClient connected to it over the SDK's in-memory transport. It
// exercises the full client.Client surface (ListTools/CallTool) against a real
// protocol peer without spawning processes or binding ports.
func newTestServerClient(t *testing.T) *sdkClient {
	t.Helper()
	server := sdk.NewServer(&sdk.Implementation{Name: "test-server", Version: "v0.0.1"}, nil)
	sdk.AddTool(server, &sdk.Tool{Name: "greet", Description: "greet someone"},
		func(_ context.Context, _ *sdk.CallToolRequest, in struct {
			Name string `json:"name"`
		}) (*sdk.CallToolResult, any, error) {
			return &sdk.CallToolResult{
				Content: []sdk.Content{&sdk.TextContent{Text: "hi " + in.Name}},
			}, nil, nil
		})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	st, ct := sdk.NewInMemoryTransports()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	c := &sdkClient{}
	if err := c.connect(ctx, ct); err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestSDKClientListTools(t *testing.T) {
	c := newTestServerClient(t)
	tools, err := c.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "greet" {
		t.Fatalf("tools = %+v", tools)
	}
	if tools[0].Description != "greet someone" {
		t.Errorf("description = %q", tools[0].Description)
	}
	if tools[0].InputSchema == nil {
		t.Error("expected a non-nil input schema")
	}
}

func TestSDKClientCallTool(t *testing.T) {
	c := newTestServerClient(t)
	out, err := c.CallTool(context.Background(), "greet", map[string]any{"name": "goa"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if out != "hi goa" {
		t.Errorf("output = %q, want %q", out, "hi goa")
	}
}

func TestSDKClientCallUnknownTool(t *testing.T) {
	c := newTestServerClient(t)
	if _, err := c.CallTool(context.Background(), "nope", nil); err == nil {
		t.Error("expected error calling unknown tool")
	}
}

func TestSchemaToMap(t *testing.T) {
	m := schemaToMap(map[string]any{"type": "object"})
	if m["type"] != "object" {
		t.Errorf("schemaToMap = %v", m)
	}
	if schemaToMap(nil) != nil {
		t.Error("nil schema should map to nil")
	}
}
