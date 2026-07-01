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

// CreateGoalRenderer renders CreateGoal tool calls and results.
type CreateGoalRenderer struct{}

// RenderCall implements tuirender.ToolRenderer.
func (r CreateGoalRenderer) RenderCall(args map[string]any, ctx tuirender.RenderContext) string {
	objective := extractArg(args, "objective")
	return ansiBold("◆ Started goal") + " " + ansiMuted(objective)
}

// RenderResult implements tuirender.ToolRenderer.
func (r CreateGoalRenderer) RenderResult(output string, ctx tuirender.RenderContext) string {
	return renderGoalSummary(output)
}

// PreviewLines returns the number of preview lines.
func (r CreateGoalRenderer) PreviewLines() int { return 3 }

// HideResultWhenCollapsed returns false.
func (r CreateGoalRenderer) HideResultWhenCollapsed() bool { return false }

// UpdateGoalRenderer renders UpdateGoal tool calls and results.
type UpdateGoalRenderer struct{}

// RenderCall implements tuirender.ToolRenderer.
func (r UpdateGoalRenderer) RenderCall(args map[string]any, ctx tuirender.RenderContext) string {
	status := extractArg(args, "status")
	switch status {
	case "complete":
		return ansiBold("◆ Reported goal complete")
	case "blocked":
		return ansiBold("◆ Reported goal blocked")
	case "paused":
		return ansiBold("◆ Paused goal")
	case "active":
		return ansiBold("◆ Resumed goal")
	default:
		return ansiBold("◆ Updated goal") + " " + ansiMuted(status)
	}
}

// RenderResult implements tuirender.ToolRenderer.
func (r UpdateGoalRenderer) RenderResult(output string, ctx tuirender.RenderContext) string {
	return ""
}

// PreviewLines returns the number of preview lines.
func (r UpdateGoalRenderer) PreviewLines() int { return 0 }

// HideResultWhenCollapsed returns true.
func (r UpdateGoalRenderer) HideResultWhenCollapsed() bool { return true }

// GetGoalRenderer renders GetGoal tool calls and results.
type GetGoalRenderer struct{}

// RenderCall implements tuirender.ToolRenderer.
func (r GetGoalRenderer) RenderCall(args map[string]any, ctx tuirender.RenderContext) string {
	return ansiBold("◆ Checked goal")
}

// RenderResult implements tuirender.ToolRenderer.
func (r GetGoalRenderer) RenderResult(output string, ctx tuirender.RenderContext) string {
	return renderGoalSummary(output)
}

// PreviewLines returns the number of preview lines.
func (r GetGoalRenderer) PreviewLines() int { return 3 }

// HideResultWhenCollapsed returns false.
func (r GetGoalRenderer) HideResultWhenCollapsed() bool { return false }

// SetGoalBudgetRenderer renders SetGoalBudget tool calls and results.
type SetGoalBudgetRenderer struct{}

// RenderCall implements tuirender.ToolRenderer.
func (r SetGoalBudgetRenderer) RenderCall(args map[string]any, ctx tuirender.RenderContext) string {
	value := extractArg(args, "value")
	unit := extractArg(args, "unit")
	return ansiBold("◆ Set goal budget") + " " + ansiMuted(fmt.Sprintf("(%s %s)", value, unit))
}

// RenderResult implements tuirender.ToolRenderer.
func (r SetGoalBudgetRenderer) RenderResult(output string, ctx tuirender.RenderContext) string {
	return ""
}

// PreviewLines returns the number of preview lines.
func (r SetGoalBudgetRenderer) PreviewLines() int { return 0 }

// HideResultWhenCollapsed returns true.
func (r SetGoalBudgetRenderer) HideResultWhenCollapsed() bool { return true }

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
