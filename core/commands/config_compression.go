// SPDX-License-Identifier: GPL-3.0-or-later

package commands

import (
	"fmt"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/tui"
)

func (m *configMenu) settingCompression() {
	m.current = m.settingCompression
	cfg := m.ctx.Config
	strategy := cfg.ContextCompression.Strategy
	if strategy == "" {
		strategy = "tool_elision"
	}
	trigger := compressionTriggerDisplay(cfg)
	hardPct := cfg.ContextCompression.Thresholds.HardPercent
	items := []tui.SelectorItem{
		{Value: "strategy", Label: "Trigger strategy", Description: strategy},
		{Value: "soft_strategy", Label: "Soft strategy", Description: layerStrategyLabel(cfg.ContextCompression.Strategies.Soft, "micro")},
		{Value: "hard_strategy", Label: "Hard strategy", Description: layerStrategyLabel(cfg.ContextCompression.Strategies.Hard, "hybrid")},
		{Value: "soft", Label: "Soft threshold (early maintenance)", Description: percentLabel(cfg.ContextCompression.Thresholds.SoftPercent, "off (default)")},
		{Value: "threshold", Label: "Trigger threshold", Description: trigger},
		{Value: "hard", Label: "Hard ceiling", Description: percentLabel(cfg.ContextCompression.Thresholds.HardPercent, "off (default)")},
		// Derived limits (CM:13 design rule 5: ALL compression-related
		// limits must be visible in /config — no hidden 95%). These are computed
		// from the hard ceiling and shown read-only so the user can see exactly
		// where each reactive/proactive gate fires.
		{Value: "_derived_eff_hard", Label: "  ↳ Effective hard ceiling (reactive)", Description: derivedPercentLabel(agentic.EffectiveHardPercent(hardPct))},
		{Value: "_derived_escalation", Label: "  ↳ Escalation level (cheap→selective)", Description: derivedPercentLabel(agentic.EscalationPercent(hardPct))},
		{Value: "_derived_deferral", Label: "  ↳ Deferral ceiling (cache-hot cutoff)", Description: derivedPercentLabel(agentic.DeferralCeilingPercent(hardPct))},
		{Value: "_derived_elision", Label: "  ↳ Elision target (hysteresis)", Description: derivedPercentLabel(agentic.ElisionTargetPercent(hardPct))},
		{Value: "_derived_reactive_savings", Label: "  ↳ Reactive savings target", Description: fmt.Sprintf("%d%% per pass (cut to %d%%)", agentic.ReactiveSavingsPercent, agentic.ReactiveTargetPercent(hardPct))},
		{Value: "cache_gate", Label: "Cache gate (defer compression for hot cache)", Description: cacheGateLabel(cfg.ContextCompression.CacheGate)},
		{Value: "max_tokens", Label: "Max tokens", Description: maxTokensLabel(cfg.ContextCompression.MaxTokens)},
		{Value: "preserve_recent_turns", Label: "Preserve recent turns (selective/hybrid)", Description: preserveRecentTurnsLabel(cfg)},
		{Value: "micro_enabled", Label: "Micro: pre-summarize step (opt-in)", Description: microEnabledLabel(cfg)},
		{Value: "micro_min_context_ratio", Label: "Micro: min context ratio (own gate)", Description: microMinContextRatioLabel(cfg)},
		{Value: "micro_cache_miss_threshold", Label: "Micro: cache-miss threshold (cold-cache gate)", Description: microCacheMissThresholdLabel(cfg)},
		{Value: "micro_keep_recent_messages", Label: "Micro: keep recent messages", Description: microKeepRecentLabel(cfg)},
		{Value: "micro_min_content_tokens", Label: "Micro: min content tokens", Description: microMinContentTokensLabel(cfg)},
		{Value: "micro_truncated_marker", Label: "Micro: truncation marker", Description: microTruncatedMarkerLabel(cfg)},
		{Value: "enabled", Label: "Enabled", Description: boolLabel(cfg.ContextCompression.EnabledValue())},
		{Value: "on_context_error", Label: "Compress on context error", Description: boolLabel(cfg.ContextCompression.OnContextError)},
	}
	openers := map[string]func(){
		"strategy":                   m.settingCompressionStrategy,
		"soft_strategy":              m.settingCompressionSoftStrategy,
		"hard_strategy":              m.settingCompressionHardStrategy,
		"soft":                       m.settingCompressionSoft,
		"threshold":                  m.settingCompressionThreshold,
		"hard":                       m.settingCompressionHard,
		"max_tokens":                 m.settingCompressionMaxTokens,
		"preserve_recent_turns":      m.settingCompressionPreserveRecentTurns,
		"micro_min_context_ratio":    m.settingCompressionMicroRatio,
		"micro_cache_miss_threshold": m.settingCompressionMicroCacheMissThreshold,
		"micro_keep_recent_messages": m.settingCompressionMicroKeepRecent,
		"micro_min_content_tokens":   m.settingCompressionMicroMinContent,
		"micro_truncated_marker":     m.settingCompressionMicroMarker,
	}
	m.ctx.SelectOption("Compression settings:", items, "", func(selected string, ok bool) {
		if !ok {
			m.back()
			return
		}
		if open, found := openers[selected]; found {
			m.open(open)
			return
		}
		switch selected {
		case "cache_gate":
			m.applySet("context_compression.cache_gate", toggleCacheGateValue(cfg.ContextCompression.CacheGate))
			m.settingCompression()
		case "enabled":
			m.applySet("context_compression.enabled", toggleBoolLabel(cfg.ContextCompression.EnabledValue()))
			m.settingCompression()
		case "on_context_error":
			m.applySet("context_compression.on_context_error", toggleBoolLabel(cfg.ContextCompression.OnContextError))
			m.settingCompression()
		case "micro_enabled":
			m.applySet("context_compression.micro_compaction.enabled", toggleMicroEnabledValue(cfg))
			m.settingCompression()
		}
	})
}

