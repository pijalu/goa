// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agentic "github.com/pijalu/goa/internal/agentic"
)

func TestNewRestClientTool(t *testing.T) {
	logger := agentic.NewLogger(agentic.Error)

	tool := NewRestClientTool(logger)
	if tool == nil {
		t.Fatal("NewRestClientTool returned nil")
	}

	_ = tool
}

func TestNewRestClientToolWithClient(t *testing.T) {
	logger := agentic.NewLogger(agentic.Error)
	client := &http.Client{}

	tool := NewRestClientToolWithClient(client, logger)
	if tool == nil {
		t.Fatal("NewRestClientToolWithClient returned nil")
	}

	// Verify it's the same tool type
	restTool, ok := tool.(*restClientTool)
	if !ok {
		t.Fatal("Tool should be *restClientTool")
	}
	if restTool.client != client {
		t.Error("Custom client not set correctly")
	}
}

func TestRestClientToolSchema(t *testing.T) {
	tool := NewRestClientTool(nil)
	schema := tool.Schema()

	assertRestSchemaBasics(t, schema)
	assertRestSchemaRequired(t, schema)
	assertRestSchemaProperties(t, schema)
	assertRestSchemaMethod(t, schema)
}

func assertRestSchemaBasics(t *testing.T, schema agentic.ToolSchema) {
	t.Helper()
	if schema.Name != "rest_api" {
		t.Errorf("Schema.Name = %q, want %q", schema.Name, "rest_api")
	}
	if schema.Description == "" {
		t.Error("Schema.Description should not be empty")
	}
	if schema.Schema == nil {
		t.Fatal("Schema.Schema should not be nil")
	}
}

func assertRestSchemaRequired(t *testing.T, schema agentic.ToolSchema) {
	t.Helper()
	required, ok := schema.Schema["required"]
	if !ok {
		t.Fatal("required field not found")
	}
	requiredStr := fmt.Sprintf("%v", required)
	if !strings.Contains(requiredStr, "method") || !strings.Contains(requiredStr, "url") {
		t.Errorf("required should contain method and url, got: %v", required)
	}
}

func assertRestSchemaProperties(t *testing.T, schema agentic.ToolSchema) {
	t.Helper()
	schemaMap, ok := schema.Schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("Schema properties not found or wrong type")
	}
	for _, prop := range []string{"method", "url", "headers", "body", "query_params"} {
		if _, ok := schemaMap[prop]; !ok {
			t.Errorf("Missing property: %s", prop)
		}
	}
}

func assertRestSchemaMethod(t *testing.T, schema agentic.ToolSchema) {
	t.Helper()
	schemaMap := schema.Schema["properties"].(map[string]interface{})
	methodProp, ok := schemaMap["method"]
	if !ok {
		t.Fatal("method property not found")
	}
	methodStr := fmt.Sprintf("%v", methodProp)
	if !strings.Contains(methodStr, "string") {
		t.Errorf("method should have string type")
	}
	if !strings.Contains(methodStr, "GET") {
		t.Errorf("method should have enum with GET")
	}
}

func TestRestClientToolExecute(t *testing.T) {
	server := newUserMockServer()
	defer server.Close()

	tool := NewRestClientTool(nil)
	baseURL := server.URL

	for _, tt := range restClientExecuteCases(baseURL) {
		t.Run(tt.name, func(t *testing.T) {
			assertRestClientResult(t, tool, tt.input, tt.want, tt.wantError)
		})
	}
}

type restClientExecuteCase struct {
	name      string
	input     string
	want      string
	wantError bool
}

func newUserMockServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/api/users" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"users":[{"id":1,"name":"Alice"},{"id":2,"name":"Bob"}]}`))
			return
		}
		if r.Method == "POST" && r.URL.Path == "/api/users" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"id":3,"name":"Charlie"}`))
			return
		}
		if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/api/users/") {
			id := strings.TrimPrefix(r.URL.Path, "/api/users/")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"id":` + id + `,"name":"User ` + id + `"}`))
			return
		}
		if r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/api/users/") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			var body map[string]interface{}
			json.NewDecoder(r.Body).Decode(&body)
			w.Write([]byte(`{"updated":true}`))
			_ = body
			return
		}
		if r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, "/api/users/") {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"not found"}`))
	}))
}

func restClientExecuteCases(baseURL string) []restClientExecuteCase {
	return []restClientExecuteCase{
		{name: "GET_list_users", input: `{"method":"GET","url":"` + baseURL + `/api/users"}`, want: "Alice"},
		{name: "GET_single_user", input: `{"method":"GET","url":"` + baseURL + `/api/users/1"}`, want: "User 1"},
		{name: "POST_create_user", input: `{"method":"POST","url":"` + baseURL + `/api/users","body":{"name":"Charlie"}}`, want: "Charlie"},
		{name: "PUT_update_user", input: `{"method":"PUT","url":"` + baseURL + `/api/users/1","body":{"name":"Alice Updated"}}`, want: "updated"},
		{name: "DELETE_user", input: `{"method":"DELETE","url":"` + baseURL + `/api/users/1"}`, want: "204"},
		{name: "GET_with_query_params", input: `{"method":"GET","url":"` + baseURL + `/api/users","query_params":{"page":"1","limit":"10"}}`, want: "Alice"},
		{name: "GET_with_custom_headers", input: `{"method":"GET","url":"` + baseURL + `/api/users","headers":{"Authorization":"Bearer token123"}}`, want: "Alice"},
		{name: "missing_url", input: `{"method":"GET"}`, wantError: true},
		{name: "invalid_json", input: `not json`, wantError: true},
		{name: "nonexistent_server", input: `{"method":"GET","url":"http://localhost:99999/nonexistent"}`, wantError: true},
		{name: "GET_404_error", input: `{"method":"GET","url":"` + baseURL + `/api/nonexistent"}`, wantError: true},
	}
}

func assertRestClientResult(t *testing.T, tool agentic.Tool, input, want string, wantError bool) {
	result, err := tool.Execute(input)
	if wantError {
		if err == nil {
			t.Error("Expected error, got nil")
		}
		return
	}
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !strings.Contains(result, want) {
		t.Errorf("Execute() should contain %q, got:\n%s", want, result)
	}
}

func TestRestClientToolExecuteWithLogger(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	logger := agentic.NewLogger(agentic.Debug)
	tool := NewRestClientTool(logger)

	result, err := tool.Execute(`{"method":"GET","url":"` + server.URL + `/test"}`)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !strings.Contains(result, "ok") {
		t.Errorf("Execute() should contain %q, got:\n%s", "ok", result)
	}
}

func TestRestClientToolNonJSONResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`plain text response`))
	}))
	defer server.Close()

	tool := NewRestClientTool(nil)

	result, err := tool.Execute(`{"method":"GET","url":"` + server.URL + `/test"}`)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !strings.Contains(result, "plain text response") {
		t.Errorf("Execute() should contain plain text response, got:\n%s", result)
	}
}

func TestRestClientToolUserAgent(t *testing.T) {
	var receivedUA string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedUA = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	tool := NewRestClientTool(nil)

	_, err := tool.Execute(`{"method":"GET","url":"` + server.URL + `/test"}`)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if receivedUA == "" {
		t.Error("User-Agent header should be set")
	}

	if !strings.Contains(receivedUA, "goa") {
		t.Errorf("User-Agent should contain 'goa', got: %s", receivedUA)
	}
}

func TestRestClientToolJSONPrettyPrint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"nested":{"deep":"value"},"array":[1,2,3]}`))
	}))
	defer server.Close()

	tool := NewRestClientTool(nil)

	result, err := tool.Execute(`{"method":"GET","url":"` + server.URL + `/test"}`)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	// Check that JSON is pretty printed (contains indentation)
	if !strings.Contains(result, "  ") {
		t.Errorf("Execute() should contain pretty-printed JSON, got:\n%s", result)
	}
}
