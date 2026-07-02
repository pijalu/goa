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
	items := []tui.SelectorItem{
		{Value: "strategy", Label: "Strategy", Description: strategy},
		{Value: "threshold", Label: "Trigger threshold", Description: fmt.Sprintf("%d%%", cfg.ContextCompression.ThresholdPercent)},
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
		case "threshold":
			m.open(m.settingCompressionThreshold)
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
	current := fmt.Sprintf("%d", m.ctx.Config.ContextCompression.ThresholdPercent)
	m.ctx.SelectOption("Trigger threshold (% of max tokens):", items, current, func(v string, ok bool) {
		if !ok {
			m.back()
			return
		}
		m.applySet("context_compression.threshold_percent", v)
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
	return fmt.Sprintf("%s @ %d%%", strategy, cfg.ContextCompression.ThresholdPercent)
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