func (m *configMenu) settingCompressionStrategy() {
	m.current = m.settingCompressionStrategy
	current := m.ctx.Config.ContextCompression.Strategy
	if current == "" {
		current = "tool_elision"
	}
	items := []tui.SelectorItem{
		{Value: "micro", Label: "micro", Description: "truncate old tool result bodies (cache-friendly)"},
		{Value: "tool_elision", Label: "tool_elision", Description: "replace old tool args/results with placeholders"},
		{Value: "selective", Label: "selective", Description: "drop oldest messages, keep system + recent turns"},
		{Value: "hybrid", Label: "hybrid", Description: "tool_elision → selective → summarize"},
		{Value: "summarize", Label: "summarize", Description: "ask the LLM to summarize older turns"},
	}
	m.ctx.SelectOption("Compression strategy:", items, current, func(v string, ok bool) {
		if !ok {
			m.back()
			return
		}
		m.applySet("context_compression.strategy", v)
		m.back()
	})
}

func (m *configMenu) settingCompressionThreshold() {
	m.current = m.settingCompressionThreshold
	// 0 = off (default: no proactive trigger); the settable range in 5% steps.
	items := []tui.SelectorItem{
		{Value: "0", Label: "off (default)", Description: "no proactive trigger — compress only on context error"},
	}
	items = append(items, percentStepItems()...)
	current := fmt.Sprintf("%d", compressionTriggerValue(m.ctx.Config))
	m.ctx.SelectOption("Trigger threshold (% of max tokens):", items, current, func(v string, ok bool) {
		if !ok {
			m.back()
			return
		}
		m.applySet("context_compression.thresholds.trigger_percent", v)
		m.back()
	})
}

// layerStrategyLabel renders a per-layer strategy for the menu, showing the
// SDK default when unset.
func layerStrategyLabel(v, def string) string {
	if v == "" {
		return def + " (default)"
	}
	return v
}

