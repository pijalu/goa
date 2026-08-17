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
