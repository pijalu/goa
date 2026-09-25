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

// planCallIcons maps each plan action to the icon shown in its call header.
var planCallIcons = map[string]string{
	"add_item":        "📋 add",
	"update_item":     "✏️ update",
	"remove_item":     "🗑️ remove",
	"reorder":         "🔀 reorder",
	"get":             "📄 get",
	"submit_review":   "📬 submit review",
	"resolve_comment": "✅ resolve comment",
	"start_item":      "▶️ start",
	"complete_item":   "✅ complete",
	"block_item":      "🚫 block",
	"skip_item":       "⏭️ skip",
}

// planCallLabelLimit caps the dynamic part of a call header (item title) so a
// long plan item cannot flood the transcript line.
const planCallLabelLimit = 40

// RenderCall returns the header for a plan tool call. Known actions show their
// icon plus the item title (add_item) or item/comment id (everything else);
// unknown or missing actions fall back to a generic plan header.
func (r *PlanToolRenderer) RenderCall(args map[string]any, ctx tuirender.RenderContext) string {
	action, _ := args["action"].(string)
	icon, known := planCallIcons[action]
	if !known {
		if action == "" {
			return "📋 plan"
		}
		return "📋 plan " + action
	}

	label := planCallLabel(action, args)
	if label == "" {
		return icon
	}
	if action == "add_item" {
		return icon + " " + ansi.Bold + truncateForRender(label, planCallLabelLimit) + ansi.BoldReset
	}
	return icon + " " + label
}

// planCallLabel returns the argument worth showing for an action: the item
// title for add_item, the item/comment id otherwise.
func planCallLabel(action string, args map[string]any) string {
	key := "id"
	if action == "add_item" {
		key = "title"
	}
	label, _ := args[key].(string)
	return label
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
