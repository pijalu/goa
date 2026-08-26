// SPDX-License-Identifier: GPL-3.0-or-later
package commands

import (
	"strings"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/multiagent"
	"github.com/pijalu/goa/tui"
)

func (m *configMenu) applySet(k, v string) { _ = applyConfigSet(m.ctx, k, v) }
func (m *configMenu) saveConfig() {
	if m.ctx.ConfigSaver != nil {
		if e := m.ctx.ConfigSaver.Save(m.ctx.Config); e != nil {
			m.flash("Failed to save config: " + e.Error())
		}
	}
}
func (m *configMenu) saveProvidersAndModels() {
	if m.ctx.ConfigSaver != nil {
		if e := persistModelCatalogChange(m.ctx, m.ctx.Config, m.ctx.ConfigSaver); e != nil {
			m.flash("Failed to save config: " + e.Error())
		}
	}
}
func (m *configMenu) saveHomeSection(p []string, v any) {
	if m.ctx.ConfigSaver != nil {
		if e := m.ctx.ConfigSaver.SaveHomeFieldValue(p, v); e != nil {
			m.flash("Failed to save config: " + e.Error())
		}
	}
}
func (m *configMenu) flash(s string) { m.ctx.Flash(s) }
func configuredProviderItems(c *config.Config) []tui.SelectorItem {
	seen := map[string]bool{}
	var out []tui.SelectorItem
	for _, p := range c.Providers {
		if p.ID == "" || seen[p.ID] {
			continue
		}
		seen[p.ID] = true
		out = append(out, tui.SelectorItem{Value: p.ID, Label: p.ID, Description: p.Name})
	}
	if len(out) == 0 {
		out = append(out, tui.SelectorItem{Label: "(no providers configured)", Description: "use /config:add provider"})
	}
	return out
}
func modelManagerItems(c *config.Config) []tui.SelectorItem {
	out := []tui.SelectorItem{{Value: "__add__", Label: "Add model…", Description: "Configure a new model"}, {Value: "__set_active__", Label: "Set active model", Description: c.ActiveModel}}
	for _, m := range c.Models {
		out = append(out, tui.SelectorItem{Value: "__edit__" + m.ID, Label: "Edit " + m.ID, Description: m.Model}, tui.SelectorItem{Value: "__remove__" + m.ID, Label: "Remove " + m.ID})
	}
	return out
}
func multiAgentLabel(c *config.Config, o *multiagent.ForegroundOrchestrator) string {
	if c.MultiAgent.Enabled {
		return "on"
	}
	if o != nil && (o.Mode() == multiagent.WorkflowAgentDriven || o.Mode() == multiagent.WorkflowCompanionMinor) {
		return "on"
	}
	return "off"
}
func thinkingBlocksLabel(c *config.Config) string {
	if c.TUI.Transparency.ThinkingCollapsed {
		return "collapsed"
	}
	return "expanded"
}
func boolLabel(v bool) string {
	if v {
		return "on"
	}
	return "off"
}
func (m *configMenu) modeItems() []tui.SelectorItem {
	if m.ctx.ModeRegistry != nil {
		majors := m.ctx.ModeRegistry.Majors()
		out := make([]tui.SelectorItem, 0, len(majors))
		for _, major := range majors {
			desc := ""
			if s, e := m.ctx.ModeRegistry.Resolve(major); e == nil {
				desc = s.Description
			}
			out = append(out, tui.SelectorItem{Value: string(major), Label: string(major), Description: desc})
		}
		return out
	}
	return []tui.SelectorItem{{Value: "coder", Label: "coder", Description: "full coding mode"}, {Value: "planner", Label: "planner", Description: "architecture mode"}, {Value: "reviewer", Label: "reviewer", Description: "code review mode"}}
}
func modeItems() []tui.SelectorItem {
	return []tui.SelectorItem{{Value: "yolo", Label: "yolo", Description: "run tools without confirmation"}, {Value: "solo", Label: "solo", Description: "auto-run tools constrained to the codebase"}, {Value: "confirm", Label: "confirm", Description: "confirm each tool"}, {Value: "review", Label: "review", Description: "review after each turn"}}
}
func themeItems() []tui.SelectorItem {
	return []tui.SelectorItem{{Value: "dark", Label: "dark", Description: "dark theme"}, {Value: "light", Label: "light", Description: "light theme"}}
}
func deriveModelID(s string) string {
	base := s
	if i := strings.LastIndex(s, "/"); i >= 0 {
		base = s[i+1:]
	}
	var b strings.Builder
	for _, r := range strings.ToLower(base) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	if id := strings.Trim(b.String(), "-"); id != "" {
		return id
	}
	return "model"
}
