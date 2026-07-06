// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	orchpanel "github.com/pijalu/goa/tui/orchestrator"
)

// updateOrchInputPrompt sets the input editor's title to reflect the steering
// target implied by the active orchestration tab: "steer <role>:" for an agent
// tab, "steer all:" for Stats/All, and "" when no run is active. Must be called
// on the command loop (it mutates the editor + reads the view).
func (a *App) updateOrchInputPrompt() {
	inp := a.subs.getInput()
	if inp == nil {
		return
	}
	v := a.subs.agentView
	if v == nil || !v.Active() {
		inp.SetTitle("")
		return
	}
	inp.SetTitle(orchSteerPrompt(v))
}

// orchSteerPrompt returns the prompt label for the active tab.
func orchSteerPrompt(v *orchpanel.MultiAgentView) string {
	if id := v.ActiveAgentID(); id != "" {
		return "steer " + orchRoleLabel(v, id) + ":"
	}
	return "steer all:"
}

// orchRoleLabel returns the human-readable role for an agent id (falls back to
// the id when the role is unknown).
func orchRoleLabel(v *orchpanel.MultiAgentView, id string) string {
	if l := v.LogFor(id); l != nil && l.Role != "" {
		return l.Role
	}
	return id
}

// cycleAgentTab moves the active orchestration tab by dir and refreshes the
// steering prompt. Invoked on the command loop from a hotkey shortcut.
func (a *App) cycleAgentTab(dir int) {
	v := a.subs.agentView
	if v == nil || !v.Active() {
		return
	}
	v.Cycle(dir)
	a.updateOrchInputPrompt()
}

// selectAgentTab selects a tab by key (or 1-based index) and refreshes the
// steering prompt. Returns whether the selection matched. Invoked on the
// command loop from the /orchestrate:tab command.
func (a *App) selectAgentTab(sel string) bool {
	v := a.subs.agentView
	if v == nil || !v.Active() {
		return false
	}
	ok := v.SelectByKey(sel)
	if ok {
		a.updateOrchInputPrompt()
	}
	return ok
}
