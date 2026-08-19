// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

type ToolDisplayConfig struct {
	// View is the default mode: "summary" (collapsed, N-line preview) or
	// "full" (expanded). Defaults to "summary". Ctrl+O toggles all tool
	// blocks between the two modes for the running session.
	View ToolView `yaml:"view"`
	// PreviewLines is the number of input/output lines shown per tool block
	// in Summary mode. Defaults to 10. It is the single source of truth for
	// the collapsed line count across ALL tools (replaces the previous
	// inconsistent per-tool hardcodes).
	PreviewLines int `yaml:"preview_lines"`
	// ShowRead controls whether the read tool's file content is displayed in
	// the TUI. When false (default), read output is hidden unless the user
	// toggles it. Set to true to show read output by default.
	ShowRead bool `yaml:"show_read"`
}

// TransparencyConfig controls which LLM transparency features are visible.
type TransparencyConfig struct {
	ShowThinking         bool   `yaml:"show_thinking"`
	ShowStreaming        bool   `yaml:"show_streaming"`
	ShowToolCalls        bool   `yaml:"show_tool_calls"`
	ShowTokenStats       bool   `yaml:"show_token_stats"`
	ShowLogs             bool   `yaml:"show_logs"`
	ThinkingPanePosition string `yaml:"thinking_pane_position"`
	HighlightToolInput   bool   `yaml:"highlight_tool_input"`
	ThinkingCollapsed    bool   `yaml:"thinking_collapsed"`
}

// PluginsConfig controls the JS plugin system.
type PluginsConfig struct {
	Dirs    []string `yaml:"dirs"`
	Enabled []string `yaml:"enabled"`
	// Bundled toggles built-in (embedded) plugins by id. A plugin loads when
	// its entry is absent or true; setting it to false opts out. Defaults to
	// enabled so built-ins like provider-quota work out of the box.
	Bundled map[string]bool `yaml:"bundled,omitempty"`
}

// BundledEnabled reports whether a bundled plugin loads. Absent entry = true.
func (p PluginsConfig) BundledEnabled(id string) bool {
	if p.Bundled == nil {
		return true
	}
	enabled, ok := p.Bundled[id]
	return !ok || enabled
}

// ThinkingLevelConfig controls per-role reasoning effort settings.
