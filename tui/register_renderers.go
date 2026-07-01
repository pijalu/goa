// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"github.com/pijalu/goa/tools"
	goaltui "github.com/pijalu/goa/tui/goal"
	swarm "github.com/pijalu/goa/tui/swarm"
)

func init() {
	tools.Themer = TheTheme
	RegisterToolRenderer("read", tools.NewReadFileRenderer())
	RegisterToolRenderer("write", tools.NewWriteFileRenderer())
	RegisterToolRenderer("edit", tools.NewEditFileRenderer())
	RegisterToolRenderer("bash", tools.NewBashRenderer())
	RegisterToolRenderer("terminal", tools.TerminalRenderer{})
	RegisterToolRenderer("webfetch", tools.NewWebFetchRenderer())
	RegisterToolRenderer("search", tools.NewSearchRenderer())
	RegisterToolRenderer("smartsearch", tools.NewSmartSearchRenderer())
	RegisterToolRenderer("CreateGoal", goaltui.CreateGoalRenderer{})
	RegisterToolRenderer("UpdateGoal", goaltui.UpdateGoalRenderer{})
	RegisterToolRenderer("GetGoal", goaltui.GetGoalRenderer{})
	RegisterToolRenderer("SetGoalBudget", goaltui.SetGoalBudgetRenderer{})
	RegisterToolRenderer("agent", &tools.AgentToolRenderer{})
	RegisterToolRenderer("agent_swarm", &swarm.AgentSwarmRenderer{})
}

// SyncToolTheme updates the theme provider used by tool renderers. Call this
// after switching themes at runtime.
func SyncToolTheme() {
	tools.Themer = TheTheme
}
