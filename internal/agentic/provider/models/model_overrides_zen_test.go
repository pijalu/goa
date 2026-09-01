// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package models

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// OpenCode Zen/Go is NOT a protocol converter: its gateway
// (packages/console/.../handler.ts:198) throws a generic Error -> HTTP 500
// when the request wire format differs from the model's provider format, and
// the request-body converter is dead code. The models.dev catalog marks the
// whole opencode provider @ai-sdk/openai-compatible (chat-completions), which
// 500s the anthropic-format (claude-*, qwen3.x-plus|max) and openai-responses
// (gpt-*, grok-*, muse-spark-*) families. These overrides pin the correct
// wire API + full base_url per family (verified against
// https://opencode.ai/docs/zen + live probes).
//
// requireAPI asserts one registry entry carries the expected API and base_url.
func requireZenAPI(t *testing.T, prov provider.Provider, id, wantAPI, wantBase string) {
	t.Helper()
	m := GetModelForProvider(prov, id)
	if m == nil {
		t.Fatalf("registry model %q @ %q not found", id, prov)
	}
	if string(m.Api) != wantAPI {
		t.Errorf("%s @ %s: Api = %q, want %q", id, prov, m.Api, wantAPI)
	}
	if m.BaseURL != wantBase {
		t.Errorf("%s @ %s: BaseURL = %q, want %q", id, prov, m.BaseURL, wantBase)
	}
}

// Anthropic-format families (claude, qwen3.x-plus|max) on zen full tier.
func TestRegistry_ZenAnthropicFamilies(t *testing.T) {
	base := "https://opencode.ai/zen/v1/messages"
	for _, id := range []string{
		"claude-opus-4-6", "claude-sonnet-4-6", "claude-haiku-4-5",
		"qwen3.5-plus", "qwen3.6-plus",
	} {
		requireZenAPI(t, provider.ProviderOpenCode, id, "anthropic-messages", base)
	}
}

// Anthropic-format families on zen Go tier (no claude there).
func TestRegistry_ZenGoAnthropicFamilies(t *testing.T) {
	base := "https://opencode.ai/zen/go/v1/messages"
	for _, id := range []string{"qwen3.6-plus", "qwen3.7-max", "qwen3.7-plus", "qwen3.8-max", "qwen3.8-flash"} {
		requireZenAPI(t, provider.ProviderOpenCodeGo, id, "anthropic-messages", base)
	}
}

// OpenAI Responses-format families (gpt, grok) on both tiers.
func TestRegistry_ZenResponsesFamilies(t *testing.T) {
	requireZenAPI(t, provider.ProviderOpenCode, "gpt-5.2", "openai-responses", "https://opencode.ai/zen/v1/responses")
	requireZenAPI(t, provider.ProviderOpenCode, "grok-4.6", "openai-responses", "https://opencode.ai/zen/v1/responses")
	requireZenAPI(t, provider.ProviderOpenCodeGo, "gpt-5.6-luna", "openai-responses", "https://opencode.ai/zen/go/v1/responses")
	requireZenAPI(t, provider.ProviderOpenCodeGo, "grok-4.6", "openai-responses", "https://opencode.ai/zen/go/v1/responses")
}

// The qwen3.6-plus-free straggler is oa-compat (chat/completions), NOT
// anthropic — the qwen3.6- prefix override would otherwise misroute it. The
// exact-ID carve-out must win (exact overrides run after prefix overrides).
func TestRegistry_ZenQwenFreeCarveOut(t *testing.T) {
	requireZenAPI(t, provider.ProviderOpenCode, "qwen3.6-plus-free",
		"openai-completions", "https://opencode.ai/zen/v1/chat/completions")
}

// The oa-compat families (deepseek/minimax/glm/kimi/mimo + the remaining
// *-free models) keep the chat-completions surface — the overrides are scoped
// to the anthropic/responses prefixes only.
func TestRegistry_ZenOACompatUnchanged(t *testing.T) {
	keep := []struct {
		prov provider.Provider
		id   string
	}{
		{provider.ProviderOpenCode, "mimo-v2.5-free"},
		{provider.ProviderOpenCode, "deepseek-v4-flash"},
		{provider.ProviderOpenCode, "minimax-m2.5-free"},
		{provider.ProviderOpenCodeGo, "glm-5"},
		{provider.ProviderOpenCodeGo, "mimo-v2.5"},
		{provider.ProviderOpenCodeGo, "kimi-k3"},
	}
	for _, tc := range keep {
		m := GetModelForProvider(tc.prov, tc.id)
		if m == nil {
			t.Fatalf("registry model %q @ %q not found", tc.id, tc.prov)
		}
		if m.Api != provider.ApiOpenAICompletions {
			t.Errorf("%s @ %s: Api = %q, want openai-completions (oa-compat, unchanged)", tc.id, tc.prov, m.Api)
		}
	}
}

// Prefix sweep: every claude-* and qwen3.x-plus|max member on opencode must
// carry anthropic-messages; every gpt-*/grok-* member must carry
// openai-responses. Keeps the family honest as upstream adds versions. The
// qwen *-free members are the explicit exception (oa-compat).
func TestRegistry_ZenPrefixSweep(t *testing.T) {
	for _, m := range GetModels(provider.ProviderOpenCode) {
		id := m.ID
		switch {
		case strings.HasPrefix(id, "claude-"):
			if m.Api != provider.ApiAnthropicMessages {
				t.Errorf("%s: Api = %q, want anthropic-messages", id, m.Api)
			}
		case strings.HasPrefix(id, "gpt-"), strings.HasPrefix(id, "grok-"):
			if m.Api != provider.ApiOpenAIResponses {
				t.Errorf("%s: Api = %q, want openai-responses", id, m.Api)
			}
		case strings.HasPrefix(id, "qwen3.") && !strings.HasSuffix(id, "-free"):
			if m.Api != provider.ApiAnthropicMessages {
				t.Errorf("%s: Api = %q, want anthropic-messages", id, m.Api)
			}
		}
	}
}
