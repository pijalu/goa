// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package swarm

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/internal/tuirender"
)

// AgentSwarmRenderer renders agent_swarm calls and results.
type AgentSwarmRenderer struct{}

var _ tuirender.ToolRenderer = (*AgentSwarmRenderer)(nil)

func (r *AgentSwarmRenderer) RenderCall(args map[string]any, ctx tuirender.RenderContext) string {
	task := stringArg(args, "task")
	if task == "" {
		task = "swarm task"
	}
	items := sliceArg(args, "items")
	return fmt.Sprintf("🐝 %s (%d items)", task, len(items))
}

func (r *AgentSwarmRenderer) RenderResult(output string, ctx tuirender.RenderContext) string {
	if ctx.Expanded {
		return output
	}
	lines := strings.Split(output, "\n")
	if len(lines) <= 3 {
		return output
	}
	return strings.Join(lines[:3], "\n") + fmt.Sprintf("\n… %d more lines", len(lines)-3)
}

func (r *AgentSwarmRenderer) PreviewLines() int             { return 3 }
func (r *AgentSwarmRenderer) HideResultWhenCollapsed() bool { return false }

func stringArg(args map[string]any, key string) string {
	v, ok := args[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

func sliceArg(args map[string]any, key string) []string {
	v, ok := args[key]
	if !ok {
		return nil
	}
	raw, _ := v.([]any)
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
