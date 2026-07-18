// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

// CompressionThresholds defines the fill levels — percent of the effective
// context window — at which compression behavior escalates. All fields are
// optional; zero means "use the default" (soft: disabled).
//
// The three tiers, from lowest to highest:
//
//   - SoftPercent: early, cheap maintenance. At/above it, zero-LLM strategies
//     (micro compaction, tool elision) may run, and only when the provider
//     prefix cache is presumed cold. Never blocks, never calls the LLM.
//     0 disables the soft tier (default).
//   - TriggerPercent: the configured compression strategy fires. This is the
//     main trigger, equivalent to the legacy ThresholdPercent.
//   - HardPercent: emergency ceiling. Cache gates are bypassed, cheap
//     strategies escalate to selective, the ceiling enforcer drops oldest
//     messages, and new turns are refused above it.
type CompressionThresholds struct {
	// SoftPercent is the early-maintenance level. 0 = disabled (default).
	SoftPercent int
	// TriggerPercent is the main strategy trigger. 0 = default 90.
	TriggerPercent int
	// HardPercent is the emergency ceiling. 0 = default 95.
	HardPercent int
}

// Default threshold values. DefaultTriggerPercent preserves the historical
// SDK fallback; the app's embedded config sets an explicit 80.
const (
	DefaultTriggerPercent = 90
	DefaultHardPercent    = 95
)

// resolvedThresholds is the fully-defaulted view of CompressionThresholds
// used by every gate (proactive, micro, silent-overflow, ceiling, limit).
type resolvedThresholds struct {
	soft    int
	trigger int
	hard    int
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

// resolveThresholds folds the explicit Thresholds with the deprecated
// ThresholdPercent alias and the documented defaults. The legacy alias wins
// when both are set, so existing configs keep their exact behavior.
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
	if t.trigger <= 0 {
		t.trigger = DefaultTriggerPercent
	}
	if t.hard <= 0 {
		t.hard = DefaultHardPercent
	}
	if t.soft < 0 {
		t.soft = 0
	}
	return t
}

// compressionTier is the escalation level selected for this turn.
type compressionTier int

const (
	// tierNone: usage below all actionable levels, or deferred for cache.
	tierNone compressionTier = iota
	// tierSoft: early maintenance — cheap zero-LLM strategies only.
	tierSoft
	// tierTrigger: the configured strategy fires.
	tierTrigger
)

// proactiveTierLocked selects the compression tier for the current turn given
// the usage percentage and the cache state. The caller must hold a.mu
// (cacheAssumedColdForProactive reads lastTurnEnd).
//
// Escalation rules:
//   - usage >= hard → trigger tier, cache gate bypassed (overflow risk beats
//     cache churn).
//   - cache hot → defer everything (tierNone), regardless of level.
//   - usage >= trigger → trigger tier.
//   - usage >= soft (and soft enabled) → soft tier.
func (a *Agent) proactiveTierLocked(usagePercent int, rt resolvedThresholds) compressionTier {
	if usagePercent >= rt.hard {
		return tierTrigger
	}
	if !a.cacheAssumedColdForProactive() {
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

func (a *Agent) logDeferral(usagePercent int) {
	if a.cfg.Logger != nil {
		a.cfg.Logger.Log(Debug, "proactive compression deferred: provider cache presumed hot (usage=%d%%)", usagePercent)
	}
}

// softStrategy maps the configured strategy to the zero-LLM strategy allowed
// at the soft tier. Tool elision passes through; micro stays micro; anything
// else (summarize, hybrid, selective — LLM-costly or destructive) degrades to
// micro compaction so early maintenance never calls the LLM and never drops
// messages.
func softStrategy(configured CompressionStrategy) CompressionStrategy {
	switch configured {
	case CompressionToolElision, "":
		return CompressionToolElision
	case CompressionMicro:
		return CompressionMicro
	default:
		return CompressionMicro
	}
}
