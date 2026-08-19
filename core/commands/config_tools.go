// SPDX-License-Identifier: GPL-3.0-or-later
package commands

import (
	"fmt"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/tools"
	"github.com/pijalu/goa/tui"
)

func (c *configMenu) settingBash() {
	c.current = c.settingBash
	cfg := c.ctx.Config
	c.ctx.SelectOption("Bash settings:", []tui.SelectorItem{{Value: "warn_file_edits", Label: "Warn on shell file edits", Description: boolLabel(warnFileEditsOn(cfg))}}, "", func(s string, ok bool) {
		if !ok {
			c.back()
			return
		}
		if s == "warn_file_edits" {
			c.toggleWarnFileEdits()
			return
		}
		c.back()
	})
}
func (c *configMenu) toggleWarnFileEdits() {
	v := "true"
	if warnFileEditsOn(c.ctx.Config) {
		v = "false"
	}
	c.applySet("tools.bash.warn_file_edits", v)
	c.settingBash()
}
func (c *configMenu) settingTools() {
	c.current = c.settingTools
	c.ctx.SelectOption("Toggle optional tools:", buildToolItems(c.ctx.Config), "", c.toolToggleHandler)
}
func buildToolItems(c *config.Config) []tui.SelectorItem {
	names := tools.ConfigurableTools()
	out := make([]tui.SelectorItem, len(names))
	for i, t := range names {
		out[i] = tui.SelectorItem{Value: t.Name, Label: t.Name, Description: boolLabel(getToolEnabled(c, t.Name))}
	}
	return out
}
func (c *configMenu) toolToggleHandler(s string, ok bool) {
	if !ok || !isConfigurableTool(s) {
		c.back()
		return
	}
	enabled := getToolEnabled(c.ctx.Config, s)
	c.applyToolToggle(s, enabled)
	setToolEnabled(c.ctx.Config, s, !enabled)
	c.saveToolToggle(s, !enabled)
	c.flash(fmt.Sprintf("Tool %s %s", s, toggleNextLabel(enabled)))
	c.settingTools()
}
func toggleNextLabel(v bool) string {
	if v {
		return "off"
	}
	return "on"
}
func (c *configMenu) applyToolToggle(name string, enabled bool) {
	if !enabled {
		return
	}
	if tr, ok := c.ctx.ToolRegistry.(*tools.ToolRegistry); ok {
		tr.Unregister(name)
	}
	if c.ctx.AgentManager != nil {
		_ = c.ctx.AgentManager.SetTools(c.ctx.ToolRegistry.All())
	}
}
func (c *configMenu) saveToolToggle(name string, enabled bool) {
	if c.ctx.ConfigSaver == nil {
		return
	}
	path, v := toolSaveKeyValue(name, enabled)
	if err := c.ctx.ConfigSaver.SaveProjectField(path, v); err != nil {
		c.flash("Failed to save config: " + err.Error())
	}
}
func toolsEnabledLabel(c *config.Config) string {
	on := 0
	for _, n := range tools.ConfigurableToolNames() {
		if getToolEnabled(c, n) {
			on++
		}
	}
	return fmt.Sprintf("%d/%d enabled", on, len(tools.ConfigurableToolNames()))
}
