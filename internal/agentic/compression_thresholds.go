// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

// CompressionThresholds defines the fill levels — percent of the effective
// context window — at which compression behavior escalates. Every layer is
// OPT-IN: 0 (or a negative value) disables that layer. There is no engine
// default-on layer; the goa embedded default config sets the hard ceiling
// explicitly (hard_percent: 95, strategies.hard: summarize).
//
// Rationale: proactive compression (esp. tool_elision) busts the provider
// prefix cache and re-bills most of the context for a modest headroom gain,
// so nothing fires unless explicitly enabled. At the hard ceiling the
// overflow risk beats cache churn: the actor is the hard-layer strategy
// (default summarize) — a full-window LLM compaction — not a destructive
// message drop. The reactive safety net — overflow recovery on a
// context-length error (handleContextError → the on-error strategy,
// default hybrid: elision → selective → summarize) — is controlled by
// OnContextError and stays on regardless of these thresholds.
//
// The model is EXACTLY soft / hard / (on-)error — there is no "trigger" layer.
// The two proactive layers, from lowest to highest:
//
//   - SoftPercent: early maintenance. At/above it, the soft-layer strategy
//     runs (any strategy; default micro), and only when the provider prefix
//     cache is presumed cold. 0 = disabled.
//   - HardPercent: emergency ceiling. Cache gates are bypassed and the
//     hard-layer strategy (default summarize) fires proactively. 0 = disabled
//     (the goa default config sets 95 explicitly).
type CompressionThresholds struct {
	// SoftPercent is the early-maintenance level. 0 = disabled (default).
	SoftPercent int
	// HardPercent is the proactive emergency ceiling. 0 = disabled;
	// negative values are accepted and also mean disabled (legacy opt-out).
	HardPercent int
}

// CompressionLayerStrategies selects the compression strategy per escalation
// layer (soft / hard). Zero fields use the defaults (soft: micro, hard:
// summarize). Any strategy is allowed on any layer; the soft layer simply
// defaults to micro.
type CompressionLayerStrategies struct {
	// Soft is the early-maintenance strategy (default micro).
	Soft CompressionStrategy
	// Hard is the emergency strategy fired at the hard ceiling (default
	// summarize — the destructive message-drop ceiling is only a last resort
	// when summarize cannot run at all).
	Hard CompressionStrategy
}

// DefaultHardPercent is the fallback hard ceiling used for escalation math
// (escalationPercent, deferralCeiling, elisionTargetPercent) and by the
// reactive paths when no explicit hard ceiling is configured. It is NOT an
// implicit default-on layer: with HardPercent unset (0) the proactive hard
// tier is disabled; the goa embedded default config sets hard_percent: 95
// explicitly.
const DefaultHardPercent = 95

// resolvedThresholds is the fully-defaulted view of the compression layers,
// used by every gate (proactive, micro, ceiling, limit), with the per-layer
// strategies resolved alongside the levels. The model is EXACTLY soft / hard /
// (on-)error: there is no "trigger" layer and no derived hard−N magic levels.
type resolvedThresholds struct {
	soft int
	hard int

	softStrategy CompressionStrategy
	hardStrategy CompressionStrategy
}

// effectiveHard returns the hard ceiling to use for the tier gate and the
// reactive ceiling fallback: the configured value, or DefaultHardPercent when
// the proactive hard layer is disabled (0 or negative). This keeps the safety
// net working even when the proactive hard tier is fully opt-out.
func (t resolvedThresholds) effectiveHard() int {
	if t.hard > 0 {
		return t.hard
	}
	return DefaultHardPercent
}

// hardEnabled reports whether the proactive hard tier can fire: only a
// positive value enables it (the goa default config sets 95). 0 and negative
// values disable the proactive hard tier while leaving the reactive safety
// net (effectiveHard) intact.
func (t resolvedThresholds) hardEnabled() bool {
	return t.hard > 0
}

