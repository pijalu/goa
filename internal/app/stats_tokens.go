// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/agentic/provider/models"
	"github.com/pijalu/goa/internal/metrics"
	"github.com/pijalu/goa/internal/usage"
	"github.com/pijalu/goa/tui"
)

const cacheBustDropToleranceTokens = 1024

// turnStatsFingerprint identifies one applied round of token statistics so a
// byte-identical re-emission within the same turn can be skipped.
type turnStatsFingerprint struct {
	promptN    int
	predictedN int
	cacheRead  int
	cacheWrite int
}

func (a *App) handleTokenStats(ev *agentic.OutputEvent) {
	a.statsMu.Lock()
	// Extract token counts from timings
	appliedStats := false
	if ev.Timings != nil {
		a.turnStatsSeen = true
		// Dedupe: emitTurnStats re-emits the unchanged providerUsage on
		// consecutive round ends (its provider-usage path never sets
		// turnStatsEmitted), delivering the SAME TokenTimings twice per turn.
		// Skip the duplicate so session totals, the bust counter and the
		// usage.db record count each round once. Identical values in a NEW turn
		// (turnCount advanced) are a different turn and must count again.
		fp := turnStatsFingerprint{
			promptN:    ev.Timings.PromptN,
			predictedN: ev.Timings.PredictedN,
			cacheRead:  ev.Timings.CacheReadTokens,
			cacheWrite: ev.Timings.CacheWriteTokens,
		}
		isDuplicate := a.lastStatsDedupSet &&
			a.lastStatsDedupTurn == a.turnCount &&
			a.lastStatsDedup == fp
		if !isDuplicate {
			a.lastStatsDedup = fp
			a.lastStatsDedupSet = true
			a.lastStatsDedupTurn = a.turnCount
			a.applyTokenTimingsLocked(ev.Timings)
			appliedStats = true
		}
	}

	// Extract context window usage
	if ev.ContextStats != nil {
		a.tokenSessionMax = ev.ContextStats.MaxTokens
		a.tokenSessionEstimate = ev.ContextStats.EstimatedTokens
		a.tokenSessionProjected = ev.ContextStats.ProjectedTokens
	}

	// Record per-turn usage to the global store (best-effort, non-fatal).
	// Only for genuinely new stats — a duplicate re-emission would append the
	// same usage.db row twice.
	if appliedStats {
		a.recordTurnUsageLocked()
	}

	// Compute cost from active model's pricing config
	stats := a.buildFooterStatsLocked()
	a.statsMu.Unlock()

	subs := a.subs
	subs.footer.SetData(tui.FooterData{
		Workdir:                subs.projectDir,
		Model:                  activeModelDisplay(subs),
		Profile:                string(subs.effectiveModeState().Major),
		Mode:                   string(subs.effectiveModeState().Autonomy),
		Stats:                  formatFooterStats(stats),
		CompanionModel:         companionModelDisplay(subs),
		Provider:               sessionProviderID(subs),
		ThinkingLevel:          mainThinkingLevel(subs),
		CompanionThinkingLevel: companionThinkingLevel(subs),
	})
}

