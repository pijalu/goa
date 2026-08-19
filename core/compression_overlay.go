package core

import "github.com/pijalu/goa/config"

func applyCompressionOverride(ov *compressionOverlay, o config.ModelCompressionOverride) {
	if o.MaxTokens != 0 {
		ov.maxTokens = o.MaxTokens
	}
	if o.Strategy != "" {
		ov.strategy = o.Strategy
	}
	if o.PreserveRecentTurns != 0 {
		ov.preserveRecentTurns = o.PreserveRecentTurns
	}
	if o.ThresholdPercent != 0 {
		ov.legacyTrigger = o.ThresholdPercent
	}
	if o.Thresholds.SoftPercent != 0 {
		ov.thresholds.SoftPercent = o.Thresholds.SoftPercent
	}
	if o.Thresholds.TriggerPercent != 0 {
		ov.thresholds.TriggerPercent = o.Thresholds.TriggerPercent
	}
	if o.Thresholds.HardPercent != 0 {
		ov.thresholds.HardPercent = o.Thresholds.HardPercent
	}
	if o.Strategies.Soft != "" {
		ov.strategies.Soft = o.Strategies.Soft
	}
	if o.Strategies.Trigger != "" {
		ov.strategies.Trigger = o.Strategies.Trigger
	}
	if o.Strategies.Hard != "" {
		ov.strategies.Hard = o.Strategies.Hard
	}
	if o.CacheGate != "" {
		ov.cacheGate = o.CacheGate
	}
}