// cacheGateLabel renders the cache-gate toggle state for the menu.
func cacheGateLabel(v string) string {
	if v == "off" {
		return "off"
	}
	return "on (default)"
}

// toggleCacheGateValue flips the cache gate: default/on → off, off → on.
func toggleCacheGateValue(v string) string {
	if v == "off" {
		return "on"
	}
	return "off"
}

func (m *configMenu) settingCompressionSoftStrategy() {
	m.current = m.settingCompressionSoftStrategy
	// The soft layer is zero-LLM only: no LLM call, no message drops.
	items := []tui.SelectorItem{
		{Value: "micro", Label: "micro (default)", Description: "truncate old tool result bodies (cache-friendly)"},
		{Value: "tool_elision", Label: "tool_elision", Description: "replace old tool args/results with placeholders"},
	}
	current := m.ctx.Config.ContextCompression.Strategies.Soft
	m.ctx.SelectOption("Soft-layer strategy (early maintenance, zero-LLM only):", items, current, func(v string, ok bool) {
		if !ok {
			m.back()
			return
		}
		m.applySet("context_compression.strategies.soft", v)
		m.back()
	})
}

func (m *configMenu) settingCompressionHardStrategy() {
	m.current = m.settingCompressionHardStrategy
	items := []tui.SelectorItem{
		{Value: "hybrid", Label: "hybrid (default)", Description: "tool_elision → selective → summarize"},
		{Value: "tool_elision", Label: "tool_elision", Description: "replace old tool args/results with placeholders"},
		{Value: "selective", Label: "selective", Description: "drop oldest messages, keep system + recent turns"},
		{Value: "summarize", Label: "summarize", Description: "ask the LLM to summarize older turns"},
		{Value: "micro", Label: "micro", Description: "truncate old tool result bodies (cache-friendly)"},
	}
	current := m.ctx.Config.ContextCompression.Strategies.Hard
	m.ctx.SelectOption("Hard-layer strategy (emergency, cache gate bypassed):", items, current, func(v string, ok bool) {
		if !ok {
			m.back()
			return
		}
		m.applySet("context_compression.strategies.hard", v)
		m.back()
	})
}

func (m *configMenu) settingCompressionSoft() {
	m.current = m.settingCompressionSoft
	// 0 = off (default: no early maintenance); levels in 5% steps 10-95.
	items := []tui.SelectorItem{
		{Value: "0", Label: "off (default)", Description: "no early maintenance"},
	}
	items = append(items, percentStepItems()...)
	current := fmt.Sprintf("%d", m.ctx.Config.ContextCompression.Thresholds.SoftPercent)
	m.ctx.SelectOption("Soft threshold — cheap zero-LLM maintenance when cache is cold:", items, current, func(v string, ok bool) {
		if !ok {
			m.back()
			return
		}
		m.applySet("context_compression.thresholds.soft_percent", v)
		m.back()
	})
}

func (m *configMenu) settingCompressionHard() {
	m.current = m.settingCompressionHard
	items := []tui.SelectorItem{
		{Value: "0", Label: "off (default)", Description: "no proactive ceiling — the reactive error/ceiling net stays on"},
	}
	items = append(items, percentStepItems()...)
	current := fmt.Sprintf("%d", compressionHardValue(m.ctx.Config))
	m.ctx.SelectOption("Hard ceiling (emergency: bypass cache, hard-layer strategy fires):", items, current, func(v string, ok bool) {
		if !ok {
			m.back()
			return
		}
		m.applySet("context_compression.thresholds.hard_percent", v)
		m.back()
	})
}

// percentStepItems builds the 10–95% selector items in 5% increments (the
// user-settable range for every compression level).
func percentStepItems() []tui.SelectorItem {
	items := make([]tui.SelectorItem, 0, 18)
	for pct := 10; pct <= 95; pct += 5 {
		items = append(items, tui.SelectorItem{
			Value: fmt.Sprintf("%d", pct),
			Label: fmt.Sprintf("%d%%", pct),
		})
	}
	return items
}

