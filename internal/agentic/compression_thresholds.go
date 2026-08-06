// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

// CompressionThresholds defines the fill levels — percent of the effective
// context window — at which compression behavior escalates. Every layer is
// OPT-IN: 0 disables that layer (the default is NO proactive, threshold-
// triggered compression). Positive values enable the layer at that percent;
// negative values are treated as 0 (disabled).
//
// Rationale: proactive compression (esp. tool_elision) busts the provider
// prefix cache and re-bills most of the context for a modest headroom gain,
// so it is OFF unless explicitly enabled. The reactive safety net — overflow
// recovery on a context-length error (handleContextError → hybrid: elision →
// selective → summarize) and the hard-ceiling message-drop enforcer — stays
// on regardless and uses effectiveHard for its escalation math.
//
// The three layers, from lowest to highest:
//
//   - SoftPercent: early, cheap maintenance. At/above it, the soft-layer
//     strategy (zero-LLM only: micro compaction or tool elision) runs, and
//     only when the provider prefix cache is presumed cold. 0 = disabled.
//   - TriggerPercent: the trigger-layer (medium) strategy fires. This is
//     the main trigger, equivalent to the legacy ThresholdPercent. 0 = disabled.
//   - HardPercent: emergency ceiling. Cache gates are bypassed and the
//     hard-layer strategy (default hybrid) fires proactively. 0 = disabled
//     (the reactive ceiling enforcer and overflow recovery still protect the
//     window; they use effectiveHard, not this value, when it is 0).
type CompressionThresholds struct {
	// SoftPercent is the early-maintenance level. 0 = disabled (default).
	SoftPercent int
	// TriggerPercent is the main strategy trigger. 0 = disabled (default).
	TriggerPercent int
	// HardPercent is the proactive emergency ceiling. 0 = disabled (default).
	HardPercent int
}

// CompressionLayerStrategies selects the compression strategy per escalation
// layer. Zero fields use the defaults (soft: micro, trigger: tool_elision —
// or the legacy Strategy field when set — hard: hybrid). The soft layer is
// restricted to zero-LLM strategies; anything else degrades to micro.
type CompressionLayerStrategies struct {
	// Soft is the early-maintenance strategy (micro|tool_elision; default micro).
	Soft CompressionStrategy
	// Trigger is the main strategy (default: legacy Strategy, else tool_elision).
	Trigger CompressionStrategy
	// Hard is the emergency strategy fired at the hard ceiling (default hybrid).
	Hard CompressionStrategy
}

// DefaultHardPercent is the fallback emergency ceiling used for escalation
// math (escalationPercent, deferralCeiling, elisionTargetPercent) and the
// reactive ceiling enforcer when no explicit hard ceiling is configured.
// Proactive thresholds no longer default: 0 disables each layer (opt-in),
// so there are no DefaultSoftPercent/DefaultTriggerPercent constants.
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
// the proactive hard layer is disabled (0). This keeps the safety net working
// even when proactive threshold compression is fully opt-in / off.
func (t resolvedThresholds) effectiveHard() int {
	if t.hard > 0 {
		return t.hard
	}
	return DefaultHardPercent
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
// history growth (bugs.md prefix-cache bust loop). Sits 20 points below the
// hard ceiling (default ≈75%).
func (t resolvedThresholds) elisionTargetPercent() int {
	target := t.effectiveHard() - 20
	if target < 1 {
		target = 1
	}
	return target
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
	// Opt-in semantics: 0 (or negative) disables each layer. No level defaults
	// to a positive value — proactive compression is off unless configured.
	if t.soft < 0 {
		t.soft = 0
	}
	if t.trigger < 0 {
		t.trigger = 0
	}
	if t.hard < 0 {
		t.hard = 0
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
		t.hardStrategy = CompressionHybrid
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
// (cacheAssumedColdForProactive reads lastTurnEnd). Every layer is opt-in: a
// threshold of 0 disables that tier entirely, so with the default all-zero
// thresholds this always returns tierNone (no proactive compression).
//
// Escalation rules (each gated on its threshold being enabled, > 0):
//   - hard > 0 and usage >= hard → hard tier, cache gate bypassed.
//   - cache hot and usage < deferralCeiling → defer everything (tierNone).
//   - cache hot, trigger > 0 and usage >= deferralCeiling → trigger tier.
//   - trigger > 0 and usage >= trigger → trigger tier.
//   - soft > 0 and usage >= soft → soft tier.
func (a *Agent) proactiveTierLocked(usagePercent int, rt resolvedThresholds) compressionTier {
	if rt.hard > 0 && usagePercent >= rt.hard {
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
