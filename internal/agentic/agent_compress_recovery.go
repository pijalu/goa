// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"context"
	"fmt"
	"strings"

	"github.com/pijalu/goa/internal/agentic/provider/hooks"
)

func mergeCompaction(x, y compactionResult) compactionResult {
	return compactionResult{
		removed:     x.removed + y.removed,
		freedTokens: x.freedTokens + y.freedTokens,
		changed:     x.changed + y.changed,
		escalated:   x.escalated || y.escalated,
	}
}

// handleContextError checks if the error is a context-length error and, if
// OnContextError is enabled, applies the configured on-error strategy to free
// context space. Default strategy: hybrid — tool_elision → selective (message
// removal) → summarize as a last resort (pi-style). This is the reactive
// safety net that stays on when proactive threshold compression is disabled
// (the default): cheap steps run first, and only if the window is still near
// full does it escalate — ending in a Compact (LLM summarize) when nothing
// cheaper freed enough.
func (a *Agent) handleContextError(ctx context.Context, err error) {
	if !isContextLengthError(err) {
		return
	}

	if a.cfg.Logger != nil {
		a.cfg.Logger.Log(Info, "Context length error detected: %v", err)
	}
	a.emitEvent(OutputEvent{
		Type:     EventContent,
		Role:     System,
		Text:     fmt.Sprintf("Context length exceeded: %v. The conversation is too long for this model's context window.", err),
		Metadata: map[string]string{"category": "system-notification"},
	})

	if !a.cfg.ContextCompression.OnContextError {
		return
	}

	if a.cfg.Logger != nil {
		a.cfg.Logger.Log(Info, "Context length error — applying on-error compression (%s)", a.onErrorStrategy())
	}
	a.compressOverflowRecovery(ctx)
}

// onErrorStrategy resolves the configured on-error recovery strategy
// (empty = hybrid, the documented default).
func (a *Agent) onErrorStrategy() CompressionStrategy {
	if s := a.cfg.ContextCompression.OnErrorStrategy; s != "" {
		return s
	}
	return CompressionHybrid
}

// compressOverflowRecovery applies the on-error strategy for a PROVEN context
// overflow. With the default hybrid it runs tool_elision → selective
// (unconditional) → summarize (only if the estimate still sits at the window
// edge). Unlike compressHybrid (which gates selective behind the escalation
// level), a provider rejection proves the request exceeded the window — and
// the local estimate provably under-counts (a deepseek-v4 session overflowed
// at 84% estimated / 100% actual, and gating selective behind the estimate
// made the retry fail identically). So selective message removal ALWAYS runs
// here to buy real headroom. A dedicated summarize strategy skips straight
// to Compact; micro/tool_elision/selective run their own pass and still get
// the escalate-to-Compact tail when the estimate remains at the window edge.
func (a *Agent) compressOverflowRecovery(ctx context.Context) {
	strategy := a.onErrorStrategy()
	if strategy == CompressionSummarize || strategy == CompressionFreshWindow {
		// Summarize and fresh_window both escalate to a full compaction, which
		// applies the Phase 2b.3 ordering (remote_compact → fresh_window →
		// local ladder): the on-error slot is threaded through so a
		// fresh_window selection still yields to an available remote
		// compaction first.
		if err := a.compactOrdered(ctx, strategy); err != nil && a.cfg.Logger != nil {
			a.cfg.Logger.Log(Error, "Context-overflow compaction failed: %v", err)
		}
		a.emitContextStats()
		return
	}
	before, res, stats := a.overflowRecoveryInMemory(strategy)

	// If the estimate still sits at the window edge, escalate to a summarize as
	// the last resort (pi-style): the cheaper steps could not free enough.
	// When it escalates, Compact emits its own "summarize" event, so the
	// pre-escalation elision/selective work is NOT separately reported (it is
	// subsumed by the summarize) — exactly one EventCompact per recovery.
	threshold := a.cfg.ContextCompression.resolveThresholds().effectiveHard()
	if stats.UsagePercent >= threshold {
		if err := a.Compact(ctx); err != nil && a.cfg.Logger != nil {
			a.cfg.Logger.Log(Error, "Context-overflow summarize failed: %v", err)
		}
	} else {
		a.emitCompactionResult("overflow", before, res, "")
	}
	a.emitContextStats()
}

