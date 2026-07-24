// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"encoding/json"
	"fmt"

	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/internal/tuirender"
)

// GoalRenderer renders the unified `goal` tool calls and results (bugs.md S2).
// It dispatches on the `action` argument to produce the same concise headers
// the four legacy per-tool renderers produced.
type GoalRenderer struct{}

// RenderCall implements tuirender.ToolRenderer.
func (r GoalRenderer) RenderCall(args map[string]any, ctx tuirender.RenderContext) string {
	switch extractArg(args, "action") {
	case "create":
		return ansiBold("◆ Started goal") + " " + ansiMuted(extractArg(args, "objective"))
	case "update":
		switch extractArg(args, "status") {
		case "complete":
			return ansiBold("◆ Reported goal complete")
		case "blocked":
			return ansiBold("◆ Reported goal blocked")
		case "paused":
			return ansiBold("◆ Paused goal")
		case "active":
			return ansiBold("◆ Resumed goal")
		default:
			return ansiBold("◆ Updated goal") + " " + ansiMuted(extractArg(args, "status"))
		}
	case "get":
		return ansiBold("◆ Checked goal")
	case "set_budget":
		return ansiBold("◆ Set goal budget") + " " + ansiMuted(fmt.Sprintf("(%s %s)", extractArg(args, "value"), extractArg(args, "unit")))
	default:
		return ansiBold("◆ Goal")
	}
}

// RenderResult implements tuirender.ToolRenderer.
func (r GoalRenderer) RenderResult(output string, ctx tuirender.RenderContext) string {
	return renderGoalSummary(output)
}

// PreviewLines returns the number of preview lines.
func (r GoalRenderer) PreviewLines() int { return 3 }

// HideResultWhenCollapsed returns false.
func (r GoalRenderer) HideResultWhenCollapsed() bool { return false }

func renderGoalSummary(output string) string {
	var result struct {
		Goal *struct {
			Objective   string `json:"objective"`
			Status      string `json:"status"`
			TurnsUsed   int    `json:"turnsUsed"`
			TokensUsed  int    `json:"tokensUsed"`
			WallClockMs int64  `json:"wallClockMs"`
		} `json:"goal"`
	}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		return ""
	}
	if result.Goal == nil {
		return "No current goal"
	}
	return fmt.Sprintf("Goal %s: %s · %d turns · %s tokens · %s",
		result.Goal.Status, result.Goal.Objective, result.Goal.TurnsUsed,
		formatTokens(result.Goal.TokensUsed), formatElapsed(result.Goal.WallClockMs))
}

func extractArg(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	if v, ok := args[key].(float64); ok {
		return fmt.Sprintf("%g", v)
	}
	if v, ok := args[key].(bool); ok {
		return fmt.Sprintf("%t", v)
	}
	return ""
}

func ansiBold(s string) string {
	return ansi.Bold + ansi.Fg(ansiColorPrimary) + s + ansi.Reset + ansi.BoldReset
}

func ansiMuted(s string) string {
	return ansi.Faint + s + ansi.Reset
}

func formatTokens(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 1_000_000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
}

func formatElapsed(ms int64) string {
	s := ms / 1000
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	m := s / 60
	s = s % 60
	return fmt.Sprintf("%dm%02ds", m, s)
}
