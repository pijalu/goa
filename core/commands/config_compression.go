// SPDX-License-Identifier: GPL-3.0-or-later

package commands

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/tui"
)

func (m *configMenu) settingCompression() {
	m.current = m.settingCompression
	cfg := m.ctx.Config
	items := []tui.SelectorItem{
		// The master switch is a first-class row: a stale higher-layer
		// `enabled: false` once silently disabled compression with no visible
		// surface (bugs.md 2026-08-26). Toggling flips it inline.
		{Value: "enabled", Label: "Enabled", Description: boolLabel(cfg.ContextCompression.EnabledValue())},
		{Value: "soft_percent", Label: "Soft ceiling %", Description: ceilingPercentLabel(cfg.ContextCompression.Thresholds.SoftPercent)},
		{Value: "soft_method", Label: "Soft ceiling method", Description: layerMethodLabel(cfg.ContextCompression.Strategies.Soft, "micro")},
		{Value: "hard_percent", Label: "Hard ceiling %", Description: ceilingPercentLabel(cfg.ContextCompression.Thresholds.HardPercent)},
		{Value: "hard_method", Label: "Hard ceiling method", Description: layerMethodLabel(cfg.ContextCompression.Strategies.Hard, "summarize")},
		{Value: "on_error", Label: "On error", Description: onErrorLabel(cfg)},
		{Value: "advanced", Label: "Advanced…", Description: "trigger layer, cache gate, max tokens, micro, per-model"},
	}
	openers := map[string]func(){
		"soft_percent": func() { m.settingCompressionCeiling("soft") },
		"soft_method":  func() { m.settingCompressionMethod("soft") },
		"hard_percent": func() { m.settingCompressionCeiling("hard") },
		"hard_method":  func() { m.settingCompressionMethod("hard") },
		"on_error":     m.settingCompressionOnError,
		"advanced":     m.settingCompressionAdvanced,
	}
	m.ctx.SelectOption("Compression:", items, "", func(selected string, ok bool) {
		if !ok {
			m.back()
			return
		}
		// Zero dead rows: every selectable item must open a picker (the
		// regression behind the "returns to main menu" bug report).
		if open, found := openers[selected]; found {
			m.open(open)
			return
		}
		if selected == "enabled" {
			m.applySet("context_compression.enabled", toggleBoolLabel(cfg.ContextCompression.EnabledValue()))
			m.settingCompression()
		}
	})
}

// settingCompressionCeiling is the percent picker for one proactive layer
// (soft/hard): 0 = disabled, then 5..100 in 5% steps.
func (m *configMenu) settingCompressionCeiling(layer string) {
	m.current = func() { m.settingCompressionCeiling(layer) }
	var current int
	switch layer {
	case "soft":
		current = m.ctx.Config.ContextCompression.Thresholds.SoftPercent
	default:
		current = m.ctx.Config.ContextCompression.Thresholds.HardPercent
	}
	title := "Soft ceiling (% of max tokens, 0 = disabled):"
	if layer != "soft" {
		title = "Hard ceiling (% of max tokens, 0 = disabled):"
	}
	m.ctx.SelectOption(title, ceilingPercentItems(false), fmt.Sprintf("%d", current), func(v string, ok bool) {
		if !ok {
			m.back()
			return
		}
		m.applySet("context_compression.thresholds."+layer+"_percent", v)
		m.back()
	})
}

// settingCompressionMethod is the strategy picker for one layer; every
// method is offered on every layer (all-methods soft included).
func (m *configMenu) settingCompressionMethod(layer string) {
	m.current = func() { m.settingCompressionMethod(layer) }
	var current string
	if layer == "soft" {
		current = m.ctx.Config.ContextCompression.Strategies.Soft
	} else {
		current = m.ctx.Config.ContextCompression.Strategies.Hard
	}
	title := "Soft ceiling method:"
	if layer != "soft" {
		title = "Hard ceiling method:"
	}
	m.ctx.SelectOption(title, compressionStrategyItems(false), current, func(v string, ok bool) {
		if !ok {
			m.back()
			return
		}
		m.applySet("context_compression.strategies."+layer, v)
		m.back()
	})
}

