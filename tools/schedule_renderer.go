// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"strings"

	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/internal/tuirender"
)

// ScheduleRenderer displays schedule_create / schedule_delete / schedule_list
// calls and their JSON results.
type ScheduleRenderer struct{}

var _ tuirender.ToolRenderer = (*ScheduleRenderer)(nil)

// RenderCall renders the schedule call header.
func (ScheduleRenderer) RenderCall(args map[string]any, ctx tuirender.RenderContext) string {
	var label string
	switch {
	case args["prompt"] != nil:
		prompt := stringArg(args, "prompt")
		if ansi.Width(prompt) > 60 {
			prompt = ansi.Truncate(prompt, 57) + "..."
		}
		label = "Create reminder: " + prompt
	case args["id"] != nil:
		label = "Delete reminder: " + stringArg(args, "id")
	default:
		label = "List reminders"
	}
	return rToolTitle("schedule") + rMuted(" · ") + label
}

// RenderResult renders the schedule tool output.
func (ScheduleRenderer) RenderResult(output string, ctx tuirender.RenderContext) string {
	if output == "" {
		return ""
	}
	if strings.HasPrefix(output, "[") || strings.HasPrefix(output, "{") {
		return rToolOutput(output)
	}
	return rToolOutput(output)
}

func (ScheduleRenderer) PreviewLines() int             { return 12 }
func (ScheduleRenderer) HideResultWhenCollapsed() bool { return false }
