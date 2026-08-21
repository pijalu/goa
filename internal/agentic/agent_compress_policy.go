// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import "context"

func (a *Agent) MaybeCompress(ctx context.Context) error {
	return a.MaybeCompressWith(ctx, "", true)
}

// MaybeCompressWith manually triggers context compression using the given
// strategy (empty falls back to configured). When force is true, internal
// per-strategy thresholds are bypassed so manual invocations always perform
// work. No-op if the history is empty.
func (a *Agent) MaybeCompressWith(ctx context.Context, strategy CompressionStrategy, force bool) error {
	a.mu.Lock()
	n := len(a.history)
	a.mu.Unlock()
	if n == 0 {
		return nil
	}
	if a.cfg.Logger != nil {
		a.cfg.Logger.Log(Info, "Manual context compression triggered (strategy=%s force=%v)", strategy, force)
	}
	return a.compressHistoryWith(ctx, strategy, force)
}

// maybeCompress checks context usage and triggers compression if needed.
// The escalation layer (soft/trigger/hard/none) is selected from the
// configured thresholds and the cache gate; each layer runs its own resolved
// strategy (any method, per the all-methods layers rework).
func (a *Agent) maybeCompress(ctx context.Context) error {
	maxTokens := a.effectiveMaxTokens()
	if maxTokens == 0 {
		return nil
	}

	rt := a.cfg.ContextCompression.resolveThresholds()

	// Legacy whole-strategy micro: micro compaction self-manages its internal
	// thresholds, so skip the tier gate except at the emergency ceiling. Uses
	// effectiveHard so a disabled proactive hard layer (0 or negative) still
	// leaves the emergency branch reachable for the reactive paths.
	//
	// The gate MUST compare usage against the same effective window the tier
	// gate and the ceiling use (computeContextStatsForMax(maxTokens)), not the
	// display stats (ContextStats prefers the runtime model window): with a
	// configured max_tokens far below the advertised window, the display
	// percent stayed < hard and the micro self-management branch swallowed
	// every turn, masking the hard tier until the reactive ceiling fired.
	a.mu.Lock()
	effective := a.computeContextStatsForMax(maxTokens)
	a.mu.Unlock()
	stats := a.ContextStats()
	// The projected metric is provider-anchored and is preferred for the soft
	// maintenance gate, but it can under-report the full in-memory transcript
	// when the provider has not refreshed usage yet. Never let that discrepancy
	// route a hard-ceiling turn to the destructive fallback: the hard layer must
	// get the first chance to summarize. The reactive enforcer uses the full
	// estimate, so use the same threshold here for hard decisions.
	estimatedHard := rt.effectiveHard() > 0 && effective.EstimatedTokens >= maxTokens*rt.effectiveHard()/100
	if estimatedHard {
		if a.cfg.Logger != nil {
			a.cfg.Logger.Log(Info, "Hard-layer context compression: estimated usage %d%% (%d / %d tokens)",
				effective.EstimatedTokens*100/maxTokens, effective.EstimatedTokens, maxTokens)
		}
		return a.compressAndReportWith(ctx, rt.hardStrategy)
	}
	// Legacy whole-strategy micro: when the SOFT layer is micro it self-manages
	// its internal thresholds below the hard ceiling, so skip the tier gate and
	// run it directly until the emergency ceiling is reached. Route to the SOFT
	// strategy (micro), not the legacy whole-config Strategy, so a soft=micro
	// config applies micro rather than falling back to tool_elision.
	//
	// The gate must check the soft THRESHOLD (rt.soft > 0), not merely the
	// resolved soft strategy: resolveThresholds defaults softStrategy to micro
	// even when the soft layer is disabled (SoftPercent 0 = opt-in off), so
	// gating on the strategy alone fired micro on every turn — eliding fresh
	// tool results at near-empty context.
	if rt.soft > 0 && rt.softStrategy == CompressionMicro && effective.UsagePercent < rt.effectiveHard() {
		return a.compressSoftAndReport(ctx, rt.softStrategy)
	}

	tier := a.proactiveTier(rt, maxTokens)
	switch tier {
	case tierHard:
		if a.cfg.Logger != nil {
			a.cfg.Logger.Log(Info, "Hard-layer context compression: %d%% usage (%d / %d tokens)",
				stats.UsagePercent, stats.EstimatedTokens, stats.MaxTokens)
		}
		return a.compressAndReportWith(ctx, rt.hardStrategy)
	case tierSoft:
		if a.cfg.Logger != nil {
			a.cfg.Logger.Log(Info, "Soft-tier context maintenance: %d%% usage (%d / %d tokens)",
				stats.UsagePercent, stats.EstimatedTokens, stats.MaxTokens)
		}
		return a.compressSoftAndReport(ctx, rt.softStrategy)
	default:
		return nil
	}
}