// settingCompressionOnError is the single on-context-error row: "off" turns
// the reactive recovery net off; any method turns it on and selects the
// recovery strategy (empty = hybrid).
func (m *configMenu) settingCompressionOnError() {
	m.current = m.settingCompressionOnError
	cfg := m.ctx.Config
	items := append([]tui.SelectorItem{{
		Value:       "off",
		Label:       "off",
		Description: "no compression on context error",
	}}, compressionStrategyItems(false)...)
	current := "off"
	if cfg.ContextCompression.OnContextError {
		current = cfg.ContextCompression.OnErrorStrategy
		if current == "" {
			current = "hybrid"
		}
	}
	m.ctx.SelectOption("On context error:", items, current, func(v string, ok bool) {
		if !ok {
			m.back()
			return
		}
		if v == "off" {
			m.applySet("context_compression.on_context_error", "false")
		} else {
			m.applySet("context_compression.on_context_error", "true")
			m.applySet("context_compression.on_error_strategy", v)
		}
		m.back()
	})
}

// settingCompressionAdvanced hosts the knobs that do not fit the 5 main
// rows: trigger-layer settings, cache gate, max tokens, preserve window,
// micro compaction, per-model overrides and the master enable. Every row is
// actionable (opener or inline toggle) — no derived/read-only rows.
func (m *configMenu) settingCompressionAdvanced() {
	m.current = m.settingCompressionAdvanced
	cfg := m.ctx.Config
	items := []tui.SelectorItem{
		{Value: "enabled", Label: "Enabled", Description: boolLabel(cfg.ContextCompression.EnabledValue())},
		{Value: "strategy", Label: "Trigger strategy", Description: layerMethodLabel(cfg.ContextCompression.Strategy, "tool_elision")},
		{Value: "threshold", Label: "Trigger threshold", Description: compressionTriggerDisplay(cfg)},
		{Value: "cache_gate", Label: "Cache gate (defer compression for hot cache)", Description: cacheGateLabel(cfg.ContextCompression.CacheGate)},
		{Value: "max_tokens", Label: "Max tokens", Description: maxTokensLabel(cfg.ContextCompression.MaxTokens)},
		{Value: "preserve_recent_turns", Label: "Preserve recent turns (selective/hybrid)", Description: preserveRecentTurnsLabel(cfg)},
		{Value: "micro_enabled", Label: "Micro: pre-summarize step (opt-in)", Description: microEnabledLabel(cfg)},
		{Value: "micro_min_context_ratio", Label: "Micro: min context ratio (own gate)", Description: microMinContextRatioLabel(cfg)},
		{Value: "micro_cache_miss_threshold", Label: "Micro: cache-miss threshold (cold-cache gate)", Description: microCacheMissThresholdLabel(cfg)},
		{Value: "micro_keep_recent_messages", Label: "Micro: keep recent messages", Description: microKeepRecentLabel(cfg)},
		{Value: "micro_min_content_tokens", Label: "Micro: min content tokens", Description: microMinContentTokensLabel(cfg)},
		{Value: "micro_truncated_marker", Label: "Micro: truncation marker", Description: microTruncatedMarkerLabel(cfg)},
		{Value: "per_model", Label: "Per-model overrides", Description: perModelCompressionLabel(cfg)},
	}
	openers := map[string]func(){
		"per_model":                  m.settingCompressionPerModel,
		"strategy":                   m.settingCompressionStrategy,
		"threshold":                  m.settingCompressionThreshold,
		"max_tokens":                 m.settingCompressionMaxTokens,
		"preserve_recent_turns":      m.settingCompressionPreserveRecentTurns,
		"micro_min_context_ratio":    m.settingCompressionMicroRatio,
		"micro_cache_miss_threshold": m.settingCompressionMicroCacheMissThreshold,
		"micro_keep_recent_messages": m.settingCompressionMicroKeepRecent,
		"micro_min_content_tokens":   m.settingCompressionMicroMinContent,
		"micro_truncated_marker":     m.settingCompressionMicroMarker,
	}
	m.ctx.SelectOption("Compression — advanced:", items, "", func(selected string, ok bool) {
		if !ok {
			m.back()
			return
		}
		if open, found := openers[selected]; found {
			m.open(open)
			return
		}
		switch selected {
		case "enabled":
			m.applySet("context_compression.enabled", toggleBoolLabel(cfg.ContextCompression.EnabledValue()))
		case "cache_gate":
			m.applySet("context_compression.cache_gate", toggleCacheGateValue(cfg.ContextCompression.CacheGate))
		case "micro_enabled":
			m.applySet("context_compression.micro_compaction.enabled", toggleMicroEnabledValue(cfg))
		default:
			return // unknown row: never close the menu silently
		}
		m.settingCompressionAdvanced()
	})
}

