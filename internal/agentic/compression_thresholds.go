// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

// CompressionThresholds defines the fill levels — percent of the effective
// context window — at which compression behavior escalates. All fields are
// optional; zero means "use the default" (soft: 80, trigger: 90, hard: 95).
//
// The three layers, from lowest to highest:
//
//   - SoftPercent: early, cheap maintenance. At/above it, the soft-layer
//     strategy (zero-LLM only: micro compaction or tool elision) runs, and
//     only when the provider prefix cache is presumed cold. Never blocks,
//     never calls the LLM. 0 = default 80; negative disables the layer.
//   - TriggerPercent: the trigger-layer (medium) strategy fires. This is
//     the main trigger, equivalent to the legacy ThresholdPercent.
//   - HardPercent: emergency ceiling. Cache gates are bypassed and the
//     hard-layer strategy (default hybrid) fires; the ceiling enforcer
//     drops oldest messages as a last resort.
type CompressionThresholds struct {
	// SoftPercent is the early-maintenance level. 0 = default 80, negative = disabled.
	SoftPercent int
	// TriggerPercent is the main strategy trigger. 0 = default 90.
	TriggerPercent int
	// HardPercent is the emergency ceiling. 0 = default 95.
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

// Default threshold values. DefaultTriggerPercent preserves the historical
// SDK fallback; the app's embedded config sets an explicit 80.
const (
	DefaultSoftPercent    = 80
	DefaultTriggerPercent = 90
	DefaultHardPercent    = 95
)

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

// escalationPercent is the usage level above which cheap strategies (elision,
// micro) escalate to selective message removal during overflow recovery. It
// sits 5 points below the hard ceiling so the retry goes out with headroom;
// with the default hard=95 this reproduces the historical fixed 90%.
func (t resolvedThresholds) escalationPercent() int {
	e := t.hard - 5
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
	c := t.hard - 10
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
	target := t.hard - 20
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
	if t.soft == 0 {
		t.soft = DefaultSoftPercent
	} else if t.soft < 0 {
		t.soft = 0 // negative disables the soft layer
	}
	if t.trigger <= 0 {
		t.trigger = DefaultTriggerPercent
	}
	if t.hard <= 0 {
		t.hard = DefaultHardPercent
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
// (cacheAssumedColdForProactive reads lastTurnEnd).
//
// Escalation rules:
//   - usage >= hard → hard tier, cache gate bypassed (overflow risk beats
//     cache churn).
//   - cache hot and usage < deferralCeiling → defer everything (tierNone).
//   - cache hot and usage >= deferralCeiling → trigger tier (too close to
//     the window to keep protecting the cache).
//   - usage >= trigger → trigger tier.
//   - usage >= soft (and soft enabled) → soft tier.
func (a *Agent) proactiveTierLocked(usagePercent int, rt resolvedThresholds) compressionTier {
	if usagePercent >= rt.hard {
		return tierHard
	}
	if !a.cfg.ContextCompression.DisableCacheGate && !a.cacheAssumedColdForProactive() {
		if usagePercent >= rt.deferralCeiling() {
			return tierTrigger
		}
		if usagePercent >= rt.trigger {
			a.logDeferral(usagePercent)
		}
		return tierNone
	}
	if usagePercent >= rt.trigger {
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