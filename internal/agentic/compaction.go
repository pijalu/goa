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
	// Enabled gates micro compaction as an explicit opt-in. It is DISABLED by
	// default so the summarize strategy stays the default compaction path on a
	// full window. When enabled, micro compaction runs as a dry-run first to
	// validate it can meet the required shrink before any mutation; see Compact.
	Enabled bool

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

// PruneMarker is the fixed in-place marker substituted for the removed middle
// span of an over-budget tool result by the pre-compaction tool-result pruner.
// Its cost counts against the pruning budget: a valid configuration keeps
// HeadChars + len(PruneMarker) + TailChars within ThresholdChars so every
// pruned result fits the threshold and a second pass emits nothing.
const PruneMarker = "\n\n[... tool result middle pruned ...]\n\n"

// DefaultToolResultPruningConfig is the default configuration for the
// pre-compaction tool-result pruner (dsh compaction-tool-result-pruner parity:
// 8192/4096/1024 Unicode code points).
var DefaultToolResultPruningConfig = ToolResultPruningConfig{
	ThresholdChars: 8192,
	HeadChars:      4096,
	TailChars:      1024,
}

// ToolResultPruningConfig controls the pre-compaction tool-result pruner: a
// model-free pass that runs ahead of summarizeHistory and rewrites over-budget
// historical tool results in place to head + PruneMarker + tail. Character
// budgets are in Unicode code points (runes), matching the dsh reference.
type ToolResultPruningConfig struct {
	// Enabled gates the PRE-COMPACTION pruning pass in compactOrdered.
	// DEFAULT OFF (false): at the hard ceiling the configured summarize runs
	// directly — pre-pruning rewrites historical tool results in place and,
	// when it resolves pressure on its own, skips the summarize entirely.
	// The summarize-overflow fallback (shrinkToolPayloadsToFitLocked) is NOT
	// gated by this flag: pruning still runs there to make room for the
	// summarize retry.
	Enabled bool

	// ThresholdChars prunes a tool result when its content exceeds this many
	// Unicode code points. <= 0 falls back to the default (8192).
	ThresholdChars int

	// HeadChars is the number of leading Unicode code points retained.
	// <= 0 falls back to the default (4096).
	HeadChars int

	// TailChars is the number of trailing Unicode code points retained.
	// <= 0 falls back to the default (1024).
	TailChars int
}

// resolve returns the effective pruning configuration: zero or negative
// fields inherit the defaults so an SDK caller that never configured pruning
// (or set only one field) still gets a sane pass — the same field-wise
// fallback convention as microFallbackConfig. A combination whose head +
// marker + tail exceeds the threshold cannot prune without growth (the dsh
// config rejects it at construction); resolve falls the whole triple back to
// the defaults so the pruned output is always within threshold and strictly
// smaller than its input.
func (c ToolResultPruningConfig) resolve() ToolResultPruningConfig {
	def := DefaultToolResultPruningConfig
	if c.ThresholdChars <= 0 {
		c.ThresholdChars = def.ThresholdChars
	}
	if c.HeadChars <= 0 {
		c.HeadChars = def.HeadChars
	}
	if c.TailChars <= 0 {
		c.TailChars = def.TailChars
	}
	if c.HeadChars+len([]rune(PruneMarker))+c.TailChars > c.ThresholdChars {
		return def
	}
	return c
}

// microFallbackConfig returns the micro settings for the summarize-overflow
// fallback in Compact (applyMicroForSummarize). The fallback runs regardless
// of MicroCompaction.Enabled — it is the escape hatch when summarize itself
// overflows the window, not an opt-in maintenance pass — so Enabled is
// irrelevant here and zero-valued truncation fields fall back to the
// documented defaults: SDK callers that never configured micro still get a
// sane pass (bounded keep window, minimum content size, readable marker)
// instead of wiping every old tool result with an empty marker.
func microFallbackConfig(cfg MicroCompactionConfig) MicroCompactionConfig {
	def := DefaultMicroCompactionConfig
	if cfg.KeepRecentMessages <= 0 {
		cfg.KeepRecentMessages = def.KeepRecentMessages
	}
	if cfg.MinContentTokens <= 0 {
		cfg.MinContentTokens = def.MinContentTokens
	}
	if cfg.TruncatedMarker == "" {
		cfg.TruncatedMarker = def.TruncatedMarker
	}
	if cfg.MinContextRatio <= 0 {
		cfg.MinContextRatio = def.MinContextRatio
	}
	if cfg.CacheMissThreshold <= 0 {
		cfg.CacheMissThreshold = def.CacheMissThreshold
	}
	return cfg
}

