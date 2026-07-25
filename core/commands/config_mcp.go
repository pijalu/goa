// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"sort"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/tui"
)

// settingMCP is the /config → MCP servers sub-menu. It mirrors the Tools
// sub-menu: every installed MCP server is listed with its on/off state and
// selecting one toggles it (persisted, with live connect/disconnect).
func (m *configMenu) settingMCP() {
	m.current = m.settingMCP
	items := buildMCPItems(m.ctx.Config)
	if len(items) == 0 {
		m.flash("No MCP servers configured — install one with /mcp:add or `goa mcp add`")
		m.back()
		return
	}
	m.ctx.SelectOption("Toggle MCP servers:", items, "", m.mcpToggleHandler)
}

// buildMCPItems lists installed MCP servers with their enabled state, plus a
// trailing "add" hint entry.
func buildMCPItems(cfg *config.Config) []tui.SelectorItem {
	names := make([]string, 0, len(cfg.MCP))
	for name := range cfg.MCP {
		names = append(names, name)
	}
	sort.Strings(names)
	items := make([]tui.SelectorItem, 0, len(names))
	for _, name := range names {
		srv := cfg.MCP[name]
		target := srv.URL
		if srv.Type != config.MCPTypeRemote {
			target = "local"
		}
		items = append(items, tui.SelectorItem{
			Value:       name,
			Label:       name,
			Description: boolLabel(srv.IsEnabled()) + " · " + target,
		})
	}
	return items
}

// mcpToggleHandler flips the selected server's enabled state via the
// MCPCommand enable/disable path so persistence and live connect/disconnect
// behave exactly like /mcp:enable and /mcp:disable.
func (m *configMenu) mcpToggleHandler(selected string, ok bool) {
	if !ok {
		m.back()
		return
	}
	srv, exists := m.ctx.Config.MCP[selected]
	if !exists {
		m.back()
		return
	}
	sub := "enable"
	if srv.IsEnabled() {
		sub = "disable"
	}
	if err := (&MCPCommand{}).Run(m.ctx, []string{sub, selected}); err != nil {
		m.flash(fmt.Sprintf("MCP %s: %v", selected, err))
	} else {
		m.flash(fmt.Sprintf("MCP server %s %s", selected, toggleNextLabel(srv.IsEnabled())))
	}
	m.settingMCP()
}

// mcpServersLabel summarizes installed MCP servers for the /config root menu.
func mcpServersLabel(cfg *config.Config) string {
	if len(cfg.MCP) == 0 {
		return "none installed"
	}
	on := 0
	for _, srv := range cfg.MCP {
		if srv.IsEnabled() {
			on++
		}
	}
	return fmt.Sprintf("%d/%d enabled", on, len(cfg.MCP))
}
