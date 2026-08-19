// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import "fmt"

// fresh_window compaction (Codex TokenBudget mode, Phase 2b.3): install a
// fresh context window with ZERO summarization calls. It is still a full
// compaction — the same lifecycle/provenance triple and EventCompact fire so
// hooks and observers see one consistent contract (Codex models TokenBudget
// as a normal compaction lifecycle) — but no LLM summary is ever requested:
// history resets to the system prompt plus the configured recent-turn /
// last-user preservation tail.
//
// Because it discards history it is an EXPLICIT compaction boundary, not a
// mutation of sent messages (invariant 1): the cache generation advances and
// the derived provider cache key rotates (Hard Rule 7 — a shared key with a
// diverging context would silently evict the provider cache).

// freshWindowSelected reports whether the fresh_window strategy wins the
// given escalation slot. Two independent opt-ins, either one sufficient:
//
//   - the FreshWindow.Enabled gate (every full compaction upgrades to a
//     zero-LLM window reset, mirroring how the remote_compact capability
//     upgrades the summarize slot);
//   - the escalation slot itself naming "fresh_window" (hard layer, on-error,
//     or the legacy whole-config strategy — the latter resolves onto hard),
//     so selection alone implies intent (no two-knob trap).
//
// The zero value (gate off, slot resolves to summarize) is false, so the
// default local summarize ladder is unchanged. Remote compaction, when
// available, is checked BEFORE this and still wins.
func (a *Agent) freshWindowSelected(slot CompressionStrategy) bool {
	return a.cfg.ContextCompression.FreshWindow.Enabled || slot == CompressionFreshWindow
}

// freshWindowPreserveTurns resolves the preservation tail length: the
// fresh-window override wins, then the shared PreserveRecentTurns, then the
// engine default of 2 (matching selective). At least 1 so the last user
// message always survives the reset (Codex keeps the last user message as
// the fresh window's continuation point).
func (a *Agent) freshWindowPreserveTurns() int {
	preserve := a.cfg.ContextCompression.FreshWindow.PreserveRecentTurns
	if preserve <= 0 {
		preserve = a.cfg.ContextCompression.PreserveRecentTurns
	}
	if preserve <= 0 {
		preserve = 2
	}
	return preserve
}

// compactFreshWindow installs the fresh context window and completes the
// caller's already-open compaction transaction (txID) with the
// start → summary → end triple, then emits the EventCompact labelled
// "fresh_window". It makes NO LLM call: the summary event carries the
// shadowed-range facts with a nil Usage (there is no summarize-call usage to
// report).
//
// The reset keeps the system message plus a chain-safe preservation tail
// (recent turns ending at the last user message). History is replaced
// wholesale — a non-prefix rewrite — so the cache generation advances and the
// recorded provider prompt is invalidated before the next stream opens.
func (a *Agent) compactFreshWindow(txID string) error {
	a.mu.Lock()
	before := a.computeContextStats()
	shadowedEnd := len(a.history)
	keep := a.freshWindowHistoryLocked()
	removed := shadowedEnd - len(keep)
	if removed < 0 {
		removed = 0
	}
	a.history = keep
	a.cacheGeneration++
	// History was replaced wholesale (non-prefix rewrite): the recorded
	// provider prompt is stale and the next stream must rotate the key.
	a.invalidateContextUsageLocked()
	a.mu.Unlock()

	after := a.ContextStats()
	// Provenance summary: the whole pre-reset history was shadowed; Usage is
	// nil because fresh_window never calls the model (zero-LLM contract).
	a.emitCompactionTxSummary(&CompactionTx{
		ID:             txID,
		ShadowedStart:  0,
		ShadowedEnd:    shadowedEnd,
		ShadowedCount:  shadowedEnd,
		ShadowedTokens: before.EstimatedTokens,
		FreedTokens:    before.EstimatedTokens - after.EstimatedTokens,
		Provider:       string(a.cfg.Model.Provider),
		Model:          a.cfg.Model.ID,
	})
	a.emitCompactionTxEnd(txID, "")
	a.emitCompaction(string(CompressionFreshWindow), before, after, removed, 0,
		fmt.Sprintf("fresh context window (%d messages preserved, no summary)", len(keep)))
	return nil
}

// freshWindowHistoryLocked computes the fresh window's history: the system
// message (when present at index 0) plus the chain-safe preservation tail
// ending at the last user message. The tail selection reuses
// findCompressionBoundary so tool-call chains are never split — identical
// preservation semantics to selective compression, which is what makes the
// reset safe to land mid-conversation. The caller must hold a.mu.
func (a *Agent) freshWindowHistoryLocked() []Message {
	if len(a.history) == 0 {
		return nil
	}
	preserve := a.freshWindowPreserveTurns()
	boundary := findCompressionBoundary(a.history, preserve)

	keep := make([]Message, 0, len(a.history)-boundary+1)
	if a.history[0].Role == System {
		keep = append(keep, a.history[0])
	}
	keep = append(keep, a.history[boundary:]...)
	return keep
}
