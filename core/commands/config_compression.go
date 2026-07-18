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
		{Value: "strategy", Label: "Strategy", Description: strategy},
		{Value: "soft", Label: "Soft threshold (early maintenance)", Description: percentLabel(cfg.ContextCompression.Thresholds.SoftPercent, "off")},
		{Value: "threshold", Label: "Trigger threshold", Description: trigger},
		{Value: "hard", Label: "Hard ceiling", Description: percentLabel(cfg.ContextCompression.Thresholds.HardPercent, "95% (default)")},
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
	items := []tui.SelectorItem{
		{Value: "50", Label: "50%", Description: "early"},
		{Value: "75", Label: "75%", Description: "balanced"},
		{Value: "80", Label: "80%", Description: "default"},
		{Value: "90", Label: "90%", Description: "late"},
		{Value: "100", Label: "100%", Description: "only at the limit"},
	}
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

func (m *configMenu) settingCompressionSoft() {
	m.current = m.settingCompressionSoft
	items := []tui.SelectorItem{
		{Value: "0", Label: "off", Description: "no early maintenance"},
		{Value: "40", Label: "40%", Description: "very early"},
		{Value: "50", Label: "50%", Description: "early"},
		{Value: "60", Label: "60%", Description: "moderate"},
		{Value: "70", Label: "70%", Description: "late"},
	}
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
		{Value: "85", Label: "85%", Description: "conservative"},
		{Value: "90", Label: "90%", Description: "early ceiling"},
		{Value: "95", Label: "95%", Description: "default"},
		{Value: "100", Label: "100%", Description: "only at the hard limit"},
	}
	current := fmt.Sprintf("%d", compressionHardValue(m.ctx.Config))
	m.ctx.SelectOption("Hard ceiling (emergency: bypass cache, escalate, refuse new turns):", items, current, func(v string, ok bool) {
		if !ok {
			m.back()
			return
		}
		m.applySet("context_compression.thresholds.hard_percent", v)
		m.back()
	})
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
