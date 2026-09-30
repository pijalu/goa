// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// bugs.md #3 — "z.ai accepts context overflow silently": z.ai serves an
// over-window request WITHOUT raising a context-length error, so the ONLY thing
// that can catch the overflow is the pre-flight token-count guard. These tests
// pin the projection + threshold path that guard reads, with a z.ai-shaped
// model (context window 131072, the bound the provider resolution hands an
// unknown z.ai model id).

// zaiOverflowAgent builds an agent on a z.ai-shaped model: window 131072, GLM
// thinking wire format, tool-capable. ctxWindow 0 reproduces the pre-fix state
// (no bound at all).
func zaiOverflowAgent(ctxWindow int) *Agent {
	mdl := provider.Model{
		ID:            "glm-99-not-in-the-snapshot",
		Name:          "glm-99-not-in-the-snapshot",
		Api:           provider.ApiOpenAICompletions,
		Provider:      provider.ProviderZai,
		BaseURL:       "https://api.z.ai/api/coding/paas/v4",
		InputTypes:    []string{"text"},
		Reasoning:     true,
		ContextWindow: ctxWindow,
	}
	return NewAgent(Config{
		SystemPrompt: "You are a coding agent.",
		Model:        mdl,
		ContextCompression: ContextCompressionConfig{
			Thresholds:          CompressionThresholds{SoftPercent: 50, HardPercent: 95},
			Strategies:          CompressionLayerStrategies{Soft: CompressionSelective, Hard: CompressionSelective},
			PreserveRecentTurns: 1,
		},
	})
}

// fillHistory builds a z.ai-scale conversation and anchors the projection on a
// provider-reported usage figure of tokens (the provider count is the anchor;
// the next request's real cost).
func fillHistory(a *Agent, tokens int) {
	var history []Message
	history = append(history, Message{Type: Content, Role: System, Content: "You are a coding agent."})
	for i := 0; i < 8; i++ {
		history = append(history,
			Message{Type: Content, Role: User, Content: strings.Repeat("u", 400)},
			Message{Type: Content, Role: Assistant, Content: strings.Repeat("a", 400)})
	}
	a.SetHistory(history)
	recordTestUsage(a, &provider.Usage{InputTokens: tokens})
}

// TestZaiBoundContext_ProjectionOverSoftCeilingCompresses is the positive
// acceptance path: a z.ai request whose PROJECTED prompt is over the window's
// soft ceiling must be compressed by token count, before any request goes out —
// not left to a provider error z.ai will never raise.
func TestZaiBoundContext_ProjectionOverSoftCeilingCompresses(t *testing.T) {
	a := zaiOverflowAgent(131072)
	// 90% of the 131072 window: over the 50% soft ceiling, under the hard one.
	fillHistory(a, 120000)

	rt := a.cfg.ContextCompression.resolveThresholds()
	if tier := a.proactiveTier(rt, 131072); tier != tierSoft {
		t.Fatalf("tier = %v, want tierSoft at 90%% projected usage over a bounded z.ai window", tier)
	}

	before := historyHash(a)
	if err := a.maybeCompress(context.Background()); err != nil {
		t.Fatalf("maybeCompress: %v", err)
	}
	if historyHash(a) == before {
		t.Error("history unchanged: the token-count guard did not compress a z.ai request projected over the window")
	}
}

// TestZaiBoundContext_OverHardCeilingRefusesTurn pins the hard half: past the
// hard ceiling the agent refuses the turn (checkContextLimit) instead of
// posting an over-window request that z.ai would accept silently.
func TestZaiBoundContext_OverHardCeilingRefusesTurn(t *testing.T) {
	a := zaiOverflowAgent(131072)
	// 97% > the 95% hard ceiling.
	fillHistory(a, 127000)

	if err := a.checkContextLimit(); err == nil {
		t.Error("checkContextLimit = nil, want a refusal above the hard ceiling of a bounded z.ai window")
	}
}

// TestZaiBoundContext_UnderCeilingAllowsTurn is the other direction: the bound
// must not fire on a turn that actually fits — a floor is a guard, not a
// permanent summarizer.
func TestZaiBoundContext_UnderCeilingAllowsTurn(t *testing.T) {
	a := zaiOverflowAgent(131072)
	fillHistory(a, 30000) // ~23% of the window

	if err := a.checkContextLimit(); err != nil {
		t.Errorf("checkContextLimit = %v, want nil for a turn well under the window", err)
	}
	before := historyHash(a)
	if err := a.maybeCompress(context.Background()); err != nil {
		t.Fatalf("maybeCompress: %v", err)
	}
	if historyHash(a) != before {
		t.Error("history compressed below the soft ceiling on a bounded z.ai window")
	}
}

// TestZaiUnboundedContext_GuardIsInert is the control for the bug itself: with
// NO bound on the model (ContextWindow 0 — the pre-fix resolution for a z.ai
// model id the catalog does not know) every token-count threshold is inert.
// The very same 90% occupancy that compressed above sails through here, and the
// request would be sent to a provider that accepts the overflow silently. This
// is what the provider's declared context-window floor exists to prevent.
func TestZaiUnboundedContext_GuardIsInert(t *testing.T) {
	a := zaiOverflowAgent(0)
	fillHistory(a, 120000)

	if err := a.checkContextLimit(); err != nil {
		t.Fatalf("checkContextLimit = %v, want nil: with no bound there is nothing to compare against", err)
	}
	before := historyHash(a)
	if err := a.maybeCompress(context.Background()); err != nil {
		t.Fatalf("maybeCompress: %v", err)
	}
	if historyHash(a) != before {
		t.Error("history changed on an unbounded model — the guard must be inert at ContextWindow 0")
	}
}