func (m *configMenu) settingCompressionMaxTokens() {
	m.current = m.settingCompressionMaxTokens
	items := []tui.SelectorItem{
		{Value: "0", Label: "auto", Description: "use the model's context window"},
		{Value: "8192", Label: "8,192", Description: "small models"},
		{Value: "16384", Label: "16,384", Description: ""},
		{Value: "32768", Label: "32,768", Description: ""},
		{Value: "65536", Label: "65,536", Description: ""},
		{Value: "131072", Label: "131,072", Description: "large models"},
	}
	current := fmt.Sprintf("%d", m.ctx.Config.ContextCompression.MaxTokens)
	m.ctx.SelectOption("Max tokens (compression limit; 0 = auto):", items, current, func(v string, ok bool) {
		if !ok {
			m.back()
			return
		}
		m.applySet("context_compression.max_tokens", v)
		m.back()
	})
}

// settingCompressionPreserveRecentTurns exposes preserve_recent_turns (used
// by the selective and hybrid strategies). 0 = SDK default (2 turns).
func (m *configMenu) settingCompressionPreserveRecentTurns() {
	m.current = m.settingCompressionPreserveRecentTurns
	items := []tui.SelectorItem{
		{Value: "0", Label: "default (2)", Description: "SDK fallback"},
	}
	for _, n := range []int{2, 4, 6, 8, 10, 20} {
		items = append(items, tui.SelectorItem{Value: fmt.Sprintf("%d", n), Label: fmt.Sprintf("%d", n)})
	}
	current := fmt.Sprintf("%d", m.ctx.Config.ContextCompression.PreserveRecentTurns)
	m.ctx.SelectOption("Turns preserved from compression (0 = default):", items, current, func(v string, ok bool) {
		if !ok {
			m.back()
			return
		}
		m.applySet("context_compression.preserve_recent_turns", v)
		m.back()
	})
}

// settingCompressionMicroRatio exposes the micro compaction's OWN usage gate
// (min_context_ratio): proactive micro compaction only runs at/above this
// fill level. This is the gate that fired in the 2026-08-02 cache-bust
// session — it must be visible (no hidden configuration keys).
func (m *configMenu) settingCompressionMicroRatio() {
	m.current = m.settingCompressionMicroRatio
	items := []tui.SelectorItem{
		{Value: "0", Label: "default (50%)", Description: "SDK default"},
	}
	for pct := 30; pct <= 90; pct += 10 {
		items = append(items, tui.SelectorItem{
			Value: fmt.Sprintf("%g", float64(pct)/100),
			Label: fmt.Sprintf("%d%%", pct),
		})
	}
	current := "0"
	if r := m.ctx.Config.ContextCompression.MicroCompaction.MinContextRatio; r > 0 {
		current = fmt.Sprintf("%g", r)
	}
	m.ctx.SelectOption("Micro compaction min context ratio (0 = default):", items, current, func(v string, ok bool) {
		if !ok {
			m.back()
			return
		}
		m.applySet("context_compression.micro_compaction.min_context_ratio", v)
		m.back()
	})
}

// settingCompressionMicroCacheMissThreshold exposes the micro cold-cache
// gate: in-place truncation is deferred while the provider cache is presumed
// hot, i.e. until the agent has been idle at least this long.
func (m *configMenu) settingCompressionMicroCacheMissThreshold() {
	m.current = m.settingCompressionMicroCacheMissThreshold
	items := []tui.SelectorItem{
		{Value: "1h", Label: "1h (default)", Description: "SDK default"},
		{Value: "5m", Label: "5m", Description: "aggressive: compact after short idles"},
		{Value: "15m", Label: "15m", Description: ""},
		{Value: "30m", Label: "30m", Description: ""},
		{Value: "2h", Label: "2h", Description: ""},
		{Value: "4h", Label: "4h", Description: "conservative: protect the cache longer"},
		{Value: "0", Label: "off", Description: "no cache protection: always compact at the ratio gate"},
	}
	current := m.ctx.Config.ContextCompression.MicroCompaction.CacheMissThreshold
	if current == "" {
		current = "1h"
	}
	m.ctx.SelectOption("Idle time before micro compaction may mutate a hot cache:", items, current, func(v string, ok bool) {
		if !ok {
			m.back()
			return
		}
		m.applySet("context_compression.micro_compaction.cache_miss_threshold", v)
		m.back()
	})
}

