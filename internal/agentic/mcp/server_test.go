// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package mcp

import (
	"encoding/json"
	"testing"
)

func TestMCPServer_HandleToolsList(t *testing.T) {
	server := NewMCPServer(nil)

	// Add a test tool
	server.RegisterTool("test_tool", ToolSchema{
		Name:        "test_tool",
		Description: "A test tool",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"input": map[string]string{"type": "string"},
			},
			"required": []string{"input"},
		},
	}, func(args json.RawMessage) (string, error) {
		return `{"result": "success"}`, nil
	})

	// Create request
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "tools/list",
		ID:      1,
	}

	resp := server.HandleRequest(req)

	if resp.Error != nil {
		t.Errorf("unexpected error: %v", resp.Error)
	}

	// Result is typed as ToolsListResult, not map[string]interface{}
	result, ok := resp.Result.(ToolsListResult)
	if !ok {
		t.Fatal("result is not ToolsListResult")
	}

	if len(result.Tools) != 1 {
		t.Errorf("expected 1 tool, got %d", len(result.Tools))
	}
}

func TestMCPServer_HandleToolsCall(t *testing.T) {
	server := NewMCPServer(nil)

	// Add a test tool
	server.RegisterTool("echo", ToolSchema{
		Name:        "echo",
		Description: "Echoes the input",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"message": map[string]string{"type": "string"},
			},
			"required": []string{"message"},
		},
	}, func(args json.RawMessage) (string, error) {
		var input struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return "", err
		}
		return `{"echo": "` + input.Message + `"`, nil
	})

	// Create request
	params := json.RawMessage(`{"name":"echo","args":{"message":"hello"}}`)
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "tools/call",
		Params:  params,
		ID:      1,
	}

	resp := server.HandleRequest(req)

	if resp.Error != nil {
		t.Errorf("unexpected error: %v", resp.Error)
	}

	// Result is typed as ToolsCallResult
	result, ok := resp.Result.(ToolsCallResult)
	if !ok {
		t.Fatal("result is not ToolsCallResult")
	}

	if len(result.Content) == 0 {
		t.Fatal("expected content")
	}

	if result.Content[0].Type != "text" {
		t.Errorf("expected text type, got %s", result.Content[0].Type)
	}
}

func TestMCPServer_HandleMethodNotFound(t *testing.T) {
	server := NewMCPServer(nil)

	req := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "unknown/method",
		ID:      1,
	}

	resp := server.HandleRequest(req)

	if resp.Error == nil {
		t.Error("expected error")
	}

	if resp.Error.Code != MethodNotFoundCode {
		t.Errorf("expected MethodNotFoundCode, got %d", resp.Error.Code)
	}
}

func TestMCPServer_HandleInvalidParams(t *testing.T) {
	server := NewMCPServer(nil)

	// Missing params
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "tools/call",
		ID:      1,
	}

	resp := server.HandleRequest(req)

	if resp.Error == nil {
		t.Error("expected error")
	}

	if resp.Error.Code != InvalidParamsCode {
		t.Errorf("expected InvalidParamsCode, got %d", resp.Error.Code)
	}
}

func TestMCPServer_HandleToolNotFound(t *testing.T) {
	server := NewMCPServer(nil)

	// Try to call a non-existent tool
	params := json.RawMessage(`{"name":"nonexistent","args":{}}`)
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "tools/call",
		Params:  params,
		ID:      1,
	}

	resp := server.HandleRequest(req)

	if resp.Error == nil {
		t.Error("expected error")
	}

	// Should get code -32602 for tool not found (uses NewRPCError)
	if resp.Error.Code != -32602 {
		t.Errorf("expected code -32602, got %d", resp.Error.Code)
	}
}

func TestMCPServer_HandleInvalidJSONRPCVersion(t *testing.T) {
	server := NewMCPServer(nil)

	req := JSONRPCRequest{
		JSONRPC: "1.0",
		Method:  "tools/list",
		ID:      1,
	}

	resp := server.HandleRequest(req)

	if resp.Error == nil {
		t.Error("expected error for invalid JSON-RPC version")
	}

	if resp.Error.Code != ParseErrorCode {
		t.Errorf("expected ParseErrorCode, got %d", resp.Error.Code)
	}
}

func TestMCPServer_HandleHTTP(t *testing.T) {
	server := NewMCPServer(nil)

	// Add a test tool
	server.RegisterTool("hello", ToolSchema{
		Name:        "hello",
		Description: "Says hello",
		InputSchema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	}, func(args json.RawMessage) (string, error) {
		return `"hello world"`, nil
	})

	// Test single request
	body := []byte(`{"jsonrpc":"2.0","method":"tools/list","id":1}`)
	respBytes, err := server.HandleHTTP(body)
	if err != nil {
		t.Errorf("HandleHTTP error: %v", err)
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		t.Errorf("unmarshal error: %v", err)
	}

	if resp.Error != nil {
		t.Errorf("unexpected error: %v", resp.Error)
	}
}

func TestMCPServer_HandleHTTPBatch(t *testing.T) {
	server := NewMCPServer(nil)

	server.RegisterTool("test", ToolSchema{
		Name:        "test",
		Description: "Test tool",
		InputSchema: map[string]interface{}{"type": "object"},
	}, func(args json.RawMessage) (string, error) {
		return `"ok"`, nil
	})

	// Test batch request
	body := []byte(`[
		{"jsonrpc":"2.0","method":"tools/list","id":1},
		{"jsonrpc":"2.0","method":"tools/call","params":{"name":"test","args":{}},"id":2}
	]`)
	respBytes, err := server.HandleHTTP(body)
	if err != nil {
		t.Errorf("HandleHTTP error: %v", err)
	}

	var resps []JSONRPCResponse
	if err := json.Unmarshal(respBytes, &resps); err != nil {
		t.Errorf("unmarshal error: %v", err)
	}

	if len(resps) != 2 {
		t.Errorf("expected 2 responses, got %d", len(resps))
	}
}

func TestMCPServer_HandleHTTPInvalidJSON(t *testing.T) {
	server := NewMCPServer(nil)

	body := []byte(`invalid json`)
	respBytes, err := server.HandleHTTP(body)
	if err != nil {
		t.Errorf("HandleHTTP error: %v", err)
	}

	var resp JSONRPCResponse
	json.Unmarshal(respBytes, &resp)

	if resp.Error == nil {
		t.Error("expected error for invalid JSON")
	}
}
