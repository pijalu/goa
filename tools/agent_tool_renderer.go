// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/internal/tuirender"
)

// AgentToolRenderer renders Agent tool calls and results.
type AgentToolRenderer struct{}

var _ tuirender.ToolRenderer = (*AgentToolRenderer)(nil)

func (r *AgentToolRenderer) RenderCall(args map[string]any, ctx tuirender.RenderContext) string {
	desc := stringArg(args, "description")
	if desc == "" {
		desc = "sub-agent task"
	}
	agentType := stringArg(args, "subagent_type")
	if agentType == "" {
		agentType = "coder"
	}
	return fmt.Sprintf("🤖 %s (%s)", desc, agentType)
}

func (r *AgentToolRenderer) RenderResult(output string, ctx tuirender.RenderContext) string {
	if output == "" {
		return ""
	}
	lines := strings.Split(output, "\n")
	if ctx.Expanded {
		return output
	}
	maxLines := previewLinesFromCtx(ctx, r.PreviewLines())
	if len(lines) <= maxLines {
		return output
	}
	return strings.Join(lines[:maxLines], "\n") + fmt.Sprintf("\n… %d more lines", len(lines)-maxLines)
}

func (r *AgentToolRenderer) PreviewLines() int             { return 4 }
func (r *AgentToolRenderer) HideResultWhenCollapsed() bool { return false }