// settingCompressionMicroKeepRecent exposes keep_recent_messages: the N most
// recent messages micro compaction never truncates.
func (m *configMenu) settingCompressionMicroKeepRecent() {
	m.current = m.settingCompressionMicroKeepRecent
	items := []tui.SelectorItem{
		{Value: "0", Label: "default (20)", Description: "SDK default"},
	}
	for _, n := range []int{10, 20, 30, 50, 100} {
		items = append(items, tui.SelectorItem{Value: fmt.Sprintf("%d", n), Label: fmt.Sprintf("%d", n)})
	}
	current := fmt.Sprintf("%d", m.ctx.Config.ContextCompression.MicroCompaction.KeepRecentMessages)
	m.ctx.SelectOption("Recent messages micro compaction never touches (0 = default):", items, current, func(v string, ok bool) {
		if !ok {
			m.back()
			return
		}
		m.applySet("context_compression.micro_compaction.keep_recent_messages", v)
		m.back()
	})
}

// settingCompressionMicroMinContent exposes min_content_tokens: tool results
// smaller than this are left intact by micro compaction.
func (m *configMenu) settingCompressionMicroMinContent() {
	m.current = m.settingCompressionMicroMinContent
	items := []tui.SelectorItem{
		{Value: "0", Label: "default (100)", Description: "SDK default"},
	}
	for _, n := range []int{50, 100, 250, 500, 1000, 5000} {
		items = append(items, tui.SelectorItem{Value: fmt.Sprintf("%d", n), Label: fmt.Sprintf("%d", n)})
	}
	current := fmt.Sprintf("%d", m.ctx.Config.ContextCompression.MicroCompaction.MinContentTokens)
	m.ctx.SelectOption("Minimum tool-result size to truncate, in tokens (0 = default):", items, current, func(v string, ok bool) {
		if !ok {
			m.back()
			return
		}
		m.applySet("context_compression.micro_compaction.min_content_tokens", v)
		m.back()
	})
}

// settingCompressionMicroMarker exposes truncated_marker — the replacement
// text for cleared tool results. Free-form, so it uses the input line.
func (m *configMenu) settingCompressionMicroMarker() {
	m.current = m.settingCompressionMicroMarker
	current := m.ctx.Config.ContextCompression.MicroCompaction.TruncatedMarker
	m.ctx.ShowInput("Truncation marker (empty = default):", current, func(value string, ok bool) {
		if !ok {
			m.back()
			return
		}
		m.applySet("context_compression.micro_compaction.truncated_marker", value)
		m.back()
	})
}

// preserveRecentTurnsLabel renders preserve_recent_turns for the menu.
func preserveRecentTurnsLabel(cfg *config.Config) string {
	if v := cfg.ContextCompression.PreserveRecentTurns; v > 0 {
		return fmt.Sprintf("%d", v)
	}
	return "2 (default)"
}

// microMinContextRatioLabel renders the micro usage gate as a percentage.
func microMinContextRatioLabel(cfg *config.Config) string {
	if r := cfg.ContextCompression.MicroCompaction.MinContextRatio; r > 0 {
		return fmt.Sprintf("%.0f%%", r*100)
	}
	return "50% (default)"
}

// microCacheMissThresholdLabel renders the micro cold-cache gate.
func microCacheMissThresholdLabel(cfg *config.Config) string {
	switch v := cfg.ContextCompression.MicroCompaction.CacheMissThreshold; v {
	case "":
		return "1h (default)"
	case "0":
		return "off"
	default:
		return v
	}
}

