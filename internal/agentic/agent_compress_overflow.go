// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"fmt"

	"github.com/pijalu/goa/internal/agentic/provider"
)

func (a *Agent) checkSilentOverflow() {
	maxTokens := a.effectiveMaxTokens()
	if maxTokens == 0 {
		return
	}
	// Use effectiveHard (not the raw configured hard): with proactive thresholds
	// disabled (0) the reactive ceiling still guards against silent truncation.
	hard := a.cfg.ContextCompression.resolveThresholds().effectiveHard()
	stats := a.computeContextStats()
	if stats.UsagePercent < hard {
		return
	}
	a.cfg.Logger.Log(Warn, "Silent overflow detected: %d%% usage (%d / %d tokens)",
		stats.UsagePercent, stats.EstimatedTokens, stats.MaxTokens)
	a.emitEvent(OutputEvent{
		Type:         EventContextStats,
		ContextStats: &stats,
		Text:         fmt.Sprintf("warning: context usage ≥ %d%% without provider error — compression will fire on next turn", hard),
	})
}

// maybeCompressAfterLengthTruncation forces compression when the provider
// truncated the round at the edge of the context window: finish_reason=length
// with the reported total (gross prompt + completion) at ≥99% of the
// effective window. That truncation is the last warning before the next
// request is rejected with context_length_exceeded — a deepseek-v4 session
// ended its penultimate round at exactly total_tokens=window (finish_reason
// "length"), appended another tool result, and the next request came back
// HTTP 400. A plain max_tokens output cap does NOT trigger this: the total
// must actually be at the window.
func (a *Agent) maybeCompressAfterLengthTruncation() {
	a.mu.Lock()
	stopReason := a.lastStopReason
	total := a.lastGrossInputTokens + a.lastUsageOutputTokens
	a.mu.Unlock()
	if stopReason != provider.StopReasonMaxTokens {
		return
	}
	maxTokens := a.effectiveMaxTokens()
	if maxTokens <= 0 || total < maxTokens*99/100 {
		return
	}
	a.cfg.Logger.Log(Warn, "provider truncated output at the context window edge (%d/%d tokens, finish_reason=length); forcing compression before the next round",
		total, maxTokens)
	// Cheap strategies only touch tool payloads and cannot be trusted to free
	// enough at the window edge. With the default (empty) strategy this reduces
	// to tool_elision + selective; the explicit summarize/hybrid paths skip the
	// LLM call here (no ctx) and run the same free-space steps directly. Micro
	// compaction needs a lock-free emission boundary, so it runs off-lock via
	// compressHistoryWithStrategy (which emits its own "micro" event); the
	// selective follow-up then reports as "truncation".
	// The maintenance strategy is the SOFT-layer strategy; an unset soft layer
	// ("" after resolution when soft is disabled) reduces to tool_elision +
	// selective. Micro runs off-lock via compressHistoryWithStrategy.
	strategy := a.cfg.ContextCompression.resolveThresholds().softStrategy
	if a.cfg.ContextCompression.Thresholds.SoftPercent <= 0 {
		strategy = ""
	}
	if strategy == CompressionMicro {
		a.compressHistoryWithStrategy(string(strategy), true)
		a.mu.Lock()
		before := a.computeContextStats()
		res := a.compressSelective()
		a.mu.Unlock()
		a.emitCompactionResult("truncation", before, res, "")
		a.emitContextStats()
		return
	}
	a.mu.Lock()
	before := a.computeContextStats()
	var res compactionResult
	if strategy == "" {
		e := a.compressToolElision(true)
		s := a.compressSelective()
		res = mergeCompaction(e, s)
	} else {
		res = a.compressHistoryWithStrategyLocked(string(strategy), true)
		s := a.compressSelective()
		res = mergeCompaction(res, s)
	}
	a.mu.Unlock()
	a.emitCompactionResult("truncation", before, res, "")
	a.emitContextStats()
}

// mergeCompaction folds the work of two in-memory steps into one record so a
// multi-step pass emits a single EventCompact.
