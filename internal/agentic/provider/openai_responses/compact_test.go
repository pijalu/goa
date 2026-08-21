// SPDX-License-Identifier: GPL-3.0-or-later

package openairesponses

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// compactFixture captures the request a fake compact endpoint receives and
// serves a canned replacement transcript.
type compactFixture struct {
	body    []byte
	headers http.Header
	status  int
	// output is the JSON-encoded output item list the server returns.
	output string
}

func (f *compactFixture) server(t *testing.T) *httptest.Server {
	t.Helper()
	if f.status == 0 {
		f.status = http.StatusOK
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.body, _ = io.ReadAll(r.Body)
		f.headers = r.Header.Clone()
		if f.status != http.StatusOK {
			w.WriteHeader(f.status)
			_, _ = io.WriteString(w, `{"error":"boom"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"output":`+f.output+`}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func codexCompactRequest(baseURL string) provider.CompactRequest {
	return provider.CompactRequest{
		Model: provider.Model{
			ID:        "gpt-5-codex",
			Api:       provider.ApiOpenAICodexResponses,
			Provider:  provider.ProviderOpenAI,
			BaseURL:   baseURL,
			Reasoning: true,
		},
		Context: provider.Context{
			SystemPrompt: "You are a coding agent.",
			Messages: []provider.Message{
				{Role: provider.RoleUser, Content: []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "inspect the repo"}}},
				{Role: provider.RoleAssistant, Content: []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "done"}}},
			},
			Tools: []provider.ToolSchema{{Name: "bash", Description: "run a command", InputSchema: map[string]any{"type": "object"}}},
		},
		Options: provider.StreamOptions{
			APIKey:         "test-token",
			CodexAccountID: "acct-test",
			PromptCacheKey: "goa_cache_key_123",
			ServiceTier:    "flex",
			IdleTimeout:    50 * time.Millisecond,
		},
	}
}

// TestCompactEndpointRequestBody verifies the compact request carries only the
// normal Codex request fields (model, input, instructions, tools,
// parallel_tool_calls, reasoning, service_tier, prompt_cache_key, text) and
// never the streaming/session fields the compact endpoint rejects.
func TestCompactEndpointRequestBody(t *testing.T) {
	fx := &compactFixture{output: `[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"condensed"}]},
		{"type":"message","role":"assistant","content":[{"type":"output_text","text":"summary"}]}
	]`}
	srv := fx.server(t)

	p := &OpenAICodexResponsesProvider{}
	resp, err := p.Compact(context.Background(), codexCompactRequest(srv.URL))
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(resp.Messages) != 2 {
		t.Fatalf("expected 2 replacement messages, got %d: %#v", len(resp.Messages), resp.Messages)
	}
	if resp.Messages[0].Role != provider.RoleUser || resp.Messages[1].Role != provider.RoleAssistant {
		t.Errorf("unexpected roles: %#v", resp.Messages)
	}

	var body map[string]any
	if err := json.Unmarshal(fx.body, &body); err != nil {
		t.Fatalf("request body not JSON: %v\n%s", err, fx.body)
	}
	for _, field := range []string{"model", "input", "instructions", "tools", "parallel_tool_calls", "reasoning", "service_tier", "prompt_cache_key", "text"} {
		if _, ok := body[field]; !ok {
			t.Errorf("compact request missing field %q: %s", field, fx.body)
		}
	}
	for _, forbidden := range []string{"stream", "store", "tool_choice", "previous_response_id", "max_output_tokens"} {
		if _, ok := body[forbidden]; ok {
			t.Errorf("compact request must not carry %q: %s", forbidden, fx.body)
		}
	}
	if body["prompt_cache_key"] != "goa_cache_key_123" {
		t.Errorf("prompt_cache_key = %v, want goa_cache_key_123", body["prompt_cache_key"])
	}
	if body["service_tier"] != "flex" {
		t.Errorf("service_tier = %v, want flex", body["service_tier"])
	}
	if body["instructions"] != "You are a coding agent." {
		t.Errorf("instructions = %v", body["instructions"])
	}
	// Codex identity headers present; no secrets beyond the bearer token.
	if fx.headers.Get("originator") != "goa" {
		t.Errorf("originator header = %q, want goa", fx.headers.Get("originator"))
	}
}

// TestCompactEndpointDecodesToolItems verifies function_call and
// function_call_output items decode into tool-call and tool-result messages.
func TestCompactEndpointDecodesToolItems(t *testing.T) {
	fx := &compactFixture{output: `[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"run ls"}]},
		{"type":"function_call","name":"bash","call_id":"call_1","arguments":"{\"cmd\":\"ls\"}"},
		{"type":"function_call_output","call_id":"call_1","output":"file.go"}
	]`}
	srv := fx.server(t)

	p := &OpenAICodexResponsesProvider{}
	resp, err := p.Compact(context.Background(), codexCompactRequest(srv.URL))
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(resp.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d: %#v", len(resp.Messages), resp.Messages)
	}
	call := resp.Messages[1]
	if call.Role != provider.RoleAssistant || len(call.Content) == 0 || call.Content[0].Type != provider.ContentBlockToolCall {
		t.Fatalf("message[1] not a tool call: %#v", call)
	}
	if call.Content[0].ToolCallID != "call_1" || call.Content[0].ToolName != "bash" {
		t.Errorf("tool call mismatch: %#v", call.Content[0])
	}
	result := resp.Messages[2]
	if result.Role != provider.RoleToolResult || result.Content[0].Text != "file.go" || result.Content[0].ToolCallID != "call_1" {
		t.Errorf("tool result mismatch: %#v", result)
	}
}

// TestCompactEndpointNon200 verifies a non-200 compact response surfaces an
// error and yields no replacement, so the caller can fall back.
func TestCompactEndpointNon200(t *testing.T) {
	fx := &compactFixture{status: http.StatusInternalServerError}
	srv := fx.server(t)

	p := &OpenAICodexResponsesProvider{}
	_, err := p.Compact(context.Background(), codexCompactRequest(srv.URL))
	if err == nil {
		t.Fatal("expected error on non-200 compact response")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should carry the status code: %v", err)
	}
}

// TestCompactEndpointEmptyInput verifies an empty conversation short-circuits
// to an empty replacement without issuing a request.
func TestCompactEndpointEmptyInput(t *testing.T) {
	p := &OpenAICodexResponsesProvider{}
	req := codexCompactRequest("http://unused.invalid")
	req.Context.Messages = nil
	resp, err := p.Compact(context.Background(), req)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(resp.Messages) != 0 {
		t.Errorf("expected empty replacement, got %#v", resp.Messages)
	}
}

// TestCompactRequestTimeoutBounded verifies the compact call derives a bounded
// timeout from the idle timeout (Codex idle × multiplier).
func TestCompactRequestTimeoutBounded(t *testing.T) {
	opts := provider.StreamOptions{IdleTimeout: 10 * time.Second}
	if got := compactRequestTimeout(opts); got != 40*time.Second {
		t.Errorf("timeout = %v, want 40s (idle × 4)", got)
	}
	// No idle configured → scaled default, always bounded.
	if got := compactRequestTimeout(provider.StreamOptions{}); got != provider.DefaultStreamIdleTimeout*compactRequestTimeoutIdleMultiplier {
		t.Errorf("default timeout = %v, want %v", got, provider.DefaultStreamIdleTimeout*compactRequestTimeoutIdleMultiplier)
	}
}

// TestCompactBaseURLPathSwap verifies the compact verb replaces the trailing
// Responses route on each Codex/OpenAI base URL shape.
func TestCompactBaseURLPathSwap(t *testing.T) {
	cases := map[string]string{
		"https://chatgpt.com/backend-api/codex/responses": "https://chatgpt.com/backend-api/codex/responses/compact",
		"https://api.openai.com/v1/responses/codex":       "https://api.openai.com/v1/responses/compact",
		"https://api.openai.com/v1/responses":             "https://api.openai.com/v1/responses/compact",
		"http://localhost:8080/responses":                 "http://localhost:8080/responses/compact",
	}
	for in, want := range cases {
		if got := replaceResponsesPath(in, compactEndpointPath); got != want {
			t.Errorf("replaceResponsesPath(%q) = %q, want %q", in, got, want)
		}
	}
}