// microKeepRecentLabel renders keep_recent_messages for the menu.
func microKeepRecentLabel(cfg *config.Config) string {
	if v := cfg.ContextCompression.MicroCompaction.KeepRecentMessages; v > 0 {
		return fmt.Sprintf("%d", v)
	}
	return "20 (default)"
}

// microMinContentTokensLabel renders min_content_tokens for the menu.
func microMinContentTokensLabel(cfg *config.Config) string {
	if v := cfg.ContextCompression.MicroCompaction.MinContentTokens; v > 0 {
		return fmt.Sprintf("%d", v)
	}
	return "100 (default)"
}

// microTruncatedMarkerLabel renders the truncation marker for the menu.
func microTruncatedMarkerLabel(cfg *config.Config) string {
	if v := cfg.ContextCompression.MicroCompaction.TruncatedMarker; v != "" {
		return v
	}
	return "[Old tool result content cleared] (default)"
}

// microEnabledLabel renders the micro pre-summarize opt-in for the menu.
// Micro compaction is DISABLED by default so summarize stays the default
// compaction path; nil means "not explicitly set" (still off).
func microEnabledLabel(cfg *config.Config) string {
	if e := cfg.ContextCompression.MicroCompaction.Enabled; e != nil && *e {
		return "on"
	}
	return "off (default)"
}

// toggleMicroEnabledValue flips the micro pre-summarize opt-in: on → off,
// off/unset → on.
func toggleMicroEnabledValue(cfg *config.Config) string {
	if e := cfg.ContextCompression.MicroCompaction.Enabled; e != nil && *e {
		return "false"
	}
	return "true"
}

// compressionLabel returns a one-line summary for the root /config menu.
// With proactive compression off (the default), it surfaces the reactive
// on-error net instead of a misleading "strategy @ 0%".
func compressionLabel(cfg *config.Config) string {
	if !cfg.ContextCompression.EnabledValue() {
		return "off"
	}
	if compressionTriggerValue(cfg) <= 0 {
		if cfg.ContextCompression.OnContextError {
			return "on-error (hybrid)"
		}
		return "off"
	}
	strategy := cfg.ContextCompression.Strategy
	if strategy == "" {
		strategy = "tool_elision"
	}
	return fmt.Sprintf("%s @ %d%%", strategy, compressionTriggerValue(cfg))
}

// compressionTriggerValue resolves the effective trigger percent for display:
// legacy alias wins, then the thresholds block.
func compressionTriggerValue(cfg *config.Config) int {
	if cfg.ContextCompression.ThresholdPercent > 0 {
		return cfg.ContextCompression.ThresholdPercent
	}
	return cfg.ContextCompression.Thresholds.TriggerPercent
}

// compressionTriggerDisplay renders the trigger value for the menu,
// annotating when it is unset (proactive trigger off — the default).
func compressionTriggerDisplay(cfg *config.Config) string {
	if v := compressionTriggerValue(cfg); v > 0 {
		return fmt.Sprintf("%d%%", v)
	}
	return "off (default)"
}

// compressionHardValue resolves the effective hard ceiling for display
// (0 = proactive ceiling off; the reactive net still protects the window).
func compressionHardValue(cfg *config.Config) int {
	return cfg.ContextCompression.Thresholds.HardPercent
}

// percentLabel renders an optional percent value with a fallback label.
func percentLabel(v int, fallback string) string {
	if v <= 0 {
		return fallback
	}
	return fmt.Sprintf("%d%%", v)
}

// derivedPercentLabel renders a computed (always-present) compression limit.
// Unlike percentLabel these values are never "off" — they are derived from the
// hard ceiling (CM:13 rule 5: no hidden limits).
func derivedPercentLabel(v int) string {
	return fmt.Sprintf("%d%%", v)
}

// maxTokensLabel renders the compression max_tokens value for display.
func maxTokensLabel(v int) string {
	if v <= 0 {
		return "auto"
	}
	return fmt.Sprintf("%d", v)
}