// overflowRecoveryInMemory runs the in-memory part of the on-error recovery
// (everything except the LLM Compact) and returns the pre-pass stats, the
// work done, and the post-pass stats for the escalate-to-Compact decision.
// The hybrid default keeps the historical lock discipline: both steps plus
// the stats snapshot happen under one a.mu hold.
func (a *Agent) overflowRecoveryInMemory(strategy CompressionStrategy) (before ContextStats, res compactionResult, stats ContextStats) {
	if strategy == CompressionMicro {
		// Micro self-manages its lock (and its own emission boundary), so it
		// cannot run under a.mu; forced because the overflow is proven.
		b, r := a.microCompactForced(true)
		return b, r, a.ContextStats()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	before = a.computeContextStats()
	switch strategy {
	case CompressionSelective:
		res = a.compressSelective()
	case CompressionToolElision:
		res = a.compressToolElision(true)
	default: // hybrid: elision → selective (unconditional)
		e := a.compressToolElision(true)
		s := a.compressSelective()
		res = mergeCompaction(e, s)
	}
	return before, res, a.computeContextStats()
}

// compressHistoryWithStrategy applies the named compression strategy
// directly (empty = tool_elision).  The force parameter bypasses internal
// per-strategy thresholds. Micro compaction self-manages its own lock (and
// now reports through the caller's emission point).
func (a *Agent) compressHistoryWithStrategy(strategy string, force bool) {
	// Build a temporary Ctx-free strategy dispatch.  The summarization
	// strategy needs a real context, so we skip it here (it is not a
	// useful emergency strategy anyway since it costs an LLM call).
	if CompressionStrategy(strategy) == CompressionMicro {
		before, res := a.microCompactForced(force)
		a.emitCompactionResult(string(CompressionMicro), before, res, "")
		return
	}
	a.mu.Lock()
	a.compressHistoryWithStrategyLocked(strategy, force)
	a.mu.Unlock()
}

// compressHistoryWithStrategyLocked is the a.mu-held core of
// compressHistoryWithStrategy: in-memory strategies only (selective /
// elision / hybrid's free-space steps). It returns the work done so the
// caller can emit a single EventCompact after unlock. Micro compaction is
// NOT handled here (it needs the lock-free emission boundary); the caller
// must route CompressionMicro to microCompactForced before acquiring a.mu.
func (a *Agent) compressHistoryWithStrategyLocked(strategy string, force bool) compactionResult {
	switch CompressionStrategy(strategy) {
	case CompressionSelective:
		return a.compressSelective()
	case CompressionHybrid:
		res := a.compressToolElision(true)
		stats := a.computeContextStats()
		maxTokens := a.effectiveMaxTokens()
		escalation := a.cfg.ContextCompression.resolveThresholds().effectiveHard()
		if maxTokens > 0 && stats.EstimatedTokens > maxTokens*escalation/100 {
			s := a.compressSelective()
			res = mergeCompaction(res, s)
		}
		return res
	case CompressionToolElision:
		return a.compressToolElision(force)
	default:
		return a.compressToolElision(force)
	}
}

// isContextLengthError reports whether the error indicates the LLM's context
// window was exceeded. It uses both structured classification (checking
// ProviderError.IsContextOverflow via hooks.IsContextOverflow) and string
// matching so that all provider error formats are recognised.
func isContextLengthError(err error) bool {
	if err == nil {
		return false
	}
	// Structured check first — catches ProviderError where the hook
	// pipeline already classified IsContextOverflow=true.
	if hooks.IsContextOverflow(err) {
		return true
	}
	// Fallback string matching for errors not wrapped as ProviderError.
	msg := strings.ToLower(err.Error())
	patterns := []string{
		"context_length_exceeded",
		"context length",
		"maximum context",
		"token limit",
		"max_tokens",
		"too many tokens",
		"context window",
	}
	for _, p := range patterns {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return false
}

// SetBufferedToolCallCountForTest manually sets the buffered tool call
// counter. It is intended only for tests that exercise status labels without
// driving a real stream.
