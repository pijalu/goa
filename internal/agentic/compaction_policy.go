// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import "time"

// CompactionPolicyDecision is the action selected for the next request. It is
// deliberately a value, rather than an instruction to mutate history: callers
// decide how (and whether) to execute the selected strategy.
type CompactionPolicyDecision uint8

const (
	Noop CompactionPolicyDecision = iota
	SoftMaintenance
	HighMarkCompaction
	EmergencyFallback
)

func (d CompactionPolicyDecision) String() string {
	switch d {
	case SoftMaintenance:
		return "soft_maintenance"
	case HighMarkCompaction:
		return "high_mark_compaction"
	case EmergencyFallback:
		return "emergency_fallback"
	default:
		return "noop"
	}
}

// CompactionPolicyInput contains the complete immutable snapshot used by the
// policy. Marks are percentages of MaxTokens (0 disables a non-hard mark).
// HardPercent is an unconditional ceiling: cache state and strategy
// availability can never suppress EmergencyFallback.
type CompactionPolicyInput struct {
	EstimatedTokens int
	// NextRequestTokens, when set, is the projected prompt plus expected suffix.
	// It models the request that will be sent after this policy pass rather than
	// treating the current transcript size as the complete risk signal.
	NextRequestTokens int
	// ReserveTokens covers output/reasoning and protocol overhead that must fit
	// alongside the next prompt.
	ReserveTokens int
	// MarginTokens is configurable proactive headroom beyond the mark.
	MarginTokens                                                                 int
	MaxTokens                                                                    int
	SoftPercent                                                                  int
	HighMarkPercent                                                              int
	HardPercent                                                                  int
	CacheHot                                                                     bool
	LastTurnAt                                                                   time.Time
	Now                                                                          time.Time
	CacheTTL                                                                     time.Duration
	SoftStrategyAvailable, HighMarkStrategyAvailable, EmergencyStrategyAvailable bool
	// RemoteCompactAvailable reports that the active provider/model exposes a
	// usable server-side compaction endpoint (Codex Phase 2b) AND the operator
	// has opted in via features.remote_compaction. It is purely an availability
	// input: 2b.1 does not select a remote strategy on it, so the default
	// (false) leaves the local ladder unchanged. 2b.2 consumes this to prefer
	// the remote /responses/compact strategy.
	RemoteCompactAvailable bool
}

// DecideCompactionPolicy is a pure compaction policy decision primitive. It
// reads no agent state and changes neither history nor timestamps. A high-mark
// decision is based on the risk of the next request (the estimated occupancy
// snapshot), not merely on the amount of retained memory.
func DecideCompactionPolicy(in CompactionPolicyInput) CompactionPolicyDecision {
	if in.MaxTokens <= 0 {
		return Noop
	}
	usageTokens := in.EstimatedTokens
	if in.NextRequestTokens > 0 {
		usageTokens = in.NextRequestTokens
	}
	usageTokens += maxInt(in.ReserveTokens, 0) + maxInt(in.MarginTokens, 0)
	usage := percentOf(usageTokens, in.MaxTokens)
	hard := in.HardPercent
	if hard > 0 && usage >= hard {
		return EmergencyFallback
	}
	if highMarkReached(usage, in.HighMarkPercent, hard) && in.HighMarkStrategyAvailable {
		return HighMarkCompaction
	}
	if softMaintenanceReady(usage, in) {
		return SoftMaintenance
	}
	return Noop
}

func highMarkReached(usage, high, hard int) bool {
	if high <= 0 {
		high = hard
	}
	return hard > 0 && high < hard && usage >= high
}

func softMaintenanceReady(usage int, in CompactionPolicyInput) bool {
	return in.SoftPercent > 0 && usage >= in.SoftPercent && in.SoftStrategyAvailable && !policyCacheHot(in)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// CompactionStrategiesOrdered returns the least destructive strategy order used
// by proactive maintenance. Keeping this order in one primitive prevents a
// caller from accidentally escalating straight to summarization.
func CompactionStrategiesOrdered() []CompressionStrategy {
	return []CompressionStrategy{
		CompressionToolElision,
		CompressionMicro,
		CompressionSelective,
		CompressionSummarize,
	}
}

func percentOf(usage, max int) int {
	if usage <= 0 || max <= 0 {
		return 0
	}
	// Split before multiplying so a large provider estimate cannot overflow
	// while evaluating policy.
	return usage/max*100 + usage%max*100/max
}

func policyCacheHot(in CompactionPolicyInput) bool {
	if in.CacheHot {
		return true
	}
	if in.CacheTTL <= 0 || in.LastTurnAt.IsZero() || in.Now.IsZero() {
		return false
	}
	return in.Now.Sub(in.LastTurnAt) < in.CacheTTL
}