// microCompactForced is the forced variant of micro compaction. When force is
// true, the MinContextRatio check is skipped so a manual /compress invocation
// can run even when usage is below the configured ratio.
//
// It self-manages the agent mutex: the history read, cache gate, and in-place
// truncation run under a.mu; it returns the pre-pass stats plus the work done
// so the CALLER emits the EventCompact after unlock (emitEvent acquires a.mu
// itself, so emitting under the lock would self-deadlock). This moves the
// single emission point to the top-level entry (compressHistoryWith /
// compressHistoryWithStrategy), matching every other strategy.
func (a *Agent) microCompactForced(force bool) (ContextStats, compactionResult) {
	cfg := a.cfg.ContextCompression.MicroCompaction
	a.mu.Lock()
	before := a.computeContextStats()
	if len(a.history) == 0 {
		a.mu.Unlock()
		return before, compactionResult{}
	}
	contextRatio := a.contextRatio()
	if !force && contextRatio < cfg.MinContextRatio {
		a.mu.Unlock()
		return before, compactionResult{}
	}

	// Cache-aware gating (resurrects the previously-dead CacheMissThreshold).
	// In-place truncation of old tool results mutates the provider's cached
	// prefix, flipping a hot cache into a full re-process on the next turn.
	// When invoked proactively (not a manual /compress), defer the mutation
	// unless the cache is presumed cold (inter-turn idle gap exceeded
	// CacheMissThreshold) or usage crossed the deferral ceiling, where
	// skipping the mutation risks an overflow. force=true (explicit
	// /compress) always mutates so a manual invocation always does visible
	// work.
	// Simplified cache gate (soft/hard/error model): micro is the SOFT-layer
	// strategy. Defer it while the provider cache is presumed hot, regardless of
	// any derived level (no deferralCeiling = hard−10 magic). force=true (manual
	// /compress) always mutates. The one override is the HARD ceiling: at/above
	// it the overflow risk beats cache churn, so the mutation runs even hot.
	overHard := contextRatio >= float64(a.cfg.ContextCompression.resolveThresholds().effectiveHard())/100
	if !force && !overHard && !a.cacheAssumedCold() {
		if a.cfg.Logger != nil {
			// Info, not Debug: this suppresses compression the user configured.
			a.cfg.Logger.Log(Info, "micro (soft) compaction deferred: provider cache presumed hot (idle < %s, ratio=%.1f%%)",
				cfg.CacheMissThreshold, contextRatio*100)
		}
		a.mu.Unlock()
		return before, compactionResult{}
	}

	keepIdx := computeKeepIdx(a.history, cfg.KeepRecentMessages, force)
	changed := a.truncateToolResults(a.history, keepIdx, cfg)
	a.mu.Unlock()

	if changed > 0 && a.cfg.Logger != nil {
		a.cfg.Logger.Log(Info, "Applied micro compaction: truncated %d tool results, keepIdx=%d, ratio=%.1f%%",
			changed, keepIdx, contextRatio*100)
	}
	return before, compactionResult{changed: changed}
}