// EffectiveHardPercent returns the reactive ceiling actually in force for the
// given configured hard percent: the value itself, or DefaultHardPercent when 0.
func EffectiveHardPercent(hard int) int {
	r := resolvedThresholds{hard: hard}
	return r.effectiveHard()
}

// resolveThresholds folds the explicit thresholds with the documented defaults
// and resolves the per-layer strategies. The model is exactly soft / hard /
// (on-)error; on-error strategy is resolved separately (onErrorStrategy).
func (c ContextCompressionConfig) resolveThresholds() resolvedThresholds {
	t := resolvedThresholds{
		soft: c.Thresholds.SoftPercent,
		hard: c.Thresholds.HardPercent,
	}
	// Opt-in semantics: 0 (or negative) disables a layer. The goa default
	// config sets hard_percent: 95 explicitly; the reactive fallback always
	// falls back to effectiveHard (DefaultHardPercent).
	if t.soft < 0 {
		t.soft = 0
	}

	// Layer strategies: the soft layer defaults to micro (cheap maintenance);
	// the hard layer defaults to SUMMARIZE — at the ceiling the contract is a
	// full-window LLM compaction, with the destructive message-drop ("hard
	// fallback") reserved for when summarize cannot fit the window.
	t.softStrategy = c.Strategies.Soft
	if t.softStrategy == "" {
		t.softStrategy = CompressionMicro
	}
	t.hardStrategy = c.Strategies.Hard
	if t.hardStrategy == "" {
		t.hardStrategy = CompressionSummarize
	}
	return t
}

// compressionTier is the escalation level selected for this turn.
type compressionTier int

const (
	// tierNone: usage below all actionable levels, or soft deferred for cache.
	tierNone compressionTier = iota
	// tierSoft: early maintenance — the soft-layer strategy.
	tierSoft
	// tierHard: emergency — the hard-layer strategy fires, cache gate bypassed.
	tierHard
)

// proactiveTierLocked selects the compression tier for the current turn given
// the usage percentage and the cache state. The caller must hold a.mu
// (cacheAssumedColdForProactive reads lastTurnEnd). Every layer is opt-in
// (0 = off); the goa default config enables only the hard tier
// (hard_percent: 95, summarize).
//
// The usagePercent fed in is the PROJECTED occupancy (ContextStats.UsagePercent
// is computed from ProjectedTokens, CX8/P20): the provider-anchored figure that
// reacts immediately when a large tool result lands.
//
// Rules (soft/hard only — no trigger, no derived levels):
//   - hard tier enabled and usage >= effectiveHard → hard tier, cache gate
//     bypassed (overflow risk beats cache churn; hard ALWAYS fires).
//   - soft > 0 and usage >= soft → soft tier, UNLESS the provider cache is
//     presumed hot (cheap in-place maintenance churns the hot prefix cache, so
//     it is deferred); the hard tier is never deferred.
func (a *Agent) proactiveTierLocked(usagePercent int, rt resolvedThresholds) compressionTier {
	if rt.hardEnabled() && usagePercent >= rt.effectiveHard() {
		return tierHard
	}
	if rt.soft > 0 && usagePercent >= rt.soft {
		// Simplified cache gate: defer ONLY soft maintenance while the cache is
		// hot (the hard tier above already returned, bypassing the gate).
		if !a.cfg.ContextCompression.DisableCacheGate && !a.cacheAssumedColdForProactive() {
			a.logDeferral(usagePercent)
			return tierNone
		}
		return tierSoft
	}
	return tierNone
}

// logDeferral records a cache-hot deferral of the SOFT layer at Info level:
// the soft maintenance the user configured was skipped to preserve a hot
// provider cache, so it must be visible in the default (info) log.
func (a *Agent) logDeferral(usagePercent int) {
	if a.cfg.Logger != nil {
		a.cfg.Logger.Log(Info, "soft compression deferred: provider cache presumed hot (usage=%d%%)", usagePercent)
	}
}
