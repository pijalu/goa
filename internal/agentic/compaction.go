// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package agentic provides the core agent SDK — message types, context compression,
// and tool execution primitives for building LLM-powered agents.
package agentic

import (
	"time"
)

// DefaultMicroCompactionConfig is the default configuration for micro compaction.
var DefaultMicroCompactionConfig = MicroCompactionConfig{
	KeepRecentMessages: 20,
	MinContentTokens:   100,
	CacheMissThreshold: 1 * time.Hour,
	TruncatedMarker:    "[Old tool result content cleared]",
	MinContextRatio:    0.5,
}

// MicroCompactionConfig controls the micro compaction strategy.
type MicroCompactionConfig struct {
	// KeepRecentMessages is the number of most recent messages to never touch.
	KeepRecentMessages int

	// MinContentTokens is the minimum content size in tokens before truncating.
	// Messages below this threshold are left intact.
	MinContentTokens int

	// CacheMissThreshold is how long the agent must have been idle before
	// micro compaction is triggered on the next turn.
	CacheMissThreshold time.Duration

	// TruncatedMarker is the replacement text for cleared tool results.
	TruncatedMarker string

	// MinContextRatio is the minimum context usage ratio to trigger compaction.
	// 0.5 means at least 50% of the context window must be in use.
	MinContextRatio float64
}

// microCompactForced is the forced variant of micro compaction. When force is
// true, the MinContextRatio check is skipped so a manual /compress invocation
// can run even when usage is below the configured ratio.
//
// It self-manages the agent mutex: the history read, cache gate, and in-place
// truncation run under a.mu; the EventCompact emission runs after unlock
// (emitEvent acquires a.mu itself, so emitting under the lock would self-deadlock).
func (a *Agent) microCompactForced(force bool) {
	cfg := a.cfg.ContextCompression.MicroCompaction
	a.mu.Lock()
	if len(a.history) == 0 {
		a.mu.Unlock()
		return
	}
	contextRatio := a.contextRatio()
	if !force && contextRatio < cfg.MinContextRatio {
		a.mu.Unlock()
		return
	}

	// Cache-aware gating (resurrects the previously-dead CacheMissThreshold).
	// In-place truncation of old tool results mutates the provider's cached
	// prefix, flipping a hot cache into a full re-process on the next turn.
	// When invoked proactively (not a manual /compress), defer the mutation
	// unless the cache is presumed cold (inter-turn idle gap exceeded
	// CacheMissThreshold) or usage is at the hard ceiling where skipping the
	// mutation risks an overflow. force=true (explicit /compress) always
	// mutates so a manual invocation always does visible work.
	hardCeilingRatio := float64(a.cfg.ContextCompression.resolveThresholds().hard) / 100
	if !force && contextRatio < hardCeilingRatio && !a.cacheAssumedCold() {
		if a.cfg.Logger != nil {
			a.cfg.Logger.Log(Debug, "micro compaction deferred: provider cache presumed hot (idle < %s, ratio=%.1f%%)",
				cfg.CacheMissThreshold, contextRatio*100)
		}
		a.mu.Unlock()
		return
	}

	keepIdx := computeKeepIdx(a.history, cfg.KeepRecentMessages, force)
	changed := a.truncateToolResults(a.history, keepIdx, cfg)
	a.mu.Unlock()

	if changed > 0 {
		if a.cfg.Logger != nil {
			a.cfg.Logger.Log(Info, "Applied micro compaction: truncated %d tool results, keepIdx=%d, ratio=%.1f%%",
				changed, keepIdx, contextRatio*100)
		}
		a.emitEvent(OutputEvent{Type: EventCompact, Text: "micro"})
	}
}

// cacheAssumedCold reports whether the provider prefix cache is presumed cold
// for the upcoming request, justifying in-place history mutation that would
// otherwise churn a hot cache. The cache is assumed cold when either
//   - CacheMissThreshold <= 0 (cache protection disabled; legacy behavior), or
//   - the agent has been idle (no completed turn) for longer than the threshold,
//     or no previous turn has completed yet (first turn / fresh resume).
//
// The caller must hold a.mu: lastTurnEnd is guarded by it (written in
// finishProcessing under the lock).
func (a *Agent) cacheAssumedCold() bool {
	threshold := a.cfg.ContextCompression.MicroCompaction.CacheMissThreshold
	if threshold <= 0 {
		return true
	}
	last := a.lastTurnEnd
	if last.IsZero() {
		return true
	}
	return time.Since(last) >= threshold
}

// cacheAssumedColdForProactive is the cache gate for proactive (threshold-
// triggered) in-place history mutation by strategies OTHER than micro
// compaction (e.g. the default tool_elision). MicroCompaction.CacheMissThreshold
// is only populated for CompressionMicro; for every other strategy it stays
// zero, which cacheAssumedCold reads as "protection disabled". That would leave
// the default elision strategy churning the hot cache on every threshold
// crossing. To protect the cache by default regardless of strategy, a zero
// threshold here means the provider cache TTL (~1h, the default micro config);
// only an explicitly negative threshold disables protection.
//
// The caller must hold a.mu.
func (a *Agent) cacheAssumedColdForProactive() bool {
	threshold := a.cfg.ContextCompression.MicroCompaction.CacheMissThreshold
	if threshold < 0 {
		return true // protection explicitly disabled
	}
	if threshold == 0 {
		threshold = DefaultMicroCompactionConfig.CacheMissThreshold
	}
	last := a.lastTurnEnd
	if last.IsZero() {
		return true
	}
	return time.Since(last) >= threshold
}

func (a *Agent) contextRatio() float64 {
	maxTokens := a.effectiveMaxTokens()
	if maxTokens == 0 {
		return 0
	}
	stats := a.computeContextStats()
	return float64(stats.EstimatedTokens) / float64(maxTokens)
}

func computeKeepIdx(history []Message, keepRecent int, force bool) int {
	if keepRecent > len(history)-1 {
		keepRecent = len(history) - 1
	}
	if keepRecent < 0 {
		keepRecent = 0
	}
	if force {
		keepRecent = capForcedKeepRecent(len(history), keepRecent)
	}
	keepIdx := len(history) - keepRecent
	if keepIdx < 1 {
		keepIdx = 1
	}
	return keepIdx
}

func capForcedKeepRecent(historyLen, keepRecent int) int {
	maxKeep := historyLen / 2
	if maxKeep < 1 {
		maxKeep = 1
	}
	if maxKeep > 5 {
		maxKeep = 5
	}
	if keepRecent > maxKeep {
		return maxKeep
	}
	return keepRecent
}

func (a *Agent) truncateToolResults(history []Message, keepIdx int, cfg MicroCompactionConfig) int {
	changed := 0
	for i := 1; i < keepIdx && i < len(history); i++ {
		msg := &history[i]
		if msg.Role != ToolRole {
			continue
		}
		contentTokens := len(msg.Content) / 4
		if contentTokens < cfg.MinContentTokens {
			continue
		}
		msg.Content = cfg.TruncatedMarker
		changed++
	}
	return changed
}