// applyTokenTimingsLocked folds one round of provider token statistics into
// the session accumulators, last-turn fields, and the cache-bust counter.
// Called only for non-duplicate emissions (see the dedupe guard in
// handleTokenStats). Requires a.statsMu to be held.
func (a *App) applyTokenTimingsLocked(timings *agentic.TokenTimings) {
	a.lastTurnPromptN = timings.PromptN
	a.lastTurnPredictedN = timings.PredictedN
	a.tokenPromptTotal += timings.PromptN
	a.tokenPredictedTotal += timings.PredictedN

	// Track cache tokens
	prevCacheRead := a.lastTurnCacheRead
	a.lastTurnCacheRead = timings.CacheReadTokens
	a.lastTurnCacheWrite = timings.CacheWriteTokens
	a.tokenCacheReadTotal += timings.CacheReadTokens
	a.tokenCacheWriteTotal += timings.CacheWriteTokens

	// Cache-hit rate trend: the per-completion rate (pi-style, from THIS
	// round's numbers) — the 2nd value of the status-bar CH segment. Only
	// rounds with cache activity feed the trend — a cache-less round (or
	// provider) must not drag the rate to 0 and trip the drop coloring.
	if timings.CacheReadTokens > 0 || timings.CacheWriteTokens > 0 {
		a.lastCacheHit.observe(metrics.CacheHitPct(timings.CacheReadTokens, timings.CacheWriteTokens, timings.PromptN))
	}
	// The token-weighted session-wide level (CH's 1st value) folds EVERY
	// round that consumed prompt-side tokens: a recompute-after-establishment
	// (read=0/write=0, full uncached prompt) is precisely the miss that must
	// drag the weighted average down (the report's 10k-miss + 5k-hit → 33%
	// example). Rounds with no volume at all are no-ops.
	a.foldCacheHitGlobalLocked(timings)
	// Count cache busts two ways:
	//  1. Zero cache reads AFTER the cache was established (provider TTL
	//     expiry reports 0). The first request(s) of a session — or of a
	//     fresh-context conversation (EventContextReset re-arms
	//     cacheReadEstablished) — are cold by nature and not counted; a
	//     provider reporting no cache stats never establishes, so the
	//     counter stays hidden there. Establishment is tracked in
	//     cacheReadEstablished rather than tokenCacheReadTotal because
	//     the total is a session-level CH figure that must survive
	//     mid-session context resets.
	//  2. A significant DROP in cache reads: in an append-only
	//     conversation the cached prefix grows monotonically, so a
	//     collapse means the prefix was invalidated — e.g. in-place
	//     history mutation (micro compaction) leaves a PARTIAL hit
	//     (5,376 of ~113k tokens in the 2026-08-02 session export),
	//     which the zero-read rule never catches. A tolerance absorbs
	//     block-quantization wobble in provider reporting.
	if timings.CacheReadTokens > 0 {
		a.cacheReadEstablished = true
	}
	// The two failure modes carry different token damage:
	//   - full miss (zero read after establishment): the ENTIRE previous
	//     prefix was recomputed — missed = prevCacheRead.
	//   - partial miss (drop beyond tolerance): only a SUFFIX was
	//     recomputed — missed = prevCacheRead - cacheRead.
	// The zero-read rule takes precedence so a miss is never double-counted.
	switch {
	case timings.CacheReadTokens == 0 && a.cacheReadEstablished:
		a.tokenCacheFullMisses++
		a.tokenCacheMissedTokens += int64(prevCacheRead)
	case prevCacheRead > 0 && timings.CacheReadTokens+cacheBustDropToleranceTokens < prevCacheRead:
		a.tokenCachePartialMisses++
		a.tokenCacheMissedTokens += int64(prevCacheRead - timings.CacheReadTokens)
	}

	// Capture last-turn output speed
	a.lastTurnSpeed = timings.PredictedPerSecond
	if a.lastTurnSpeed == 0 && timings.PredictedMs > 0 {
		a.lastTurnSpeed = float64(timings.PredictedN) / (timings.PredictedMs / 1000.0)
	}
}

// foldCacheHitGlobalLocked folds one cache-active round into the
// token-weighted session-wide cache-hit level — the 1st value of the footer's
// CH segment. Per the report: newLevel = (level·W + rate·w) / (W + w), then
// W += w, so rounds weigh by cached-token volume, not by count.
//
// The per-round weight w is the volume that went through the cache pipeline:
// CacheRead + CacheWrite. goa normalizes PromptN to EXCLUDE cached tokens
// (computePromptN in the OpenAI completions parser strips them; Anthropic
// reports input_tokens without cache parts), so a round with no cache reads
// AND no writes still carries its uncached prompt size as weight — that is
// exactly the report's example (10k miss at 0% + 5k full hit at 100% →
// 33.3%, not 50%). Requires a.statsMu to be held.
func (a *App) foldCacheHitGlobalLocked(timings *agentic.TokenTimings) {
	weight := int64(timings.CacheReadTokens + timings.CacheWriteTokens)
	if weight == 0 {
		weight = int64(timings.PromptN)
	}
	if weight <= 0 {
		return // nothing to weight this round by
	}
	rate := metrics.CacheHitPct(timings.CacheReadTokens, timings.CacheWriteTokens, timings.PromptN)
	prevLevel := a.cacheHitGlobalLevel
	prevHad := a.cacheHitGlobalWeight > 0
	if prevHad {
		total := a.cacheHitGlobalWeight + weight
		a.cacheHitGlobalLevel = (a.cacheHitGlobalLevel*float64(a.cacheHitGlobalWeight) + rate*float64(weight)) / float64(total)
	} else {
		a.cacheHitGlobalLevel = rate
	}
	a.cacheHitGlobalWeight += weight
	a.lastCacheHit.foldGlobal(a.cacheHitGlobalLevel, prevLevel, prevHad)
}

// recordTurnUsageLocked appends the just-completed turn's token usage to the
// global usage store for /usage. It is best-effort: store errors never break
// the session. Requires a.statsMu to be held (called from handleTokenStats,
// after last-turn fields are updated).
func (a *App) recordTurnUsageLocked() {
	if a.lastTurnPromptN == 0 && a.lastTurnPredictedN == 0 {
		return // nothing recorded for this turn
	}
	st, err := a.usageStoreOpen()
	if err != nil || st == nil {
		return
	}
	subs := a.subs
	_ = st.Add(usage.Record{
		Project:    subs.projectDir,
		Provider:   sessionProviderID(subs),
		Model:      activeModelName(subs),
		PromptN:    a.lastTurnPromptN,
		PredictedN: a.lastTurnPredictedN,
		CacheRead:  a.lastTurnCacheRead,
		CacheWrite: a.lastTurnCacheWrite,
	})
}

