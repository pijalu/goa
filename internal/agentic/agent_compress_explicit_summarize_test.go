// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// B44 RED: an explicit /compress:summarize request must produce an LLM
// summary even when the fresh_window gate is enabled. Compact resolves its
// escalation slot from the hard layer, so with fresh_window enabled the
// manual summarize silently ran the zero-LLM window reset and DROPPED the
// conversation instead of summarizing it.
func TestManualSummarize_BypassesFreshWindowGate(t *testing.T) {
	const reply = "explicit checkpoint summary from the model"
	p := &provenanceSummarizeProvider{
		api:   provider.Api(fmt.Sprintf("explicit-summarize-%d", testProviderCounter.Add(1))),
		reply: reply,
		usage: &provider.Usage{InputTokens: 10, OutputTokens: 4},
	}
	agent := newProvenanceAgent(p, provenanceHistory())
	agent.cfg.ContextCompression.FreshWindow.Enabled = true
	obs := &mockEventObserver{}
	agent.AddObserver(obs)

	if err := agent.MaybeCompressWith(context.Background(), CompressionSummarize, true); err != nil {
		t.Fatalf("MaybeCompressWith(summarize): %v", err)
	}

	if p.callCount() != 1 {
		t.Fatalf("explicit summarize must call the summarize provider once, got %d calls (fresh_window must not serve a manual summarize)", p.callCount())
	}

	agent.mu.Lock()
	history := append([]Message(nil), agent.history...)
	agent.mu.Unlock()
	if len(history) != 2 {
		t.Fatalf("post-compaction history = %d messages, want the 2-message checkpoint pair: %#v", len(history), history)
	}
	if history[0].Role != User || !strings.Contains(history[0].Content, reply) {
		t.Errorf("checkpoint user frame = %q (%s), want the framed summary", history[0].Content, history[0].Role)
	}
	if history[1].Role != Assistant || history[1].Content != reply {
		t.Errorf("checkpoint assistant = %q (%s), want the raw summary %q", history[1].Content, history[1].Role, reply)
	}

	// The visible compaction event must carry the summarize label, not
	// fresh_window.
	for _, ev := range obs.Events() {
		if ev.Type == EventCompact && ev.Compaction != nil && ev.Compaction.Strategy == string(CompressionFreshWindow) {
			t.Errorf("manual summarize emitted a %q compaction event", CompressionFreshWindow)
		}
	}
}

// B44 RED: the hard layer NAMING fresh_window must not hijack an explicit
// summarize either — selection alone implies the automatic escalation, not
// the manual method override.
func TestManualSummarize_BypassesFreshWindowNamedSlot(t *testing.T) {
	const reply = "checkpoint for the named-slot case"
	p := &provenanceSummarizeProvider{
		api:   provider.Api(fmt.Sprintf("explicit-summarize-slot-%d", testProviderCounter.Add(1))),
		reply: reply,
		usage: &provider.Usage{InputTokens: 8, OutputTokens: 3},
	}
	agent := newProvenanceAgent(p, provenanceHistory())
	agent.cfg.ContextCompression.Strategies.Hard = CompressionFreshWindow
	if err := agent.MaybeCompressWith(context.Background(), CompressionSummarize, true); err != nil {
		t.Fatalf("MaybeCompressWith(summarize): %v", err)
	}

	if p.callCount() != 1 {
		t.Fatalf("explicit summarize must call the summarize provider once, got %d calls", p.callCount())
	}
	agent.mu.Lock()
	n := len(agent.history)
	agent.mu.Unlock()
	if n != 2 {
		t.Fatalf("post-compaction history = %d messages, want the checkpoint pair", n)
	}
}

// B44 guard: an explicit summarize request may be SERVED by remote
// (server-side) compaction — that is still a real summarize. Remote must keep
// winning over the local ladder even with the fresh_window gate on.
func TestManualSummarize_RemoteCompactionMayServe(t *testing.T) {
	p := &remoteCompactProbeProvider{
		api:            provider.Api(fmt.Sprintf("explicit-summarize-remote-%d", testProviderCounter.Add(1))),
		replacement:    remoteReplacement(),
		summarizeReply: "local summarize fallback",
	}
	agent := newRemoteCompactAgent(p, provenanceHistory())
	agent.cfg.ContextCompression.FreshWindow.Enabled = true

	if err := agent.MaybeCompressWith(context.Background(), CompressionSummarize, true); err != nil {
		t.Fatalf("MaybeCompressWith(summarize): %v", err)
	}

	assertRemoteCompactUsedExclusively(t, p)
}

// B44 guard: AUTOMATIC compaction (Compact with no explicit method) keeps the
// fresh_window upgrade semantics — the gate upgrades the default summarize
// slot to the zero-LLM reset.
func TestAutomaticCompact_KeepsFreshWindowUpgrade(t *testing.T) {
	p := &provenanceSummarizeProvider{
		api:   provider.Api(fmt.Sprintf("automatic-compact-%d", testProviderCounter.Add(1))),
		reply: "must never run",
	}
	agent := newProvenanceAgent(p, provenanceHistory())
	agent.cfg.ContextCompression.FreshWindow.Enabled = true

	if err := agent.Compact(context.Background()); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if p.callCount() != 0 {
		t.Errorf("automatic compaction with the fresh_window gate on must make no LLM calls, got %d", p.callCount())
	}
}
