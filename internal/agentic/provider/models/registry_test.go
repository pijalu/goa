// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package models

import (
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

type lookupByPrefixCase struct {
	name      string
	modelName string
	wantNil   bool
	wantCtx   int
	wantProv  string
}

func TestLookupByPrefix(t *testing.T) {
	for _, tt := range lookupByPrefixCases() {
		t.Run(tt.name, func(t *testing.T) {
			assertLookupByPrefix(t, tt)
		})
	}
}

func lookupByPrefixCases() []lookupByPrefixCase {
	return []lookupByPrefixCase{
		{name: "Claude Sonnet 4 exact", modelName: "claude-sonnet-4-20250514", wantCtx: 200000, wantProv: string(provider.ProviderAnthropic)},
		{name: "Claude Sonnet 4 with variant suffix", modelName: "claude-sonnet-4-20250514-variant", wantCtx: 200000},
		{name: "GPT-4o with date suffix", modelName: "gpt-4o-2024-11-20", wantCtx: 128000, wantProv: string(provider.ProviderOpenAI)},
		{name: "GPT-4o-mini with date", modelName: "gpt-4o-mini-2024-07-18", wantCtx: 128000, wantProv: string(provider.ProviderOpenAI)},
		{name: "DeepSeek V4 flash (now in registry)", modelName: "deepseek-v4-flash", wantCtx: 1000000, wantProv: string(provider.ProviderDeepSeek)},
		{name: "DeepSeek Chat exact", modelName: "deepseek-chat", wantCtx: 128000, wantProv: string(provider.ProviderDeepSeek)},
		{name: "DeepSeek Reasoner exact", modelName: "deepseek-reasoner", wantCtx: 128000, wantProv: string(provider.ProviderDeepSeek)},
		{name: "Gemini Flash (generic prefix)", modelName: "gemini-2.5-flash-001", wantCtx: 1048576, wantProv: string(provider.ProviderGoogle)},
		{name: "Gemini 2.5 Pro exact", modelName: "gemini-2.5-pro", wantCtx: 1048576, wantProv: string(provider.ProviderGoogle)},
		{name: "Mistral Large 2 exact", modelName: "mistral-large-2", wantCtx: 128000, wantProv: string(provider.ProviderMistral)},
		{name: "Unknown model", modelName: "completely-unknown-model-xyz", wantNil: true},
		{name: "Empty string", modelName: "", wantNil: true},
	}
}

func assertLookupByPrefix(t *testing.T, tt lookupByPrefixCase) {
	t.Helper()
	m := LookupByPrefix(tt.modelName)
	if tt.wantNil {
		if m != nil {
			t.Errorf("LookupByPrefix(%q) = %+v, want nil", tt.modelName, m)
		}
		return
	}
	if m == nil {
		t.Fatalf("LookupByPrefix(%q) = nil, want non-nil", tt.modelName)
	}
	if m.ContextWindow != tt.wantCtx {
		t.Errorf("ContextWindow = %d, want %d", m.ContextWindow, tt.wantCtx)
	}
	if tt.wantProv != "" && string(m.Provider) != tt.wantProv {
		t.Errorf("Provider = %q, want %q", m.Provider, tt.wantProv)
	}
	if m.ID != tt.modelName {
		t.Errorf("ID = %q, want %q (should be the original queried name)", m.ID, tt.modelName)
	}
}

func TestLookupByPrefix_LongestMatchWins(t *testing.T) {
	// Both "gpt-4o" and "gpt-4o-mini" are in the registry.
	// "gpt-4o-mini" is longer, so it should match first for "gpt-4o-mini-xxx".
	m := LookupByPrefix("gpt-4o-mini-2024-07-18")
	if m == nil {
		t.Fatal("LookupByPrefix returned nil")
	}
	if m.ContextWindow != 128000 {
		t.Errorf("Expected 128000 context for gpt-4o-mini prefix, got %d", m.ContextWindow)
	}
}

func TestLookupByPrefix_DeepSeekV4Exact(t *testing.T) {
	// "deepseek-v4-flash" now matches the exact registry entry (128000 context),
	// not the generic "deepseek-" prefix, because deepseek-v4-flash is in the model registry.
	m := LookupByPrefix("deepseek-v4-flash")
	if m == nil {
		t.Fatal("LookupByPrefix returned nil")
	}
	if m.ContextWindow != 1000000 {
		t.Errorf("Expected 1000000 context from exact deepseek-v4-flash entry, got %d", m.ContextWindow)
	}
}

func TestLookupByPrefix_CaseInsensitive(t *testing.T) {
	m := LookupByPrefix("CLAUDE-SONNET-4-20250514-SUFFIX")
	if m == nil {
		t.Fatal("LookupByPrefix should be case-insensitive")
	}
	if m.Provider != provider.ProviderAnthropic {
		t.Errorf("Provider = %q, want %q", m.Provider, provider.ProviderAnthropic)
	}
}

// TestGetModels_PerProviderSharedIDs is the regression for "glm-5.2 is not
// shown in the list for z.ai": identical model IDs registered under two
// providers (zai coding plan, zai-api pay-per-token) must not evict each
// other — the ID-keyed map is first-wins, but per-provider listings must be
// complete for BOTH providers.
func TestGetModels_PerProviderSharedIDs(t *testing.T) {
	zaiModels := GetModels(provider.ProviderZai)
	zaiAPIModels := GetModels(provider.ProviderZaiApi)

	zaiIDs := map[string]bool{}
	for _, m := range zaiModels {
		zaiIDs[m.ID] = true
	}
	for _, id := range []string{"glm-4.5-air", "glm-4.7", "glm-5-turbo", "glm-5.1", "glm-5.2", "glm-5v-turbo"} {
		if !zaiIDs[id] {
			t.Errorf("GetModels(zai) missing %q (evicted by zai-api duplicate?)", id)
		}
	}

	apiIDs := map[string]bool{}
	for _, m := range zaiAPIModels {
		apiIDs[m.ID] = true
	}
	for _, id := range []string{"glm-4.5", "glm-4.6", "glm-5", "glm-5.2"} {
		if !apiIDs[id] {
			t.Errorf("GetModels(zai-api) missing %q", id)
		}
	}
}

// TestGetModelForProvider_ProviderExactMetadata verifies provider-specific
// pricing survives shared IDs: zai's glm-5.2 is quota-priced (zero), while
// zai-api's glm-5.2 carries per-token API pricing.
func TestGetModelForProvider_ProviderExactMetadata(t *testing.T) {
	zai := GetModelForProvider(provider.ProviderZai, "glm-5.2")
	if zai == nil {
		t.Fatal("GetModelForProvider(zai, glm-5.2) = nil")
	}
	if zai.Provider != provider.ProviderZai {
		t.Errorf("Provider = %q, want zai", zai.Provider)
	}
	if zai.Cost.Input != 0 || zai.Cost.Output != 0 {
		t.Errorf("zai glm-5.2 cost = %+v, want zero (quota plan)", zai.Cost)
	}
	if zai.ThinkingFormat != provider.ThinkingFormatZai {
		t.Errorf("ThinkingFormat = %q, want zai", zai.ThinkingFormat)
	}

	api := GetModelForProvider(provider.ProviderZaiApi, "glm-5.2")
	if api == nil {
		t.Fatal("GetModelForProvider(zai-api, glm-5.2) = nil")
	}
	if api.Provider != provider.ProviderZaiApi {
		t.Errorf("Provider = %q, want zai-api", api.Provider)
	}
	if api.Cost.Input != 0.0000014 || api.Cost.Output != 0.0000044 {
		t.Errorf("zai-api glm-5.2 cost = %+v, want per-token 1.4/4.4 per Mtok", api.Cost)
	}

	// Unknown provider falls back to the ID-global entry.
	fallback := GetModelForProvider(provider.ProviderCustom, "glm-5.2")
	if fallback == nil {
		t.Fatal("GetModelForProvider(custom, glm-5.2) = nil, want ID-global fallback")
	}
}
