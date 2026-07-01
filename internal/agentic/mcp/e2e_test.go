// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
)

// mockToolForE2E is a test tool implementation.
type mockToolForE2E struct {
	name        string
	description string
	schema      map[string]interface{}
	executeFunc func(input string) (string, error)
}

func (m mockToolForE2E) IsRetryable(err error) bool { return false }

func (m mockToolForE2E) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        m.name,
		Description: m.description,
		Schema:      m.schema,
	}
}

func (m mockToolForE2E) Execute(input string) (string, error) {
	return m.executeFunc(input)
}

// TestMCPToolPublisher_E2E tests the full MCP flow via HTTP.
func TestMCPToolPublisher_E2E(t *testing.T) {
	registry := newAddMultiplyRegistry()
	publisher := NewMCPToolPublisher(registry)
	server := newMCPE2EServer(publisher)
	defer server.Close()

	t.Run("tools_list", func(t *testing.T) { testToolsList(t, server.URL) })
	t.Run("tools_call_add", func(t *testing.T) { testToolCallAdd(t, server.URL) })
	t.Run("tools_call_multiply", func(t *testing.T) { testToolCallMultiply(t, server.URL) })
	t.Run("invalid_method", func(t *testing.T) { testInvalidMethod(t, server.URL) })
	t.Run("invalid_params", func(t *testing.T) { testInvalidParams(t, server.URL) })
}

func newAddMultiplyRegistry() *agentic.ToolRegistry {
	return agentic.NewToolRegistry([]agentic.Tool{
		newMathMockTool("add", "Adds two numbers", func(a, b float64) float64 { return a + b }),
		newMathMockTool("multiply", "Multiplies two numbers", func(a, b float64) float64 { return a * b }),
	})
}

func newMathMockTool(name, description string, op func(float64, float64) float64) agentic.Tool {
	return mockToolForE2E{
		name:        name,
		description: description,
		schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"a": map[string]string{"type": "number"},
				"b": map[string]string{"type": "number"},
			},
			"required": []string{"a", "b"},
		},
		executeFunc: func(input string) (string, error) {
			var args struct {
				A float64 `json:"a"`
				B float64 `json:"b"`
			}
			if err := json.Unmarshal([]byte(input), &args); err != nil {
				return "", err
			}
			result, err := json.Marshal(map[string]interface{}{
				"result": op(args.A, args.B),
			})
			return string(result), err
		},
	}
}

func newMCPE2EServer(publisher *MCPToolPublisher) *httptest.Server {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, r.ContentLength)
		r.Body.Read(body)

		respBody, err := publisher.HandleHTTP(body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write(respBody)
	})
	return httptest.NewServer(handler)
}

func testToolsList(t *testing.T, baseURL string) {
	resp, err := postMCP(baseURL, `{"jsonrpc":"2.0","method":"tools/list","id":1}`)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	rpcResp := decodeMCPResponse(t, resp)
	if rpcResp.Error != nil {
		t.Errorf("unexpected error: %v", rpcResp.Error)
	}

	result := rpcResp.Result.(map[string]interface{})
	tools := result["tools"].([]interface{})
	if len(tools) != 2 {
		t.Errorf("expected 2 tools, got %d", len(tools))
	}
}

func testToolCallAdd(t *testing.T, baseURL string) {
	resp, err := postMCP(baseURL, `{"jsonrpc":"2.0","method":"tools/call","params":{"name":"add","args":{"a":5,"b":3}},"id":2}`)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	rpcResp := decodeMCPResponse(t, resp)
	if rpcResp.Error != nil {
		t.Errorf("unexpected error: %v", rpcResp.Error)
	}

	resultMap := rpcResp.Result.(map[string]interface{})
	content := resultMap["content"].([]interface{})
	if len(content) == 0 {
		t.Fatal("empty content")
	}
}

func testToolCallMultiply(t *testing.T, baseURL string) {
	resp, err := postMCP(baseURL, `{"jsonrpc":"2.0","method":"tools/call","params":{"name":"multiply","args":{"a":4,"b":7}},"id":3}`)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	rpcResp := decodeMCPResponse(t, resp)
	if rpcResp.Error != nil {
		t.Errorf("unexpected error: %v", rpcResp.Error)
	}
}

func testInvalidMethod(t *testing.T, baseURL string) {
	resp, err := postMCP(baseURL, `{"jsonrpc":"2.0","method":"invalid/method","id":4}`)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	rpcResp := decodeMCPResponse(t, resp)
	if rpcResp.Error == nil {
		t.Error("expected error")
	}
	if rpcResp.Error.Code != MethodNotFoundCode {
		t.Errorf("expected MethodNotFoundCode, got %d", rpcResp.Error.Code)
	}
}

func testInvalidParams(t *testing.T, baseURL string) {
	resp, err := postMCP(baseURL, `{"jsonrpc":"2.0","method":"tools/call","params":{"name":"add"},"id":5}`)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	rpcResp := decodeMCPResponse(t, resp)
	if rpcResp.Error == nil {
		t.Error("expected error")
	}
}

func postMCP(baseURL, body string) (*http.Response, error) {
	return http.Post(baseURL, "application/json", bytes.NewBufferString(body))
}

func decodeMCPResponse(t *testing.T, resp *http.Response) JSONRPCResponse {
	var rpcResp JSONRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		t.Fatal(err)
	}
	return rpcResp
}

// TestMCPServer_BatchRequests tests batch JSON-RPC requests.
func TestMCPServer_BatchRequests(t *testing.T) {
	server := NewMCPServer(nil)

	server.RegisterTool("echo", ToolSchema{
		Name:        "echo",
		Description: "Echoes input",
		InputSchema: map[string]interface{}{"type": "object"},
	}, func(args json.RawMessage) (string, error) {
		return string(args), nil
	})

	// Create batch request
	batchReq := []JSONRPCRequest{
		{JSONRPC: "2.0", Method: "tools/list", ID: 1},
		{JSONRPC: "2.0", Method: "tools/call", Params: json.RawMessage(`{"name":"echo","args":{"test":1}}`), ID: 2},
	}

	batchBody, _ := json.Marshal(batchReq)
	respBody, err := server.HandleHTTP(batchBody)
	if err != nil {
		t.Fatal(err)
	}

	var batchResp []JSONRPCResponse
	if err := json.Unmarshal(respBody, &batchResp); err != nil {
		t.Fatal(err)
	}

	if len(batchResp) != 2 {
		t.Errorf("expected 2 responses, got %d", len(batchResp))
	}
}
