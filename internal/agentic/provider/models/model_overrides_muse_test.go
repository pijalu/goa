// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package models

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// Muse Spark models on the OpenCode gateways are served through the OpenAI
// Responses API ({base}/responses, npm @ai-sdk/openai), NOT the
// chat-completions surface the opencode provider entries declare
// (bugs.md 2026-08-30). The catalog override pins api: openai-responses for
// the whole muse-spark- family on both opencode providers; every other
// catalog field (context window, reasoning, cost) must still inherit from
// models.dev.
//
// The family membership comes from the (regenerated) models.dev snapshot:
// opencode serves muse-spark-1.2 and muse-spark-1.2-contributor-free,
// opencode-go serves muse-spark-1.2-contributor. The dynamic sweep keeps the
// assertion true for any future variant upstream adds; the explicit pins make
// a reshape fail loudly instead of silently passing.
// requireMuseResponses asserts one registry entry carries the responses API
// with every other catalog field still inherited from models.dev.
func requireMuseResponses(t *testing.T, id string, prov provider.Provider) {
	t.Helper()
	m := GetModelForProvider(prov, id)
	if m == nil {
		t.Fatalf("registry model %q @ %q not found", id, prov)
	}
	if m.Api != provider.ApiOpenAIResponses {
		t.Errorf("%s @ %s: Api = %q, want openai-responses", id, prov, m.Api)
	}
	if m.ContextWindow != 1048576 {
		t.Errorf("%s @ %s: ContextWindow = %d, want inherited 1048576", id, prov, m.ContextWindow)
	}
	if !m.Reasoning {
		t.Errorf("%s @ %s: Reasoning = false, want inherited true", id, prov)
	}
	if m.Provider != prov {
		t.Errorf("%s @ %s: Provider = %q", id, prov, m.Provider)
	}
}

func TestRegistry_MuseSparkUsesResponsesAPI(t *testing.T) {
	pinned := map[provider.Provider][]string{
		provider.ProviderOpenCode:   {"muse-spark-1.2", "muse-spark-1.2-contributor-free"},
		provider.ProviderOpenCodeGo: {"muse-spark-1.2-contributor"},
	}
	for prov, ids := range pinned {
		for _, id := range ids {
			requireMuseResponses(t, id, prov)
		}
	}
}

// The override is prefix-based: EVERY muse-spark-* family member on each
// opencode provider must carry the responses API, whatever upstream lists
// next. The explicit pins above make a family reshape fail loudly; this sweep
// keeps them honest for future variants.
func TestRegistry_MuseSparkPrefixSweep(t *testing.T) {
	for _, prov := range []provider.Provider{provider.ProviderOpenCode, provider.ProviderOpenCodeGo} {
		for _, m := range GetModels(prov) {
			if !strings.HasPrefix(m.ID, "muse-spark-") {
				continue
			}
			if m.Api != provider.ApiOpenAIResponses {
				t.Errorf("%s @ %s: Api = %q, want openai-responses (prefix override)", m.ID, prov, m.Api)
			}
		}
	}
}

// Non-Muse models on the same opencode providers keep the provider's
// chat-completions API — the override is scoped to the muse-spark- prefix.
func TestRegistry_NonMuseOpencodeKeepsCompletions(t *testing.T) {
	keep := []struct {
		prov provider.Provider
		id   string
	}{
		{provider.ProviderOpenCode, "deepseek-v4-flash"},
		{provider.ProviderOpenCodeGo, "glm-5"},
	}
	for _, tc := range keep {
		m := GetModelForProvider(tc.prov, tc.id)
		if m == nil {
			t.Fatalf("registry model %q @ %q not found", tc.id, tc.prov)
		}
		if m.Api != provider.ApiOpenAICompletions {
			t.Errorf("%s @ %s: Api = %q, want openai-completions", tc.id, tc.prov, m.Api)
		}
	}
}
