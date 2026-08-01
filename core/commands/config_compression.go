// SPDX-License-Identifier: GPL-3.0-or-later

package commands

import (
	"fmt"

	"github.com/pijalu/goa/config"
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
	items := []tui.SelectorItem{
		{Value: "strategy", Label: "Trigger strategy", Description: strategy},
		{Value: "soft_strategy", Label: "Soft strategy", Description: layerStrategyLabel(cfg.ContextCompression.Strategies.Soft, "micro")},
		{Value: "hard_strategy", Label: "Hard strategy", Description: layerStrategyLabel(cfg.ContextCompression.Strategies.Hard, "hybrid")},
		{Value: "soft", Label: "Soft threshold (early maintenance)", Description: percentLabel(cfg.ContextCompression.Thresholds.SoftPercent, "80% (default)")},
		{Value: "threshold", Label: "Trigger threshold", Description: trigger},
		{Value: "hard", Label: "Hard ceiling", Description: percentLabel(cfg.ContextCompression.Thresholds.HardPercent, "95% (default)")},
		{Value: "cache_gate", Label: "Cache gate (defer compression for hot cache)", Description: cacheGateLabel(cfg.ContextCompression.CacheGate)},
		{Value: "max_tokens", Label: "Max tokens", Description: maxTokensLabel(cfg.ContextCompression.MaxTokens)},
		{Value: "enabled", Label: "Enabled", Description: boolLabel(cfg.ContextCompression.Enabled)},
		{Value: "on_context_error", Label: "Compress on context error", Description: boolLabel(cfg.ContextCompression.OnContextError)},
	}
	m.ctx.SelectOption("Compression settings:", items, "", func(selected string, ok bool) {
		if !ok {
			m.back()
			return
		}
		switch selected {
		case "strategy":
			m.open(m.settingCompressionStrategy)
		case "soft_strategy":
			m.open(m.settingCompressionSoftStrategy)
		case "hard_strategy":
			m.open(m.settingCompressionHardStrategy)
		case "cache_gate":
			m.applySet("context_compression.cache_gate", toggleCacheGateValue(cfg.ContextCompression.CacheGate))
			m.settingCompression()
		case "soft":
			m.open(m.settingCompressionSoft)
		case "threshold":
			m.open(m.settingCompressionThreshold)
		case "hard":
			m.open(m.settingCompressionHard)
		case "max_tokens":
			m.open(m.settingCompressionMaxTokens)
		case "enabled":
			m.applySet("context_compression.enabled", toggleBoolLabel(cfg.ContextCompression.Enabled))
			m.settingCompression()
		case "on_context_error":
			m.applySet("context_compression.on_context_error", toggleBoolLabel(cfg.ContextCompression.OnContextError))
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
	// 0 = SDK default (90%), then the settable range in 5% steps (10-95%).
	items := []tui.SelectorItem{
		{Value: "0", Label: "default (90%)", Description: "SDK default trigger"},
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
	// 0 = SDK default (80%), -1 disables the layer, levels in 5% steps 10-95.
	items := []tui.SelectorItem{
		{Value: "0", Label: "default (80%)", Description: "SDK default early maintenance"},
		{Value: "-1", Label: "off", Description: "no early maintenance"},
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
		{Value: "0", Label: "default (95%)", Description: "SDK default ceiling"},
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

// compressionLabel returns a one-line summary for the root /config menu.
func compressionLabel(cfg *config.Config) string {
	if !cfg.ContextCompression.Enabled {
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
// annotating when it comes from neither field (SDK default applies).
func compressionTriggerDisplay(cfg *config.Config) string {
	if v := compressionTriggerValue(cfg); v > 0 {
		return fmt.Sprintf("%d%%", v)
	}
	return "90% (default)"
}

// compressionHardValue resolves the effective hard ceiling for display.
func compressionHardValue(cfg *config.Config) int {
	if cfg.ContextCompression.Thresholds.HardPercent > 0 {
		return cfg.ContextCompression.Thresholds.HardPercent
	}
	return 95
}

// percentLabel renders an optional percent value with a fallback label.
func percentLabel(v int, fallback string) string {
	if v <= 0 {
		return fallback
	}
	return fmt.Sprintf("%d%%", v)
}

// maxTokensLabel renders the compression max_tokens value for display.
func maxTokensLabel(v int) string {
	if v <= 0 {
		return "auto"
	}
	return fmt.Sprintf("%d", v)
}

// toggleBoolLabel returns the string representation of the opposite bool,
// for toggle-style menu entries.
