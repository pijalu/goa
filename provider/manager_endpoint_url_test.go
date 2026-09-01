// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"testing"

	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
)

// modelEndpointURL adapts the configured provider endpoint to each API's wire
// URL shape; the generic protocol runtime POSTs the result verbatim.
func TestModelEndpointURL(t *testing.T) {
	cases := []struct {
		name     string
		api      agenticprovider.Api
		endpoint string
		want     string
	}{
		{"completions-suffixed", agenticprovider.ApiOpenAICompletions, "https://api.example.com/v1", "https://api.example.com/v1/chat/completions"},
		{"completions-idempotent", agenticprovider.ApiOpenAICompletions, "https://api.example.com/v1/chat/completions", "https://api.example.com/v1/chat/completions"},
		{"responses-suffixed", agenticprovider.ApiOpenAIResponses, "https://opencode.ai/zen/v1", "https://opencode.ai/zen/v1/responses"},
		{"responses-idempotent", agenticprovider.ApiOpenAIResponses, "https://opencode.ai/zen/v1/responses", "https://opencode.ai/zen/v1/responses"},
		{"azure-responses-suffixed", agenticprovider.ApiAzureOpenAIResponses, "https://res.openai.azure.com/openai/v1", "https://res.openai.azure.com/openai/v1/responses"},
		{"codex-owned-at-runtime", agenticprovider.ApiOpenAICodexResponses, "https://chatgpt.com/backend-api", "https://chatgpt.com/backend-api"},
		{"anthropic-bare-host", agenticprovider.ApiAnthropicMessages, "https://api.anthropic.com", "https://api.anthropic.com/v1/messages"},
		{"anthropic-zen-versioned", agenticprovider.ApiAnthropicMessages, "https://opencode.ai/zen/v1", "https://opencode.ai/zen/v1/messages"},
		{"anthropic-zen-go-versioned", agenticprovider.ApiAnthropicMessages, "https://opencode.ai/zen/go/v1", "https://opencode.ai/zen/go/v1/messages"},
		{"anthropic-idempotent", agenticprovider.ApiAnthropicMessages, "https://opencode.ai/zen/v1/messages", "https://opencode.ai/zen/v1/messages"},
		{"google-owned", agenticprovider.ApiGoogleGenerativeAI, "https://generativelanguage.googleapis.com/v1beta", "https://generativelanguage.googleapis.com/v1beta"},
		{"empty-endpoint", agenticprovider.ApiOpenAIResponses, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := modelEndpointURL(tc.api, tc.endpoint); got != tc.want {
				t.Errorf("modelEndpointURL(%s, %q) = %q, want %q", tc.api, tc.endpoint, got, tc.want)
			}
		})
	}
}
