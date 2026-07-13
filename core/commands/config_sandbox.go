// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/sandbox"
	"github.com/pijalu/goa/tools"
	"github.com/pijalu/goa/tui"
)

// sandboxLabel returns a short summary of sandbox settings.
func sandboxLabel(cfg *config.Config) string {
	parts := []string{}
	if cfg.Tools.Bash.EnableComplexityAnalysis {
		parts = append(parts, "bash:complexity")
	}
	if cfg.Tools.Bash.Jail {
		parts = append(parts, "bash:jail")
	}
	if cfg.Tools.Terminal.Sandbox.Enabled {
		parts = append(parts, "terminal")
	}
	if len(parts) == 0 {
		return "off"
	}
	return strings.Join(parts, ", ")
}

// openSandbox is the /config → Sandbox sub-menu.
func (m *configMenu) openSandbox() { m.open(m.settingSandbox) }

func (m *configMenu) settingSandbox() {
	m.current = m.settingSandbox
	cfg := m.ctx.Config
	items := []tui.SelectorItem{
		{Value: "bash_complexity", Label: "Bash complexity analysis", Description: boolLabel(cfg.Tools.Bash.EnableComplexityAnalysis)},
		{Value: "bash_jail", Label: "Bash jail", Description: boolLabel(cfg.Tools.Bash.Jail)},
		{Value: "bash_max_score", Label: "Bash max complexity score", Description: intLabel(cfg.Tools.Bash.MaxComplexityScore)},
		{Value: "terminal_sandbox", Label: "Terminal sandbox", Description: boolLabel(cfg.Tools.Terminal.Sandbox.Enabled)},
		{Value: "bash_blocked", Label: "Bash blocked commands", Description: fmt.Sprintf("%d items", len(cfg.Tools.Bash.BlockedCommands))},
		{Value: "bash_allowed", Label: "Bash allowed commands", Description: fmt.Sprintf("%d items", len(cfg.Tools.Bash.AllowedCommands))},
	}
	m.ctx.SelectOption("Sandbox settings:", items, "", func(selected string, ok bool) {
		if !ok {
			m.back()
			return
		}
		m.handleSandboxSetting(selected)
	})
}

func (m *configMenu) handleSandboxSetting(selected string) {
	cfg := m.ctx.Config
	switch selected {
	case "bash_complexity":
		newVal := !cfg.Tools.Bash.EnableComplexityAnalysis
		m.applySet("tools.bash.enable_complexity_analysis", boolToString(newVal))
		applyBashComplexityToggle(m.ctx, newVal)
		m.flash(fmt.Sprintf("Bash complexity analysis %s", toggleNextLabel(!newVal)))
		m.settingSandbox()
	case "bash_jail":
		newVal := !cfg.Tools.Bash.Jail
		m.applySet("tools.bash.jail", boolToString(newVal))
		applyBashJailToggle(m.ctx, newVal)
		m.flash(fmt.Sprintf("Bash jail %s", toggleNextLabel(!newVal)))
		m.settingSandbox()
	case "bash_max_score":
		m.ctx.ShowInput("Bash max complexity score (0 for default):", fmt.Sprintf("%d", cfg.Tools.Bash.MaxComplexityScore), func(v string, ok bool) {
			if ok && v != "" {
				m.applySet("tools.bash.max_complexity_score", v)
				applyBashMaxScore(m.ctx, v)
			}
			m.settingSandbox()
		})
	case "terminal_sandbox":
		newVal := !cfg.Tools.Terminal.Sandbox.Enabled
		m.applySet("tools.terminal.sandbox.enabled", boolToString(newVal))
		applyTerminalSandboxToggle(m.ctx, newVal)
		m.flash(fmt.Sprintf("Terminal sandbox %s", toggleNextLabel(!newVal)))
		m.settingSandbox()
	case "bash_blocked", "bash_allowed":
		m.flash("Edit blocked/allowed command lists in .goa/config.yaml or ~/.goa/config.yaml.")
		m.settingSandbox()
	}
}

func boolToString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// applyBashComplexityToggle updates the running bash tool and its analyzer
// when the user toggles complexity analysis from /config. Because the
// conversation is already active when this toggle is used, a steering message
// is injected so the LLM learns about the new restriction.
func applyBashComplexityToggle(ctx core.Context, enabled bool) {
	if ctx.ToolRegistry == nil {
		return
	}
	toolIface, ok := ctx.ToolRegistry.Get("bash")
	if !ok {
		return
	}
	bashTool, ok := toolIface.(*tools.BashTool)
	if !ok {
		return
	}
	bashTool.EnableComplexity = enabled
	if bashTool.Analyzer == nil {
		cfg := ctx.Config.Tools.Bash
		if len(cfg.BlockedCommands) > 0 || len(cfg.AllowedCommands) > 0 || enabled {
			bashTool.Analyzer = &sandbox.Analyzer{
				Blocked:            cfg.BlockedCommands,
				Allowed:            cfg.AllowedCommands,
				MaxComplexityScore: cfg.MaxComplexityScore,
				EnableComplexity:   &enabled,
			}
		}
	}
	if bashTool.Analyzer != nil {
		bashTool.Analyzer.EnableComplexity = &enabled
	}
	if ctx.AgentManager == nil {
		return
	}
	if enabled {
		_ = ctx.AgentManager.InjectSystemMessage(bashTool.ComplexityNotice())
	}
}

// applyBashJailToggle updates the running bash tool's jail flag.
func applyBashJailToggle(ctx core.Context, enabled bool) {
	if ctx.ToolRegistry == nil {
		return
	}
	toolIface, ok := ctx.ToolRegistry.Get("bash")
	if !ok {
		return
	}
	if bashTool, ok := toolIface.(*tools.BashTool); ok {
		bashTool.Jail = enabled
	}
}

// applyBashMaxScore updates the running bash analyzer's max complexity score.
func applyBashMaxScore(ctx core.Context, value string) {
	score, err := strconv.Atoi(value)
	if err != nil {
		return
	}
	if ctx.ToolRegistry == nil {
		return
	}
	toolIface, ok := ctx.ToolRegistry.Get("bash")
	if !ok {
		return
	}
	if bashTool, ok := toolIface.(*tools.BashTool); ok && bashTool.Analyzer != nil {
		bashTool.Analyzer.MaxComplexityScore = score
	}
}

// applyTerminalSandboxToggle updates the running terminal tool's bypass flag.
func applyTerminalSandboxToggle(ctx core.Context, enabled bool) {
	if ctx.ToolRegistry == nil {
		return
	}
	toolIface, ok := ctx.ToolRegistry.Get("terminal")
	if !ok {
		return
	}
	if terminalTool, ok := toolIface.(*tools.TerminalTool); ok {
		terminalTool.Bypass = !enabled
	}
}
