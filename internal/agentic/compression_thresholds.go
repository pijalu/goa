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
// The three layers, from lowest to highest:
//
//   - SoftPercent: early maintenance. At/above it, the soft-layer strategy
//     runs (any strategy; default micro), and only when the provider prefix
//     cache is presumed cold. 0 = disabled.
//   - TriggerPercent: the trigger-layer (medium) strategy fires. This is
//     the main trigger, equivalent to the legacy ThresholdPercent. 0 = disabled.
//   - HardPercent: emergency ceiling. Cache gates are bypassed and the
//     hard-layer strategy (default summarize) fires proactively. 0 = disabled
//     (the goa default config sets 95 explicitly).
type CompressionThresholds struct {
	// SoftPercent is the early-maintenance level. 0 = disabled (default).
	SoftPercent int
	// TriggerPercent is the main strategy trigger. 0 = disabled (default).
	TriggerPercent int
	// HardPercent is the proactive emergency ceiling. 0 = disabled;
	// negative values are accepted and also mean disabled (legacy opt-out).
	HardPercent int
}

// CompressionLayerStrategies selects the compression strategy per escalation
// layer. Zero fields use the defaults (soft: micro, trigger: tool_elision —
// or the legacy Strategy field when set — hard: summarize). Any strategy is
// allowed on any layer; the soft layer simply defaults to micro.
type CompressionLayerStrategies struct {
	// Soft is the early-maintenance strategy (default micro).
	Soft CompressionStrategy
	// Trigger is the main strategy (default: legacy Strategy, else tool_elision).
	Trigger CompressionStrategy
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

// resolvedThresholds is the fully-defaulted view of CompressionThresholds
// used by every gate (proactive, micro, silent-overflow, ceiling, limit),
// with the per-layer strategies resolved alongside the levels.
type resolvedThresholds struct {
	soft    int
	trigger int
	hard    int

	softStrategy    CompressionStrategy
	triggerStrategy CompressionStrategy
	hardStrategy    CompressionStrategy
}

// effectiveHard returns the hard ceiling to use for escalation math and the
// reactive ceiling enforcer: the configured value, or DefaultHardPercent when
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

// escalationPercent is the usage level above which cheap strategies (elision,
// micro) escalate to selective message removal during overflow recovery. It
// sits 5 points below the effective hard ceiling so the retry goes out with
// headroom; with the default hard=95 this reproduces the historical fixed 90%.
func (t resolvedThresholds) escalationPercent() int {
	e := t.effectiveHard() - 5
	if e < 1 {
		e = 1
	}
	return e
}

// deferralCeiling is the usage level above which cache-hot deferral is no
// longer allowed. The cache gate exists to avoid churning a hot provider
// prefix cache during cheap maintenance, but near the window the overflow
// risk beats cache churn: a deepseek-v4 session stayed cache-hot (99.7% hit)
// while deferrals suppressed every compression from the 50% trigger all the
// way to a provider-side rejection at 100%. The ceiling sits 10 points below
// the hard ceiling (never below the trigger, or deferral would be pointless).
func (t resolvedThresholds) deferralCeiling() int {
	c := t.effectiveHard() - 10
	if c < t.trigger {
		c = t.trigger
	}
	if c < 1 {
		c = 1
	}
	return c
}

// elisionTargetPercent is the usage level a hot-cache proactive tool_elision
// pass elides down to: far enough below the deferral ceiling that one cache
// bust buys many rounds of headroom (hysteresis), instead of re-busting the
// hot prefix cache every round as the count-based boundary advances with
// history growth (prefix-cache bust loop). Sits 20 points below the
// hard ceiling (default ≈75%).
func (t resolvedThresholds) elisionTargetPercent() int {
	target := t.effectiveHard() - 20
	if target < 1 {
		target = 1
	}
	return target
}

// ReactiveSavingsPercent is the minimum fraction of the context window a
// reactive ceiling pass must free, expressed as a percentage of the window
// (CM:13 design rule 4). The reactive enforcer (enforceContextCeiling)
// is destructive: every front-cut busts the provider prefix cache. A nibble
// that frees only enough to dip below the 95% ceiling re-busts on the very next
// tool result (the CM:13 session showed 13 busts / 58 drops). Cutting once to
// free ≥50% of the window buys many rounds of headroom per cache miss.
const ReactiveSavingsPercent = 50

// reactiveTargetPercent is the usage level the reactive ceiling enforcer cuts
// history down TO: the hard ceiling minus ReactiveSavingsPercent, so one
// destructive pass frees at least ReactiveSavingsPercent points of the window
// (design rule 4). With the default hard=95 the target is 45%, giving one
// cache bust ~50 points of headroom instead of re-busting every round. It never
// exceeds effectiveHard (no-op when savings is 0) and never goes below 1%.
func (t resolvedThresholds) reactiveTargetPercent() int {
	target := t.effectiveHard() - ReactiveSavingsPercent
	if target > t.effectiveHard() {
		target = t.effectiveHard()
	}
	if target < 1 {
		target = 1
	}
	return target
}

// --- Exported derived-percent helpers (CM:13 design rule 5) ---
//
// These let non-agentic packages (e.g. /config display in core/commands) show
// the derived compression limits — which are otherwise hidden because they are
// computed from the hard ceiling rather than stored as config. Each takes the
// raw configured hard percent (0 = disabled → DefaultHardPercent) so the caller
// does not need to construct a resolvedThresholds.

// EffectiveHardPercent returns the reactive ceiling actually in force for the
// given configured hard percent: the value itself, or DefaultHardPercent when 0.
func EffectiveHardPercent(hard int) int {
	r := resolvedThresholds{hard: hard}
	return r.effectiveHard()
}

// EscalationPercent returns the escalation level (effective hard − 5).
func EscalationPercent(hard int) int {
	return resolvedThresholds{hard: hard}.escalationPercent()
}

// DeferralCeilingPercent returns the cache-hot deferral cutoff (effective hard − 10).
func DeferralCeilingPercent(hard int) int {
	return resolvedThresholds{hard: hard}.deferralCeiling()
}

// ElisionTargetPercent returns the proactive elision hysteresis target (effective hard − 20).
func ElisionTargetPercent(hard int) int {
	return resolvedThresholds{hard: hard}.elisionTargetPercent()
}

// ReactiveTargetPercent returns the level a reactive ceiling cut targets
// (effective hard − ReactiveSavingsPercent).
func ReactiveTargetPercent(hard int) int {
	return resolvedThresholds{hard: hard}.reactiveTargetPercent()
}

// resolveThresholds folds the explicit Thresholds with the deprecated
// ThresholdPercent alias and the documented defaults, and resolves the
// per-layer strategies (legacy Strategy maps to the trigger layer).
func (c ContextCompressionConfig) resolveThresholds() resolvedThresholds {
	t := resolvedThresholds{
		soft:    c.Thresholds.SoftPercent,
		trigger: c.Thresholds.TriggerPercent,
		hard:    c.Thresholds.HardPercent,
	}
	// Deprecated alias: ThresholdPercent overrides Thresholds.TriggerPercent
	// when both are set (backwards compatibility).
	if c.ThresholdPercent > 0 {
		t.trigger = c.ThresholdPercent
	}
	// Opt-in semantics for every layer: 0 (or negative) disables it — no
	// level defaults to a positive value. The goa default config sets
	// hard_percent: 95 explicitly; the reactive paths always fall back to
	// effectiveHard (DefaultHardPercent) for their escalation math.
	if t.soft < 0 {
		t.soft = 0
	}
	if t.trigger < 0 {
		t.trigger = 0
	}

	// Layer strategies: explicit per-layer fields win; the legacy single
	// Strategy maps to the trigger layer; the soft layer defaults to micro.
	t.softStrategy = c.Strategies.Soft
	if t.softStrategy == "" {
		t.softStrategy = CompressionMicro
	}
	t.triggerStrategy = c.Strategies.Trigger
	if t.triggerStrategy == "" {
		t.triggerStrategy = c.Strategy
	}
	if t.triggerStrategy == "" {
		t.triggerStrategy = CompressionToolElision
	}
	t.hardStrategy = c.Strategies.Hard
	if t.hardStrategy == "" {
		// The default hard-layer actor is SUMMARIZE, not hybrid: at the 95%
		// ceiling the contract is a full-window LLM compaction; the hybrid
		// micro-pre-compression only applies when summarize itself overflows
		// (Compact's fallback), and the destructive ceiling message-drop is a
		// last resort when summarize cannot run at all.
		t.hardStrategy = CompressionSummarize
	}
	return t
}

// compressionTier is the escalation level selected for this turn.
type compressionTier int

const (
	// tierNone: usage below all actionable levels, or deferred for cache.
	tierNone compressionTier = iota
	// tierSoft: early maintenance — the soft-layer strategy.
	tierSoft
	// tierTrigger: the trigger-layer (medium) strategy fires.
	tierTrigger
	// tierHard: emergency — the hard-layer strategy fires, cache gate bypassed.
	tierHard
)

// proactiveTierLocked selects the compression tier for the current turn given
// the usage percentage and the cache state. The caller must hold a.mu
// (cacheAssumedColdForProactive reads lastTurnEnd). Every layer is opt-in
// (0 = off), so with the all-zero thresholds nothing fires proactively — the
// goa default config enables only the hard tier (hard_percent: 95, summarize).
//
// The usagePercent fed in is the PROJECTED occupancy (ContextStats.UsagePercent
// is computed from ProjectedTokens, CX8/P20): the provider-anchored figure
// that reacts immediately when a large tool result lands, so the trigger fires
// on what the next request would actually cost rather than the stale
// full-surface heuristic estimate.
//
// Escalation rules:
//   - hard tier enabled and usage >= effectiveHard → hard tier, cache gate
//     bypassed (overflow risk beats cache churn).
//   - cache hot and usage < deferralCeiling → defer everything (tierNone).
//   - cache hot, trigger > 0 and usage >= deferralCeiling → trigger tier.
//   - trigger > 0 and usage >= trigger → trigger tier.
//   - soft > 0 and usage >= soft → soft tier.
func (a *Agent) proactiveTierLocked(usagePercent int, rt resolvedThresholds) compressionTier {
	if rt.hardEnabled() && usagePercent >= rt.effectiveHard() {
		return tierHard
	}
	if !a.cfg.ContextCompression.DisableCacheGate && !a.cacheAssumedColdForProactive() {
		if rt.trigger > 0 && usagePercent >= rt.deferralCeiling() {
			return tierTrigger
		}
		if rt.trigger > 0 && usagePercent >= rt.trigger {
			a.logDeferral(usagePercent)
		}
		return tierNone
	}
	if rt.trigger > 0 && usagePercent >= rt.trigger {
		return tierTrigger
	}
	if rt.soft > 0 && usagePercent >= rt.soft {
		return tierSoft
	}
	return tierNone
}

// logDeferral records a cache-hot deferral at Info level: deferrals between
// the trigger and the deferral ceiling suppress compression the user asked
// for, so they must be visible in the default (info) log — a silent Debug
// line once hid a whole session's worth of skipped compressions.
func (a *Agent) logDeferral(usagePercent int) {
	if a.cfg.Logger != nil {
		a.cfg.Logger.Log(Info, "proactive compression deferred: provider cache presumed hot (usage=%d%%)", usagePercent)
	}
}
