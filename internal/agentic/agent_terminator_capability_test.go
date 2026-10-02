// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

// TestProviderOmitsStreamTerminator_Catalog pins the provider capability that
// decides whether the text-shape fallback may run at all.
//
// The user-visible requirement behind this: the completeness decision must not
// depend on the language the answer is written in. It is therefore scoped to
// providers that demonstrably never send a terminator, and is off everywhere
// else — where the protocol's own marker decides.
func TestProviderOmitsStreamTerminator_Catalog(t *testing.T) {
	cases := []struct {
		providerID string
		want       bool
		why        string
	}{
		{"zai", true, "documented: streams the full answer, never sends [DONE]/finish_reason"},
		{"zai-api", true, "same platform and endpoint family as zai"},
		{"opencode-go", true, "2026-10-02 creaves.project export request 20"},
		{"opencode", false, "the Zen endpoint, not the Go one — not observed omitting it"},
		{"openai", false, "sends finish_reason + [DONE]"},
		{"anthropic", false, "sends message_stop"},
		{"deepseek", false, "sends finish_reason + [DONE]"},
		{"lmstudio", false, "sends finish_reason"},
	}
	for _, tc := range cases {
		t.Run(tc.providerID, func(t *testing.T) {
			def := schema.LookupProviderDefByID(tc.providerID)
			if def == nil {
				t.Fatalf("provider %q not in catalog", tc.providerID)
			}
			if got := def.Compat.OmitsStreamTerminator; got != tc.want {
				t.Errorf("%s OmitsStreamTerminator = %v, want %v (%s)", tc.providerID, got, tc.want, tc.why)
			}
		})
	}
}

// TestProviderOmitsStreamTerminator_ResolvedFromModel checks NewAgent derives
// the capability from the model's provider, so no call site has to opt in and
// every agent built for that provider behaves consistently.
func TestProviderOmitsStreamTerminator_ResolvedFromModel(t *testing.T) {
	cases := []struct {
		name  string
		model provider.Model
		want  bool
	}{
		{
			name:  "catalog provider identity",
			model: provider.Model{Provider: schema.Provider("opencode-go")},
			want:  true,
		},
		{
			name:  "custom identity resolved from the endpoint",
			model: provider.Model{Provider: schema.ProviderCustom, BaseURL: "https://api.z.ai/api/coding/paas/v4"},
			want:  true,
		},
		{
			name:  "provider that sends a terminator",
			model: provider.Model{Provider: schema.ProviderOpenAI},
			want:  false,
		},
		{
			name:  "unknown provider is not granted the fallback",
			model: provider.Model{Provider: schema.Provider("something-else"), BaseURL: "https://example.test/v1"},
			want:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := NewAgent(Config{Model: tc.model, SystemPrompt: "sys", Logger: NewLogger(Error)})
			if got := a.cfg.ProviderOmitsStreamTerminator; got != tc.want {
				t.Errorf("ProviderOmitsStreamTerminator = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestProviderOmitsStreamTerminator_ExplicitOverride checks the config flag wins
// over the catalog, so a self-hosted endpoint that behaves like z.ai can opt in
// without a catalog change.
func TestProviderOmitsStreamTerminator_ExplicitOverride(t *testing.T) {
	a := NewAgent(Config{
		Model:                         provider.Model{Provider: schema.ProviderOpenAI},
		SystemPrompt:                  "sys",
		Logger:                        NewLogger(Error),
		ProviderOmitsStreamTerminator: true,
	})
	if !a.cfg.ProviderOmitsStreamTerminator {
		t.Error("explicit ProviderOmitsStreamTerminator=true was overwritten by the catalog default")
	}
}

// TestRoundDeliveredCompleteAnswer_NonLatinAnswerNeedsTerminator documents the
// exact limitation the two-tier rule exists to contain: the text-shape fallback
// reads Latin punctuation, so a non-Latin answer on a terminator-omitting
// provider is judged a stall (a bounded retry), while the same answer from a
// provider that sends a terminator is judged correctly by the protocol alone.
//
// The consequence is a wasted retry, never a discarded or corrupted answer —
// which is the safe direction for a heuristic that cannot be made
// language-neutral.
func TestRoundDeliveredCompleteAnswer_NonLatinAnswerNeedsTerminator(t *testing.T) {
	answer := "证据已完整。修复方案如下：" // ends in a full-width colon, not Latin ".:;)]}`'"

	t.Run("omitting provider without terminator is a stall", func(t *testing.T) {
		a := &Agent{cfg: Config{ProviderOmitsStreamTerminator: true}}
		a.contentBuf.WriteString(answer)
		if a.roundDeliveredCompleteAnswer() {
			t.Error("roundDeliveredCompleteAnswer() = true, want false (fallback cannot read this script, so it must not claim completion)")
		}
	})

	t.Run("terminator settles it regardless of script", func(t *testing.T) {
		a := &Agent{}
		a.roundSawProtocolTerminator = true
		a.contentBuf.WriteString(answer)
		if !a.roundDeliveredCompleteAnswer() {
			t.Error("roundDeliveredCompleteAnswer() = false, want true (the protocol terminator is language-agnostic)")
		}
	})
}