// ceilingPercentLabel renders a layer ceiling: N% or the explicit disabled
// spelling (0 = disabled under the opt-in semantics).
func ceilingPercentLabel(v int) string {
	if v <= 0 {
		return "0% (disabled)"
	}
	return fmt.Sprintf("%d%%", v)
}

// layerMethodLabel renders a layer method with its default when unset.
func layerMethodLabel(v, def string) string {
	if v == "" {
		return def + " (default)"
	}
	return v
}

// onErrorLabel renders the On error row: the recovery method, or off.
func onErrorLabel(cfg *config.Config) string {
	if !cfg.ContextCompression.OnContextError {
		return "off"
	}
	if s := cfg.ContextCompression.OnErrorStrategy; s != "" {
		return s
	}
	return "hybrid"
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
		{Value: "fresh_window", Label: "fresh_window", Description: "reset the window, keep system + recent turns (no LLM call)"},
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
	current := fmt.Sprintf("%d", compressionTriggerValue(m.ctx.Config))
	m.ctx.SelectOption("Trigger threshold (% of max tokens):", ceilingPercentItems(false), current, func(v string, ok bool) {
		if !ok {
			m.back()
			return
		}
		m.applySet("context_compression.thresholds.trigger_percent", v)
		m.back()
	})
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

// ceilingPercentItems builds the ceiling picker: 0 (disabled) plus 5..100
// in 5% steps. withInherit prepends the per-model "inherit (clear)" row.
// Every row sets PreserveOrder: the ladder is inherently ordered, and the
// selector's default alphabetical Label sort interleaves the single-digit
// entry after the two-digit ones (45%, 5%, 50%) and 100% after 10% — the
// picker must read in ascending numeric order (bugs.md). Values stay
// unpadded: they are the persisted config keys and the ✓ marker matches on
// them.
func ceilingPercentItems(withInherit bool) []tui.SelectorItem {
	items := make([]tui.SelectorItem, 0, 21)
	if withInherit {
		items = append(items, tui.SelectorItem{Value: "", Label: "inherit (clear)", Description: "use the global threshold", PreserveOrder: true})
	}
	items = append(items, tui.SelectorItem{Value: "0", Label: "0% (disabled)", Description: "layer off", PreserveOrder: true})
	for pct := 5; pct <= 100; pct += 5 {
		items = append(items, tui.SelectorItem{
			Value:         fmt.Sprintf("%d", pct),
			Label:         fmt.Sprintf("%d%%", pct),
			PreserveOrder: true,
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

// compressionLabel returns a compact summary for the root /config menu row:
// a COUNT of the enabled compression mechanisms (soft / trigger / hard /
// on-error net / micro compaction). The rich per-layer preview was dropped —
// too wide for the row — and the old preview-empty fallback "off" was wrong:
// micro compaction alone left the label "off" while compression ran. The
// master switch off is the only state spelled "disabled"; mechanisms on but
// none configured reads "none active".
func compressionLabel(cfg *config.Config) string {
	cc := cfg.ContextCompression
	if !cc.EnabledValue() {
		return "disabled"
	}
	active := 0
	if cc.Thresholds.SoftPercent > 0 {
		active++
	}
	if compressionTriggerValue(cfg) > 0 {
		active++
	}
	if cc.Thresholds.HardPercent > 0 {
		active++
	}
	if cc.OnContextError {
		active++
	}
	if e := cc.MicroCompaction.Enabled; e != nil && *e {
		active++
	}
	if active == 0 {
		return "none active"
	}
	return fmt.Sprintf("%d active", active)
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

// maxTokensLabel renders the compression max_tokens value for display.
func maxTokensLabel(v int) string {
	if v <= 0 {
		return "auto"
	}
	return fmt.Sprintf("%d", v)
}

// --- per-model overrides ------------------------------------------------------

// perModelCompressionLabel summarizes how many models carry a compression
// override for the top-level menu row.
func perModelCompressionLabel(cfg *config.Config) string {
	n := len(cfg.ContextCompression.PerModel)
	if n == 0 {
		return "none (global only)"
	}
	return fmt.Sprintf("%d model(s) overridden", n)
}

// settingCompressionPerModel lists configured models and opens the override
// editor for the chosen one. Only configured models are offered because
// validation (validateCompressionOverride) rejects an override for an unknown
// model ID.
func (m *configMenu) settingCompressionPerModel() {
	m.current = m.settingCompressionPerModel
	cfg := m.ctx.Config
	items := make([]tui.SelectorItem, 0, len(cfg.Models))
	for _, mod := range cfg.Models {
		if mod.Ephemeral {
			continue
		}
		_, has := cfg.ContextCompression.PerModel[mod.ID]
		desc := "inherits global"
		if has {
			desc = "override set"
		}
		items = append(items, tui.SelectorItem{
			Value:       mod.ID,
			Label:       mod.ID,
			Description: desc,
			Color:       localModelColor(cfg, mod.ProviderID),
			SearchLabel: modelSearchLabel(mod.ID, mod.ProviderID, mod.Model),
		})
	}
	if len(items) == 0 {
		items = append(items, tui.SelectorItem{
			Value:       "",
			Label:       "(no models configured)",
			Description: "add a model under /config models first",
		})
	}
	m.ctx.SelectOption("Per-model compression overrides — pick a model:", items, "", func(modelID string, ok bool) {
		if !ok || modelID == "" {
			m.back()
			return
		}
		m.open(func() { m.settingCompressionPerModelEdit(modelID) })
	})
}

// settingCompressionPerModelEdit is the per-model override editor: it shows the
// effective override value (or "inherit") for each settable field and lets the
// user set or clear it. All edits route through applySet with the dynamic
// context_compression.per_model.<id>.<field> key, so validation, persistence
// and live runtime refresh are identical to the global settings.
func (m *configMenu) settingCompressionPerModelEdit(modelID string) {
	m.current = func() { m.settingCompressionPerModelEdit(modelID) }
	cfg := m.ctx.Config
	ov := cfg.ContextCompression.PerModel[modelID]
	keyPrefix := "context_compression.per_model." + modelID + "."
	items := []tui.SelectorItem{
		{Value: "enabled", Label: "Enabled", Description: perModelEnabledLabel(ov)},
		{Value: "strategy", Label: "Trigger strategy", Description: perModelStrategyLabel(ov.Strategy)},
		{Value: "strategies.soft", Label: "Soft strategy", Description: perModelStrategyLabel(ov.Strategies.Soft)},
		{Value: "strategies.hard", Label: "Hard strategy", Description: perModelStrategyLabel(ov.Strategies.Hard)},
		{Value: "thresholds.soft_percent", Label: "Soft threshold", Description: perModelPctLabel(ov.Thresholds.SoftPercent)},
		{Value: "thresholds.trigger_percent", Label: "Trigger threshold", Description: perModelPctLabel(ov.Thresholds.TriggerPercent)},
		{Value: "thresholds.hard_percent", Label: "Hard ceiling", Description: perModelPctLabel(ov.Thresholds.HardPercent)},
		{Value: "max_tokens", Label: "Max tokens", Description: perModelMaxTokensLabel(ov.MaxTokens)},
		{Value: "cache_gate", Label: "Cache gate", Description: perModelCacheGateLabel(ov.CacheGate)},
		{Value: "preserve_recent_turns", Label: "Preserve recent turns", Description: perModelIntInheritLabel(ov.PreserveRecentTurns)},
		{Value: "__clear__", Label: "— clear all overrides —", Description: "remove every per-model override for " + modelID},
	}
	m.ctx.SelectOption("Compression overrides for "+modelID+":", items, "", func(field string, ok bool) {
		if !ok || field == "" {
			m.back()
			return
		}
		if field == "__clear__" {
			m.clearPerModelCompression(modelID)
			return
		}
		// Push the field selector so its apply/cancel (m.back) returns HERE, to
		// the editor — matching how the global compression menu's openers are
		// pushed via m.open and their sub-screens pop back to settingCompression.
		m.open(func() { m.openPerModelCompressionField(modelID, keyPrefix, field) })
	})
}

// openPerModelCompressionField opens the value selector for one override field.
// An "inherit (clear)" option maps to the empty value, which applySet turns
// into a field removal (the model then inherits the global section).
func (m *configMenu) openPerModelCompressionField(modelID, keyPrefix, field string) {
	m.current = func() { m.openPerModelCompressionField(modelID, keyPrefix, field) }
	key := keyPrefix + field
	switch field {
	case "enabled":
		m.selectPerModelValue("Compression for "+modelID+":", key, perModelEnabledItems())
	case "strategy":
		m.selectPerModelValue("Strategy for "+modelID+":", key, compressionStrategyItems(true))
	case "strategies.soft":
		m.selectPerModelValue("Soft (zero-LLM) strategy for "+modelID+":", key, layerStrategyItems(true, true))
	case "strategies.hard":
		m.selectPerModelValue("Hard strategy for "+modelID+":", key, layerStrategyItems(true, false))
	case "thresholds.soft_percent", "thresholds.trigger_percent", "thresholds.hard_percent":
		m.selectPerModelValue(field+" for "+modelID+":", key, percentItemsWithInherit())
	case "cache_gate":
		m.selectPerModelValue("Cache gate for "+modelID+":", key, cacheGateItems())
	case "max_tokens":
		m.promptPerModelInput("Max tokens for "+modelID+" (empty = inherit):", key)
	case "preserve_recent_turns":
		m.promptPerModelInput("Preserve recent turns for "+modelID+" (empty = inherit):", key)
	}
}

// selectPerModelValue shows a value picker and applies the choice via applySet.
func (m *configMenu) selectPerModelValue(title, key string, items []tui.SelectorItem) {
	m.ctx.SelectOption(title, items, "", func(v string, ok bool) {
		if !ok {
			m.back()
			return
		}
		m.applySet(key, v)
		m.back()
	})
}

// promptPerModelInput shows a free-text input for numeric override fields.
func (m *configMenu) promptPerModelInput(prompt, key string) {
	m.ctx.ShowInput(prompt, "", func(value string, ok bool) {
		if !ok {
			m.back()
			return
		}
		m.applySet(key, strings.TrimSpace(value))
		m.back()
	})
}

// clearPerModelCompression removes every override field for a model, both from
// the live config and from the persisted home config, then refreshes the agent.
func (m *configMenu) clearPerModelCompression(modelID string) {
	cc := &m.ctx.Config.ContextCompression
	if cc.PerModel == nil {
		m.back()
		return
	}
	delete(cc.PerModel, modelID)
	if m.ctx.ConfigSaver != nil {
		if err := m.ctx.ConfigSaver.DeleteHomeField([]string{"context_compression", "per_model", modelID}); err != nil {
			m.flash("cleared in memory, but failed to persist: " + err.Error())
		}
	}
	if m.ctx.AgentManager != nil {
		m.ctx.AgentManager.RefreshContextCompression()
	}
	m.back()
}

// --- per-model label / item helpers ------------------------------------------

func perModelStrategyLabel(v string) string {
	if v == "" {
		return "inherit"
	}
	return v
}

func perModelPctLabel(v int) string {
	if v == 0 {
		return "inherit"
	}
	if v == -1 {
		return "disabled"
	}
	return fmt.Sprintf("%d%%", v)
}

func perModelMaxTokensLabel(v int) string {
	if v <= 0 {
		return "inherit"
	}
	return fmt.Sprintf("%d", v)
}

func perModelCacheGateLabel(v string) string {
	if v == "" {
		return "inherit"
	}
	return v
}

// perModelEnabledLabel renders the per-model enable tri-state: unset means
// inherit (the global flag plus the implicit-activation rule), an explicit
// on/off force-enables/disables compression for this model (bugs.md
// 2026-08-26).
func perModelEnabledLabel(ov config.ModelCompressionOverride) string {
	if ov.Enabled == nil {
		return "inherit"
	}
	if *ov.Enabled {
		return "on (forced)"
	}
	return "off (forced)"
}

// perModelEnabledItems is the per-model enable picker: inherit (clear),
// force on, force off.
func perModelEnabledItems() []tui.SelectorItem {
	return []tui.SelectorItem{
		{Value: "", Label: "inherit (clear)", Description: "follow the global flag; a stated ceiling still opts in"},
		{Value: "true", Label: "on", Description: "compression on for this model even if globally disabled"},
		{Value: "false", Label: "off", Description: "compression off for this model even if globally enabled"},
	}
}

func perModelIntInheritLabel(v int) string {
	if v <= 0 {
		return "inherit"
	}
	return fmt.Sprintf("%d", v)
}

// compressionStrategyItems returns the strategy picker options; withInherit
// prepends the "inherit (clear)" row used by the per-model editor.
func compressionStrategyItems(withInherit bool) []tui.SelectorItem {
	items := []tui.SelectorItem{}
	if withInherit {
		items = append(items, tui.SelectorItem{Value: "", Label: "inherit (clear)", Description: "use the global compression strategy"})
	}
	return append(items,
		tui.SelectorItem{Value: "micro", Label: "micro", Description: "truncate old tool result bodies (cache-friendly)"},
		tui.SelectorItem{Value: "tool_elision", Label: "tool_elision", Description: "replace old tool args/results with placeholders"},
		tui.SelectorItem{Value: "selective", Label: "selective", Description: "drop oldest messages, keep system + recent turns"},
		tui.SelectorItem{Value: "hybrid", Label: "hybrid", Description: "tool_elision → selective → summarize"},
		tui.SelectorItem{Value: "summarize", Label: "summarize", Description: "ask the LLM to summarize older turns"},
		tui.SelectorItem{Value: "fresh_window", Label: "fresh_window", Description: "reset the window, keep system + recent turns (no LLM call)"},
	)
}

// layerStrategyItems returns per-layer strategy options: every method is
// offered on every layer (the all-methods soft rework); soft just tunes the
// title flavor in the per-model editor.
func layerStrategyItems(withInherit, soft bool) []tui.SelectorItem {
	items := []tui.SelectorItem{}
	if withInherit {
		items = append(items, tui.SelectorItem{Value: "", Label: "inherit (clear)", Description: "use the global layer strategy"})
	}
	_ = soft // all methods on all layers; kept for call-site stability
	return append(items,
		tui.SelectorItem{Value: "micro", Label: "micro", Description: "truncate old tool result bodies"},
		tui.SelectorItem{Value: "tool_elision", Label: "tool_elision", Description: "replace old tool args/results with placeholders"},
		tui.SelectorItem{Value: "selective", Label: "selective", Description: "drop oldest messages"},
		tui.SelectorItem{Value: "hybrid", Label: "hybrid", Description: "tool_elision → selective → summarize"},
		tui.SelectorItem{Value: "summarize", Label: "summarize", Description: "ask the LLM to summarize older turns"},
		tui.SelectorItem{Value: "fresh_window", Label: "fresh_window", Description: "reset the window, keep system + recent turns (no LLM call)"},
	)
}

// percentItemsWithInherit returns the ceiling options plus inherit (clear).
func percentItemsWithInherit() []tui.SelectorItem {
	return ceilingPercentItems(true)
}

// cacheGateItems returns the cache-gate options plus inherit (clear).
func cacheGateItems() []tui.SelectorItem {
	return []tui.SelectorItem{
		{Value: "", Label: "inherit (clear)", Description: "use the global cache-gate setting"},
		{Value: "on", Label: "on", Description: "defer compression while the cache is hot"},
		{Value: "off", Label: "off", Description: "compress at the threshold regardless of cache"},
	}
}
