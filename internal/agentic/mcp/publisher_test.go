// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package mcp

import (
	"encoding/json"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
)

type mockTool struct {
	name        string
	description string
	schema      map[string]interface{}
	result      string
}

func (m mockTool) IsRetryable(err error) bool { return false }

func (m mockTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        m.name,
		Description: m.description,
		Schema:      m.schema,
	}
}

func (m mockTool) Execute(input string) (string, error) {
	return m.result, nil
}

func TestMCPToolPublisher_New(t *testing.T) {
	registry := agentic.NewToolRegistry([]agentic.Tool{
		mockTool{
			name:        "calculator",
			description: "Performs calculations",
			schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"a": map[string]string{"type": "number"},
					"b": map[string]string{"type": "number"},
				},
			},
			result: "42",
		},
	})

	publisher := NewMCPToolPublisher(registry)
	if publisher == nil {
		t.Fatal("publisher is nil")
	}

	// Verify server has the tool
	srv := publisher.GetServer()
	tools := srv.toolsMap()
	if len(tools) != 1 {
		t.Errorf("expected 1 tool, got %d", len(tools))
	}

	if _, ok := tools["calculator"]; !ok {
		t.Error("calculator tool not registered")
	}
}

func TestMCPToolPublisher_HandleRequest(t *testing.T) {
	registry := agentic.NewToolRegistry([]agentic.Tool{
		mockTool{
			name:        "echo",
			description: "Echoes input",
			schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"message": map[string]string{"type": "string"},
				},
			},
			result: `{"echo":"hello"}`,
		},
	})

	publisher := NewMCPToolPublisher(registry)

	// Test tools/list
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "tools/list",
		ID:      1,
	}

	resp := publisher.HandleRequest(req)
	if resp.Error != nil {
		t.Errorf("tools/list error: %v", resp.Error)
	}

	// Test tools/call
	params := json.RawMessage(`{"name":"echo","args":{"message":"hello"}}`)
	req = JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "tools/call",
		Params:  params,
		ID:      2,
	}

	resp = publisher.HandleRequest(req)
	if resp.Error != nil {
		t.Errorf("tools/call error: %v", resp.Error)
	}

	result, ok := resp.Result.(ToolsCallResult)
	if !ok {
		t.Fatalf("result is not ToolsCallResult, got %T", resp.Result)
	}

	if len(result.Content) == 0 {
		t.Fatal("expected content")
	}
}

func TestMCPToolPublisher_HandleHTTP(t *testing.T) {
	registry := agentic.NewToolRegistry([]agentic.Tool{
		mockTool{
			name:        "test",
			description: "Test tool",
			schema:      map[string]interface{}{"type": "object"},
			result:      "test result",
		},
	})

	publisher := NewMCPToolPublisher(registry)

	body := []byte(`{"jsonrpc":"2.0","method":"tools/list","id":1}`)
	respBody, err := publisher.HandleHTTP(body)
	if err != nil {
		t.Fatal(err)
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		t.Fatal(err)
	}

	if resp.Error != nil {
		t.Errorf("unexpected error: %v", resp.Error)
	}
}
