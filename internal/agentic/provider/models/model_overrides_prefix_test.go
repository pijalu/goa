// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package models

import (
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// TestOverrideIndex_Lookup pins the lookup rules the registry relies on:
// provider scoping, exact-ID entries winning over prefix entries (the
// qwen3.6-plus-free carve-out), and the longest prefix winning among prefix
// entries.
func TestOverrideIndex_Lookup(t *testing.T) {
	exact := overrideIdx.lookup(provider.ProviderOpenCode, "qwen3.6-plus-free")
	if exact == nil {
		t.Fatal("opencode qwen3.6-plus-free: no override, want the oa-compat carve-out")
	}
	if exact.ID != "qwen3.6-plus-free" {
		t.Errorf("opencode qwen3.6-plus-free: matched %q, want the exact carve-out entry", exact.ID)
	}

	prefix := overrideIdx.lookup(provider.ProviderOpenCode, "qwen3.6-plus")
	if prefix == nil {
		t.Fatal("opencode qwen3.6-plus: no override, want the qwen3.6- family pin")
	}
	if prefix.ID != "qwen3.6-" {
		t.Errorf("opencode qwen3.6-plus: matched %q, want the qwen3.6- prefix", prefix.ID)
	}

	// Pins are provider-scoped: the zen full-tier entry must not answer for the
	// Go tier, and vice versa.
	if ov := overrideIdx.lookup(provider.ProviderOpenCode, "union-alpha"); ov != nil {
		t.Errorf("opencode union-alpha: matched %q, want no override for the Go-tier model", ov.ID)
	}
	if ov := overrideIdx.lookup(provider.ProviderOpenCodeGo, "union-alpha"); ov == nil {
		t.Error("opencode-go union-alpha: no override, want the exact pin")
	}
	if ov := overrideIdx.lookup(provider.ProviderOpenCode, "qwen3.6-plus-free"); ov == nil || ov.BaseURL != "https://opencode.ai/zen/v1/chat/completions" {
		t.Errorf("opencode qwen3.6-plus-free: %+v, want the chat-completions carve-out", ov)
	}
}

// TestSynthesizeOverrideEntry_DropsBorrowedRouting is the regression for a model
// whose catalog membership rotates away: models.dev dropped qwen3.6-plus,
// qwen3.7-max and grok-4.6 from opencode-go on 2026-10-15 while other providers
// kept listing them. The provider-scoped pin must decide the entry, and the
// borrowed global copy — an aggregator's row for the same model — must not leak
// its endpoint or wire API, or the request would be posted to another vendor.
func TestSynthesizeOverrideEntry_DropsBorrowedRouting(t *testing.T) {
	ov := overrideModel{
		ID:       "qwen3.6-",
		Provider: string(provider.ProviderOpenCodeGo),
		API:      string(provider.ApiAnthropicMessages),
		BaseURL:  "https://opencode.ai/zen/go/v1/messages",
	}
	m := synthesizeOverrideEntry(provider.ProviderOpenCodeGo, "qwen3.6-plus", ov)

	if m.ID != "qwen3.6-plus" {
		t.Errorf("ID = %q, want the model, not the override's prefix", m.ID)
	}
	if m.Provider != provider.ProviderOpenCodeGo {
		t.Errorf("Provider = %q, want opencode-go", m.Provider)
	}
	if m.Api != provider.ApiAnthropicMessages {
		t.Errorf("Api = %q, want anthropic-messages (the pin)", m.Api)
	}
	if m.BaseURL != "https://opencode.ai/zen/go/v1/messages" {
		t.Errorf("BaseURL = %q, want the zen Go messages URL (the pin)", m.BaseURL)
	}
	// Model-intrinsic metadata still comes from the ID-global catalog entry.
	if m.ContextWindow == 0 {
		t.Error("ContextWindow = 0, want the global entry's window (model-intrinsic)")
	}
}

// TestSynthesizeOverrideEntry_NoRoutingBorrowed covers a prefix pin that declares
// no routing at all (the mimo- thinking-level map): the borrowed endpoint must be
// dropped so the serving provider's own endpoint applies.
func TestSynthesizeOverrideEntry_NoRoutingBorrowed(t *testing.T) {
	reasoning := true
	ov := overrideModel{
		ID:        "mimo-",
		Provider:  string(provider.ProviderOpenCodeGo),
		Reasoning: &reasoning,
	}
	if GetModel("mimo-v2.5") == nil {
		t.Fatal("precondition: mimo-v2.5 must exist in the global catalog for this test to be meaningful")
	}
	m := synthesizeOverrideEntry(provider.ProviderOpenCodeGo, "mimo-v2.5", ov)

	if m.Api != "" {
		t.Errorf("Api = %q, want empty (provider default, no borrowed wire API)", m.Api)
	}
	if m.BaseURL != "" {
		t.Errorf("BaseURL = %q, want empty (provider default, no borrowed endpoint)", m.BaseURL)
	}
	if !m.Reasoning {
		t.Error("Reasoning = false, want the override's true")
	}
}
