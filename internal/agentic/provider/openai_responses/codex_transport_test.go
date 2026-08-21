// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package openairesponses

import (
	"net/http"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

func TestCodexBaseURL(t *testing.T) {
	tests := []struct {
		name      string
		accountID string
		want      string
	}{
		{name: "api key uses api.openai.com", accountID: "", want: codexAPIKeyBaseURL},
		{name: "oauth uses chatgpt backend-api", accountID: "acct-1", want: codexOAuthBaseURL},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := provider.StreamOptions{CodexAccountID: tt.accountID}
			if got := codexBaseURL(opts); got != tt.want {
				t.Errorf("codexBaseURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func newCodexRequest(t *testing.T) *http.Request {
	t.Helper()
	req, err := http.NewRequest("POST", "https://example.invalid", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	return req
}

func TestApplyCodexHeaders_APIKey(t *testing.T) {
	req := newCodexRequest(t)
	applyCodexHeaders(req, provider.StreamOptions{})
	if got := req.Header.Get("originator"); got != "goa" {
		t.Errorf("originator = %q, want goa", got)
	}
	if got := req.Header.Get("chatgpt-account-id"); got != "" {
		t.Errorf("account-id must be empty for API key, got %q", got)
	}
	if got := req.Header.Get("OpenAI-Beta"); got != "" {
		t.Errorf("OpenAI-Beta must be empty for API key, got %q", got)
	}
}

func TestApplyCodexHeaders_OAuth(t *testing.T) {
	req := newCodexRequest(t)
	applyCodexHeaders(req, provider.StreamOptions{CodexAccountID: "acct-42"})
	if got := req.Header.Get("chatgpt-account-id"); got != "acct-42" {
		t.Errorf("chatgpt-account-id = %q", got)
	}
	if got := req.Header.Get("OpenAI-Beta"); got != "responses=experimental" {
		t.Errorf("OpenAI-Beta = %q", got)
	}
	if got := req.Header.Get("accept"); got != "text/event-stream" {
		t.Errorf("accept = %q", got)
	}
	if got := req.Header.Get("originator"); got != "goa" {
		t.Errorf("originator = %q", got)
	}
}

func TestApplyCodexHeaders_PreservesExplicitBeta(t *testing.T) {
	req := newCodexRequest(t)
	req.Header.Set("OpenAI-Beta", "custom-beta")
	applyCodexHeaders(req, provider.StreamOptions{CodexAccountID: "a"})
	if got := req.Header.Get("OpenAI-Beta"); got != "custom-beta" {
		t.Errorf("explicit OpenAI-Beta overwritten: %q", got)
	}
}

func TestApplyCodexHeaders_PreservesExplicitOriginator(t *testing.T) {
	req := newCodexRequest(t)
	req.Header.Set("originator", "custom")
	applyCodexHeaders(req, provider.StreamOptions{CodexAccountID: "a"})
	if got := req.Header.Get("originator"); got != "custom" {
		t.Errorf("explicit originator overwritten: %q", got)
	}
}

// TestBuildResponsesBodyCodexSession pins the legacy codex request contract:
// session affinity is carried via prompt_cache_key only — the ChatGPT Codex
// SSE backend rejects previous_response_id (HTTP 400), store must be false,
// and the system prompt rides in instructions rather than a leading message.
func TestBuildResponsesBodyCodexSession(t *testing.T) {
	model := provider.Model{ID: "gpt-5.6-luna"}
	ctx := provider.Context{
		SystemPrompt: "You are a coding agent.",
		Messages: []provider.Message{
			{Role: provider.RoleUser, Content: []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "hi"}}},
		},
	}
	body := buildResponsesBody(model, ctx, provider.StreamOptions{SessionID: "codex-session"}, "codex")

	if _, hasPrev := body["previous_response_id"]; hasPrev {
		t.Errorf("codex SSE body must not send previous_response_id")
	}
	if got := body["prompt_cache_key"]; got != "codex-session" {
		t.Errorf("prompt_cache_key = %v, want codex-session", got)
	}
	if got := body["store"]; got != false {
		t.Errorf("store = %v, want false", got)
	}
	if got := body["instructions"]; got != "You are a coding agent." {
		t.Errorf("instructions = %v", got)
	}
	// The system prompt must not also appear as a leading input message.
	for _, item := range body["input"].([]map[string]interface{}) {
		if item["role"] == "system" {
			t.Errorf("codex system prompt must not be an input message")
		}
	}
}

func TestBuildResponsesBodyCodexPromptCacheKeyPrecedence(t *testing.T) {
	model := provider.Model{ID: "gpt-5-codex"}
	ctx := provider.Context{Messages: []provider.Message{
		{Role: provider.RoleUser, Content: []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "hi"}}},
	}}
	body := buildResponsesBody(model, ctx, provider.StreamOptions{
		SessionID: "legacy-session", PromptCacheKey: "scoped-cache-key",
	}, "codex")
	if got := body["prompt_cache_key"]; got != "scoped-cache-key" {
		t.Fatalf("prompt_cache_key = %v, want explicit cache key", got)
	}
	if _, hasPrevious := body["previous_response_id"]; hasPrevious {
		t.Fatal("Codex body must never include previous_response_id")
	}
}

// TestBuildResponsesBodyPlainSession ensures non-codex responses flavors still
// chain turns via previous_response_id (the codex carve-out did not regress).
func TestBuildResponsesBodyPlainSession(t *testing.T) {
	model := provider.Model{ID: "gpt-5"}
	ctx := provider.Context{
		Messages: []provider.Message{
			{Role: provider.RoleUser, Content: []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "hi"}}},
		},
	}
	body := buildResponsesBody(model, ctx, provider.StreamOptions{SessionID: "s-1"}, "")
	if got := body["previous_response_id"]; got != "s-1" {
		t.Errorf("previous_response_id = %v, want s-1", got)
	}
	if _, hasCache := body["prompt_cache_key"]; hasCache {
		t.Errorf("plain responses body must not send prompt_cache_key here")
	}
}
