// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plan

import (
	"strings"

	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/internal/tuirender"
)

// PlanToolRenderer renders plan tool calls in the TUI.
type PlanToolRenderer struct{}

var _ tuirender.ToolRenderer = (*PlanToolRenderer)(nil)

// RenderCall returns the header for a plan tool call.
func (r *PlanToolRenderer) RenderCall(args map[string]any, ctx tuirender.RenderContext) string {
	type renderInfo struct{ icon, label, detail string }
	action, _ := args["action"].(string)
	title, _ := args["title"].(string)
	id, _ := args["id"].(string)

	infos := map[string]renderInfo{
		"add_item":         {"📋 add", title, ""},
		"update_item":      {"✏️ update", id, ""},
		"remove_item":      {"🗑️ remove", id, ""},
		"reorder":          {"🔀 reorder", "", ""},
		"get":              {"📄 get", "", ""},
		"submit_review":    {"📬 submit review", "", ""},
		"resolve_comment":  {"✅ resolve comment", id, ""},
		"start_item":       {"▶️ start", id, ""},
		"complete_item":    {"✅ complete", id, ""},
		"block_item":       {"🚫 block", id, ""},
		"skip_item":        {"⏭️ skip", id, ""},
	}

	info, ok := infos[action]
	if !ok {
		parts := []string{"📋 plan"}
		if action != "" {
			parts = append(parts, action)
		}
		return strings.Join(parts, " ")
	}

	var parts []string
	parts = append(parts, info.icon, info.label)
	if info.detail != "" {
		if info.icon == "📋 add" && title != "" {
			parts = append(parts, ansi.Bold+truncateForRender(title, 40)+ansi.BoldReset)
		} else if info.detail != "" {
			parts = append(parts, info.detail)
		}
	}
	return strings.Join(parts, " ")
}

// RenderResult returns the body text for a plan tool result.
func (r *PlanToolRenderer) RenderResult(output string, ctx tuirender.RenderContext) string {
	if ctx.IsError || output == "" {
		return output
	}
	// For get results containing markdown, show a brief excerpt.
	if strings.HasPrefix(output, "# Plan:") {
		lines := strings.SplitN(output, "\n", 5)
		excerpt := strings.Join(lines[:minInt(len(lines), 4)], "\n")
		if len(lines) > 4 {
			excerpt += "\n…"
		}
		return excerpt
	}
	return output
}

// PreviewLines returns the number of preview lines when collapsed.
func (r *PlanToolRenderer) PreviewLines() int { return 2 }

// HideResultWhenCollapsed hides the result when collapsed.
func (r *PlanToolRenderer) HideResultWhenCollapsed() bool { return false }

// truncateForRender truncates a string to maxLen runes.
func truncateForRender(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen-1]) + "…"
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TaskOutcomeRenderer renders task_outcome tool calls in the TUI.
type TaskOutcomeRenderer struct{}

var _ tuirender.ToolRenderer = (*TaskOutcomeRenderer)(nil)

// RenderCall returns the header for a task_outcome tool call.
func (r *TaskOutcomeRenderer) RenderCall(args map[string]any, ctx tuirender.RenderContext) string {
	status, _ := args["status"].(string)
	switch status {
	case "done":
		return "✅ task done"
	case "needs_clarification":
		return "❓ needs clarification"
	case "blocked":
		return "🚫 task blocked"
	default:
		return "📋 task outcome"
	}
}

// RenderResult returns the body text for a task_outcome result.
func (r *TaskOutcomeRenderer) RenderResult(output string, ctx tuirender.RenderContext) string {
	return output
}

// PreviewLines returns the number of preview lines when collapsed.
func (r *TaskOutcomeRenderer) PreviewLines() int { return 1 }

// HideResultWhenCollapsed hides the result when collapsed.
func (r *TaskOutcomeRenderer) HideResultWhenCollapsed() bool { return false }
