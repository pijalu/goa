// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

// CompressionThresholds defines the fill levels — percent of the effective
// context window — at which compression behavior escalates. The soft and
// trigger layers are OPT-IN: 0 disables that layer (negative values are
// treated as 0). The hard layer is the exception: 0 means the documented
// default (DefaultHardPercent, 95) so the hard tier is the ONLY proactive
// layer on by default, and a negative value is an explicit opt-out.
//
// Rationale: proactive compression (esp. tool_elision) busts the provider
// prefix cache and re-bills most of the context for a modest headroom gain,
// so everything below the hard layer is OFF unless explicitly enabled. At
// the hard ceiling the overflow risk beats cache churn: the default actor is
// the hard-layer strategy (summarize) — a full-window LLM compaction — not a
// destructive message drop. The reactive safety net — overflow recovery on
// a context-length error (handleContextError → hybrid: elision → selective
// → summarize) and the hard-ceiling message-drop enforcer (LAST resort) —
// stays on regardless and uses effectiveHard for its escalation math.
//
// The three layers, from lowest to highest:
//
//   - SoftPercent: early, cheap maintenance. At/above it, the soft-layer
//     strategy (zero-LLM only: micro compaction or tool elision) runs, and
//     only when the provider prefix cache is presumed cold. 0 = disabled.
//   - TriggerPercent: the trigger-layer (medium) strategy fires. This is
//     the main trigger, equivalent to the legacy ThresholdPercent. 0 = disabled.
//   - HardPercent: emergency ceiling. Cache gates are bypassed and the
//     hard-layer strategy (default summarize) fires proactively. 0 = the
//     DefaultHardPercent (95) — the only proactive layer on by default; a
//     negative value explicitly disables the proactive hard tier (the
//     reactive ceiling enforcer and overflow recovery still protect the
//     window via effectiveHard).
type CompressionThresholds struct {
	// SoftPercent is the early-maintenance level. 0 = disabled (default).
	SoftPercent int
	// TriggerPercent is the main strategy trigger. 0 = disabled (default).
	TriggerPercent int
	// HardPercent is the proactive emergency ceiling. 0 = default 95 (the
	// only proactive layer on by default); negative = explicitly disabled.
	HardPercent int
}

// CompressionLayerStrategies selects the compression strategy per escalation
// layer. Zero fields use the defaults (soft: micro, trigger: tool_elision —
// or the legacy Strategy field when set — hard: summarize). The soft layer is
// restricted to zero-LLM strategies; anything else degrades to micro.
type CompressionLayerStrategies struct {
	// Soft is the early-maintenance strategy (micro|tool_elision; default micro).
	Soft CompressionStrategy
	// Trigger is the main strategy (default: legacy Strategy, else tool_elision).
	Trigger CompressionStrategy
	// Hard is the emergency strategy fired at the hard ceiling (default
	// summarize — the destructive message-drop ceiling is only a last resort
	// when summarize cannot run at all).
	Hard CompressionStrategy
}

// DefaultHardPercent is the default hard ceiling: with HardPercent unset (0)
// the hard tier fires proactively at 95% of the effective window using the
// hard-layer strategy (summarize). It is also the fallback used for escalation
// math (escalationPercent, deferralCeiling, elisionTargetPercent) and the
// reactive ceiling enforcer when no explicit hard ceiling is configured.
// Soft/trigger do not default: 0 disables each layer (opt-in), so there are no
// DefaultSoftPercent/DefaultTriggerPercent constants.
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

// hardEnabled reports whether the proactive hard tier can fire: any value
// >= 0 enables it (0 = the DefaultHardPercent ceiling); a negative value is
// an explicit opt-out of the proactive hard tier while leaving the reactive
// safety net (effectiveHard) intact.
func (t resolvedThresholds) hardEnabled() bool {
	return t.hard >= 0
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
	// Opt-in semantics for the soft/trigger layers: 0 (or negative) disables
	// them — no level defaults to a positive value. The hard layer keeps its
	// sign: 0 resolves to the default 95 ceiling (hardEnabled), and a negative
	// value is an explicit opt-out of the proactive hard tier (the reactive
	// paths still use effectiveHard).
	if t.soft < 0 {
		t.soft = 0
	}
	if t.trigger < 0 {
		t.trigger = 0
	}

	// Layer strategies: explicit per-layer fields win; the legacy single
	// Strategy maps to the trigger layer; the soft layer is zero-LLM only.
	t.softStrategy = zeroLLMStrategy(c.Strategies.Soft, CompressionMicro)
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

// zeroLLMStrategy validates a soft-layer strategy: only strategies that
// never call the LLM and never drop messages are allowed; anything else
// (including empty) falls back to the provided default.
func zeroLLMStrategy(s, fallback CompressionStrategy) CompressionStrategy {
	switch s {
	case CompressionToolElision, CompressionMicro:
		return s
	case "":
		return fallback
	default:
		return CompressionMicro
	}
}

// compressionTier is the escalation level selected for this turn.
type compressionTier int

const (
	// tierNone: usage below all actionable levels, or deferred for cache.
	tierNone compressionTier = iota
	// tierSoft: early maintenance — the zero-LLM soft-layer strategy.
	tierSoft
	// tierTrigger: the trigger-layer (medium) strategy fires.
	tierTrigger
	// tierHard: emergency — the hard-layer strategy fires, cache gate bypassed.
	tierHard
)

// proactiveTierLocked selects the compression tier for the current turn given
// the usage percentage and the cache state. The caller must hold a.mu
// (cacheAssumedColdForProactive reads lastTurnEnd). The soft/trigger layers
// are opt-in (0 = off); the hard tier is on by default (0 → the
// DefaultHardPercent ceiling, negative = explicit opt-out), so with the
// default all-zero thresholds usage below 95% does nothing and usage at/above
// 95% runs the hard-layer strategy (summarize).
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
