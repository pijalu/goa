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
	// B56: sticky picker — Enter toggles in place, Escape closes ("", false).
	c.ctx.SelectOptionSticky("Bash settings:", []tui.SelectorItem{{Value: "warn_file_edits", Label: "Warn on shell file edits", Description: boolLabel(warnFileEditsOn(cfg))}}, "", func(s string, ok bool) {
		if !ok || s != "warn_file_edits" {
			c.back()
			return
		}
		c.toggleWarnFileEdits()
	})
}
func (c *configMenu) toggleWarnFileEdits() {
	v := "true"
	if warnFileEditsOn(c.ctx.Config) {
		v = "false"
	}
	c.applySet("tools.bash.warn_file_edits", v)
	// B56: stay in the SAME picker — refresh the flipped row in place so the
	// screen does not rebuild and the selection never jumps.
	c.ctx.UpdateSelectorItems([]tui.SelectorItem{{Value: "warn_file_edits", Label: "Warn on shell file edits", Description: boolLabel(warnFileEditsOn(c.ctx.Config))}})
}
func (c *configMenu) settingToolsMenu() {
	c.current = c.settingToolsMenu
	cfg := c.ctx.Config
	items := []tui.SelectorItem{
		// B56: navigation row — picking it opens another picker, so it must
		// close this sticky one (sticky pickers stay open by default).
		{Value: "enabled_tools", Label: "Enabled/disabled tools", Description: toolsEnabledLabel(cfg), CloseOnSelect: true},
		{Value: "tool_call_fixing", Label: "Tool call fixing", Description: boolLabel(cfg.Execution.AutoHealToolCalls)},
	}
	// B56: sticky picker — the toggle row flips in place, the navigation row
	// opens the tools picker, Escape closes.
	c.ctx.SelectOptionSticky("Tools settings:", items, "", func(s string, ok bool) {
		if !ok {
			c.back()
			return
		}
		switch s {
		case "enabled_tools":
			c.open(c.settingTools)
		case "tool_call_fixing":
			v := "true"
			if cfg.Execution.AutoHealToolCalls {
				v = "false"
			}
			c.applySet("execution.auto_heal_tool_calls", v)
			// B56: stay in the SAME picker — refresh the flipped row in place.
			c.ctx.UpdateSelectorItems([]tui.SelectorItem{
				{Value: "enabled_tools", Label: "Enabled/disabled tools", Description: toolsEnabledLabel(c.ctx.Config), CloseOnSelect: true},
				{Value: "tool_call_fixing", Label: "Tool call fixing", Description: boolLabel(c.ctx.Config.Execution.AutoHealToolCalls)},
			})
			return
		}
	})
}

func (c *configMenu) settingTools() {
	c.current = c.settingTools
	// B56: sticky picker — Enter flips the highlighted tool and the handler
	// refreshes the same overlay in place; Escape closes (onSelected "").
	c.ctx.SelectOptionSticky("Toggle optional tools:", buildToolItems(c.ctx.Config), "", c.toolToggleHandler)
}
func buildToolItems(c *config.Config) []tui.SelectorItem {
	names := tools.ConfigurableTools()
	out := make([]tui.SelectorItem, len(names))
	for i, t := range names {
		out[i] = tui.SelectorItem{Value: t.Name, Label: t.Name, Description: boolLabel(getToolEnabled(c, t.Name))}
	}
	return out
}

// toolToggleHandler is the /config → Tools entry point of the shared
// enable/disable primitive: it delegates to ApplyToolToggle so this menu and
// /tools:<name>:on|off can never disagree (bugs.md "Enabling a tool during a
// session does not enable it"). A tool that cannot be built at runtime is
// reported explicitly — never a silent no-op.
func (c *configMenu) toolToggleHandler(s string, ok bool) {
	if !ok || !isConfigurableTool(s) {
		c.back()
		return
	}
	next := !getToolEnabled(c.ctx.Config, s)
	outcome, err := ApplyToolToggle(c.ctx, s, next)
	switch {
	case err != nil:
		c.flash(ToolToggleErrorText(err))
	case outcome == ToolToggleRestartRequired:
		// Durable as well as transient: a toast alone is easy to miss, and the
		// user must know the change only lands after a restart.
		c.ctx.WriteSystem(ToolRestartMessage(s)+"\n", false)
		c.flash(ToolRestartMessage(s))
	case outcome == ToolToggleUnchanged:
		c.flash(fmt.Sprintf("Tool %s is already %s", s, onOffLabel(next)))
	default:
		c.flash(fmt.Sprintf("Tool %s %s", s, onOffLabel(next)))
	}
	// B56: stay in the SAME picker — refresh the flipped row in place so the
	// screen does not rebuild and the selection never jumps.
	c.ctx.UpdateSelectorItems(buildToolItems(c.ctx.Config))
}

// toggleNextLabel names the state a toggle lands on (used by the MCP row).
func toggleNextLabel(v bool) string {
	if v {
		return "off"
	}
	return "on"
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