// compressAndReportWith applies the given strategy and emits fresh stats.
func (a *Agent) compressAndReportWith(ctx context.Context, strategy CompressionStrategy) error {
	if err := a.compressHistoryWith(ctx, strategy, false); err != nil {
		if a.cfg.Logger != nil {
			a.cfg.Logger.Log(Error, "Context compression failed: %v", err)
		}
		return err
	}
	a.emitContextStats()
	return nil
}

// compressSoftAndReport applies the soft-layer strategy and emits fresh
// stats. The configured method runs verbatim (empty = micro).
func (a *Agent) compressSoftAndReport(ctx context.Context, strategy CompressionStrategy) error {
	if err := a.compressHistoryWith(ctx, strategy, false); err != nil {
		if a.cfg.Logger != nil {
			a.cfg.Logger.Log(Error, "Soft-tier context compression failed: %v", err)
		}
		return err
	}
	a.emitContextStats()
	return nil
}

// emitContextStats emits the post-compression context stats event.
func (a *Agent) emitContextStats() {
	newStats := a.ContextStats()
	a.emitEvent(OutputEvent{
		Type:         EventContextStats,
		ContextStats: &newStats,
	})
}

// proactiveTier resolves the current usage and selects the compression tier.
// The usage percent is the PROJECTED occupancy (CX8/P20): computeContextStatsForMax
// derives UsagePercent from ProjectedTokens, so the trigger reads the
// provider-anchored next-request projection, not the stale full-surface estimate.
func (a *Agent) proactiveTier(rt resolvedThresholds, maxTokens int) compressionTier {
	a.mu.Lock()
	stats := a.computeContextStatsForMax(maxTokens)
	tier := a.proactiveTierLocked(stats.UsagePercent, rt)
	a.mu.Unlock()
	return tier
}

// compressHistoryWith applies a specific strategy. When force is true,
// strategies with their own internal thresholds (micro compaction) bypass
// those thresholds so that a manual /compress invocation always does
// something visible, even when usage is below the configured ratio.
// An empty strategy falls back to tool_elision (the zero-cost default).
func (a *Agent) compressHistoryWith(ctx context.Context, strategy CompressionStrategy, force bool) error {
	if strategy == "" {
		strategy = CompressionToolElision
	}

	switch strategy {
	case CompressionToolElision:
		before, res := a.runElision(force)
		a.emitCompactionResult(string(CompressionToolElision), before, res, "")
	case CompressionSelective:
		before, res := a.runSelective()
		a.emitCompactionResult(string(CompressionSelective), before, res, "")
	case CompressionSummarize:
		return a.Compact(ctx)
	case CompressionFreshWindow:
		// Fresh window routes through compactOrdered with itself as the
		// escalation slot so the Phase 2b.3 ordering holds even on a layer
		// that named it directly: remote_compact still wins when available,
		// then the zero-LLM window reset runs.
		return a.compactOrdered(ctx, CompressionFreshWindow)
	case CompressionHybrid:
		return a.compressHybrid(ctx)
	case CompressionMicro:
		before, res := a.microCompactForced(force)
		a.emitCompactionResult(string(CompressionMicro), before, res, "")
	default:
		before, res := a.runElision(force)
		a.emitCompactionResult(string(CompressionToolElision), before, res, "")
	}
	return nil
}

// compactionResult is the outcome of a single in-memory compression step,
// captured while the step held a.mu so callers can emit an accurate
// EventCompact after unlock (emitEvent re-acquires a.mu, so it must never
// run under the lock).
