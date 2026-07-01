// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package mcp

import (
	"context"
	"testing"

	"github.com/pijalu/goa/internal/mcp/client"
	"github.com/pijalu/goa/tools"
)

type mockClient struct {
	tools []client.ToolInfo
	call  string
}

func (m *mockClient) Initialize(ctx context.Context) error { return nil }
func (m *mockClient) ListTools(ctx context.Context) ([]client.ToolInfo, error) {
	return m.tools, nil
}
func (m *mockClient) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	m.call = name
	return "ok", nil
}
func (m *mockClient) Close() error { return nil }

func TestManagerConnectAndCall(t *testing.T) {
	reg := tools.NewToolRegistry()
	mgr := NewManager(reg)
	mgr.SetClientFactory(func(cfg ServerConfig) (client.Client, error) {
		return &mockClient{tools: []client.ToolInfo{{Name: "read", Description: "read file"}}}, nil
	})

	if err := mgr.Connect(context.Background(), ServerConfig{Name: "fs", Command: "npx"}); err != nil {
		t.Fatalf("connect: %v", err)
	}

	if len(mgr.ServerNames()) != 1 {
		t.Errorf("servers = %v", mgr.ServerNames())
	}

	if _, ok := reg.Get("mcp__fs__read"); !ok {
		t.Error("expected mcp__fs__read tool registered")
	}

	res, err := mgr.Call(context.Background(), "fs", "read", map[string]any{"path": "/tmp/x"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res != "ok" {
		t.Errorf("res = %q", res)
	}
}

func TestManagerDisconnect(t *testing.T) {
	reg := tools.NewToolRegistry()
	mgr := NewManager(reg)
	mgr.SetClientFactory(func(cfg ServerConfig) (client.Client, error) {
		return &mockClient{tools: []client.ToolInfo{{Name: "read", Description: "read file"}}}, nil
	})
	_ = mgr.Connect(context.Background(), ServerConfig{Name: "fs", Command: "npx"})
	mgr.Disconnect("fs")
	if _, ok := reg.Get("mcp__fs__read"); ok {
		t.Error("expected tool unregistered")
	}
}