// usageStoreOpen lazily opens the global usage store, caching the result.
func (a *App) usageStoreOpen() (*usage.Store, error) {
	if a.usageStore != nil {
		return a.usageStore, nil
	}
	if a.usageStoreTried {
		return nil, nil // already failed once; don't retry every turn
	}
	a.usageStoreTried = true
	p, err := usage.DefaultPath()
	if err != nil {
		return nil, err
	}
	st, err := usage.Open(p)
	if err != nil {
		return nil, err
	}
	a.usageStore = st
	return st, nil
}

// buildFooterStatsLocked requires a.statsMu to be held by the caller.
func (a *App) buildFooterStatsLocked() sessionStats {
	st := sessionStats{
		PromptN:          a.tokenPromptTotal,
		PredictedN:       a.tokenPredictedTotal,
		CacheReadTotal:   a.tokenCacheReadTotal,
		CacheWriteTotal:  a.tokenCacheWriteTotal,
		SpeedTokPerSec:   a.lastTurnSpeed,
		ContextEstimate:  a.tokenSessionEstimate,
		ContextProjected: a.tokenSessionProjected,
		ContextMax:       a.tokenSessionMax,
		ToolCalls:        a.toolCallsTotal,
		ToolCallLevel:    a.toolCallWarningLevel,
	}
	applyPricing(&st, a.subs.cfg, a.subs.cfg.ActiveModel)
	st.MicroCompacts = a.microCompacts
	st.Compacts = a.compacts
	st.CacheMissesFull = a.tokenCacheFullMisses
	st.CacheMissesPartial = a.tokenCachePartialMisses
	st.CacheMissedTokens = a.tokenCacheMissedTokens
	st.LastCacheHit = a.lastCacheHit
	st.Compactions = append([]CompactionRound(nil), a.compactions...)
	return st
}

// applyPricing computes cost and pricing-related visibility flags for the
// given session stats using the model identified by activeModelID.
//
// Pricing resolution order (first match wins):
//  1. User-configured pricing on the model's config entry (YAML) — explicit
//     override, always honored.
//  2. The built-in model registry's cost data (models.go), keyed by the
//     config entry's real model name (ModelConfig.Model), then by the config
//     ID itself. This is the bridge that makes cache-aware cost work out of
//     the box for known models, without requiring YAML cache rates.
func applyPricing(st *sessionStats, cfg *config.Config, activeModelID string) {
	pricing := resolvePricing(cfg, activeModelID)
	if pricing == nil {
		return
	}
	st.CostUSD = computeCost(st.PromptN, st.PredictedN, st.CacheReadTotal, st.CacheWriteTotal, pricing)
	if st.CostUSD > 0 || pricing.InputPer1M > 0 || pricing.OutputPer1M > 0 {
		st.ShowCost = true
	}
}

// resolvePricing returns the effective per-1M pricing for a model: the user's
// config override when present, otherwise the built-in registry cost converted
// from per-token to per-1M. Returns nil when no pricing is known.
func resolvePricing(cfg *config.Config, activeModelID string) *config.PricingConfig {
	modelCfg := cfg.GetModelByID(activeModelID)
	if modelCfg != nil && modelCfg.Pricing != nil {
		return modelCfg.Pricing // explicit user override
	}
	// Built-in registry fallback: try the real API model name, then the config ID.
	var names []string
	if modelCfg != nil && modelCfg.Model != "" {
		names = append(names, modelCfg.Model)
	}
	names = append(names, activeModelID)
	for _, name := range names {
		if m := models.GetModel(name); m != nil && hasBuiltinCost(m.Cost) {
			p := builtinCostToPricing(m.Cost)
			return &p
		}
	}
	return nil
}

// hasBuiltinCost reports whether a registry cost entry carries any non-zero rate.
func hasBuiltinCost(c provider.ModelPricing) bool {
	return c.Input != 0 || c.Output != 0 || c.CacheRead != 0 || c.CacheWrite != 0
}

// builtinCostToPricing maps the registry's ModelPricing onto the per-1M
// PricingConfig used by computeCost. Registry cost values (models.go and
// models_generated.go alike) are per-token rates; PricingConfig is per 1M
// tokens, so the rates scale by 1e6.
func builtinCostToPricing(c provider.ModelPricing) config.PricingConfig {
	return config.PricingConfig{
		InputPer1M:      c.Input * 1e6,
		OutputPer1M:     c.Output * 1e6,
		CacheReadPer1M:  c.CacheRead * 1e6,
		CacheWritePer1M: c.CacheWrite * 1e6,
	}
}

// computeCost computes cumulative cost from token totals and the model's pricing config.
// friendlyConnectionHint translates a raw connection error into a user-friendly
// message with an actionable hint.