// cacheAssumedCold reports whether the provider prefix cache is presumed cold
// for the upcoming request, justifying in-place history mutation that would
// otherwise churn a hot cache. The cache is assumed cold when either
//   - CacheMissThreshold <= 0 (cache protection disabled; legacy behavior), or
//   - the agent has been idle (no completed provider request) for longer than
//     the threshold, or no provider request has completed yet (first turn /
//     fresh resume) AND no completed request has reported cache reads. The
//     cacheWarmObserved escape keeps the first-turn presumption from failing
//     open for the whole turn: without it every per-round gate check in turn
//     1 would see a cold cache even though the cache has been hot since
//     round 2. Warm evidence does NOT override the idle-gap TTL logic —
//     after a long idle gap the provider cache really has expired.
//
// Idleness is measured from the freshest provider contact
// (lastProviderActivityLocked): per-round completions keep the cache hot
// during long single turns where lastTurnEnd (written only at turn END)
// would go stale past the threshold mid-turn.
//
// The caller must hold a.mu: the activity timestamps are guarded by it.
func (a *Agent) cacheAssumedCold() bool {
	threshold := a.cfg.ContextCompression.MicroCompaction.CacheMissThreshold
	if threshold <= 0 {
		return true
	}
	return a.cacheColdWithThreshold(threshold)
}

// cacheColdWithThreshold is the shared body of the cache gates once the
// effective idle threshold is resolved. The caller must hold a.mu.
//
// The first turn with no reported cache reads fails OPEN (cold) even while
// rounds keep completing: with no cache-read evidence there is no proven
// cache to protect, and treating round activity as warmth would suppress the
// per-round compression gate for the whole turn on providers that report no
// cache stats (TestAgent_CompressionGateBetweenRounds shape). From the
// second turn on — or once cache reads prove a hot cache — recent per-round
// activity means hot.
func (a *Agent) cacheColdWithThreshold(threshold time.Duration) bool {
	if a.lastTurnEnd.IsZero() && !a.cacheWarmObserved {
		return true
	}
	last := a.lastProviderActivityLocked()
	if last.IsZero() {
		return !a.cacheWarmObserved
	}
	return time.Since(last) >= threshold
}

// lastProviderActivityLocked returns the freshest provider-contact timestamp
// known: per-round request completions (lastRoundActivity) when present,
// falling back to the last turn end (inter-turn idle). lastTurnEnd alone goes
// stale mid-turn — it advances only at turn END, so during a long single turn
// the idle-gap logic would flip the gate cold while rounds still complete
// every few seconds, busting a provably hot cache BELOW the deferral ceiling
// (prefix-cache bust loop companion defect). The caller must hold a.mu.
func (a *Agent) lastProviderActivityLocked() time.Time {
	last := a.lastTurnEnd
	if a.lastRoundActivity.After(last) {
		last = a.lastRoundActivity
	}
	return last
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
// Like cacheAssumedCold, a zero lastTurnEnd AND zero lastRoundActivity
// (first turn / fresh resume) is cold only until a completed request reports
// cache reads (cacheWarmObserved); the idle-gap TTL logic is unaffected by
// warm evidence and runs off the freshest provider contact
// (lastProviderActivityLocked) so rounds completing mid-turn keep the cache
// hot past the inter-turn threshold.
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
	return a.cacheColdWithThreshold(threshold)
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
	if changed > 0 {
		// Tool payloads were replaced in place: the recorded provider prompt
		// no longer matches the conversation.
		a.invalidateContextUsageLocked()
	}
	return changed
}

// microCompactionDryRun estimates the outcome of a micro compaction pass
// WITHOUT mutating history. It mirrors truncateToolResults' selection exactly
// (same keepIdx, same MinContentTokens gate, same marker) but only counts the
// tokens that WOULD be freed, so the caller can validate — before committing
// any in-place mutation — whether micro compaction can shrink usage below the
// required level. Returns the number of tool results that would change and the
// estimated tokens that would be reclaimed. The caller must hold a.mu.
func (a *Agent) microCompactionDryRun(cfg MicroCompactionConfig, force bool) (changed, freedTokens int) {
	keepIdx := computeKeepIdx(a.history, cfg.KeepRecentMessages, force)
	markerTokens := len(cfg.TruncatedMarker) / 4
	for i := 1; i < keepIdx && i < len(a.history); i++ {
		msg := &a.history[i]
		if msg.Role != ToolRole {
			continue
		}
		contentTokens := len(msg.Content) / 4
		if contentTokens < cfg.MinContentTokens {
			continue
		}
		changed++
		if d := contentTokens - markerTokens; d > 0 {
			freedTokens += d
		}
	}
	return changed, freedTokens
}
