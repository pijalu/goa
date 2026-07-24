// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"github.com/pijalu/goa/tools"
	plantools "github.com/pijalu/goa/tools/plan"
	goaltui "github.com/pijalu/goa/tui/goal"
	swarm "github.com/pijalu/goa/tui/swarm"
)

func init() {
	tools.Themer = TheTheme
	RegisterToolRenderer("read", tools.NewReadFileRenderer())
	RegisterToolRenderer("write", tools.NewWriteFileRenderer())
	RegisterToolRenderer("edit", tools.NewEditFileRenderer())
	RegisterToolRenderer("bash", tools.NewBashRenderer())
	RegisterToolRenderer("python", tools.NewPythonRenderer())
	RegisterToolRenderer("verify", tools.NewVerifyRenderer())
	RegisterToolRenderer("terminal", tools.TerminalRenderer{})
	RegisterToolRenderer("webfetch", tools.NewWebFetchRenderer())
	RegisterToolRenderer("search", tools.NewSearchRenderer())
	RegisterToolRenderer("smartsearch", tools.NewSmartSearchRenderer())
	RegisterToolRenderer("goal", goaltui.GoalRenderer{})
	RegisterToolRenderer("agent", &tools.AgentToolRenderer{})
	RegisterToolRenderer("agent_swarm", &swarm.AgentSwarmRenderer{})
	RegisterToolRenderer("plan", &plantools.PlanToolRenderer{})
	RegisterToolRenderer("task_outcome", &plantools.TaskOutcomeRenderer{})
}

// SyncToolTheme updates the theme provider used by tool renderers. Call this
// after switching themes at runtime.
func SyncToolTheme() {
	tools.Themer = TheTheme
}

// SetToolProjectDir forwards the workspace directory to tool renderers that
// need it to display project-relative information (e.g. the verify renderer,
// which auto-detects the test framework for its command-line display). Call
// this once the engine and project directory are known.
func SetToolProjectDir(dir string) {
	if r, ok := ToolRendererRegistry["verify"].(*tools.VerifyRenderer); ok {
		r.SetProjectDir(dir)
	}
}
