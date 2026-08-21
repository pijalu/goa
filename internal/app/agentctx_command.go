// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"github.com/pijalu/goa/core/commands"
	"github.com/pijalu/goa/tui/agentctx"
)

// agentctx_command.go wires the /agent slash command to the multi-agent tab
// surface. The command type lives in core/commands and knows nothing about
// internal/app; the host callbacks here are the only bridge — the same
// Open/Closed adapter shape the orchestration command uses.

// newAgentCommand constructs the /agent command with live host callbacks
// bound to this App's registry/engine. Called once during subsystem assembly.
func (a *App) newAgentCommand() *commands.AgentCommand {
	cmd := &commands.AgentCommand{}
	cmd.SelectTab = a.selectAgentTabByRef
	cmd.ActiveTabs = a.agentTabRefs
	cmd.ReplayActiveTab = a.replayActiveTabRef
	return cmd
}

// selectAgentTabByRef makes the tab matching ref (registry id such as
// "dlg-coder-03" or its display label "coder·dlg-03") active, returning the
// display label. ok=false ⇒ unknown reference; the active view is unchanged.
func (a *App) selectAgentTabByRef(ref string) (string, bool) {
	reg := a.subs.agentRegistry
	if reg == nil {
		return "", false
	}
	id, ok := resolveAgentTabRef(reg, ref)
	if !ok {
		return "", false
	}
	if !a.switchAgentView(id) {
		return "", false
	}
	return agentctx.TabLabel(id), true
}

// agentTabRefs lists the selectable tabs as display labels in tab order —
// the actionable payload for unknown-tab errors.
func (a *App) agentTabRefs() []string {
	reg := a.subs.agentRegistry
	if reg == nil {
		return nil
	}
	ids := reg.IDs()
	labels := make([]string, 0, len(ids))
	for _, id := range ids {
		labels = append(labels, agentctx.TabLabel(id))
	}
	return labels
}

// replayActiveTabRef starts a deliberate full-history replay of the ACTIVE
// tab (/agent:replay) via the T3 ReplayRunner path, returning its label.
func (a *App) replayActiveTabRef() (string, bool) {
	if !a.replayActiveView() {
		return "", false
	}
	if reg := a.subs.agentRegistry; reg != nil {
		id, _ := reg.Active()
		return agentctx.TabLabel(id), true
	}
	return "", true
}

// resolveAgentTabRef matches a user-supplied tab reference against the
// registry: an exact id wins ("dlg-coder-03"), then the display label
// ("coder·dlg-03"). Matching is case-sensitive on purpose — ids are minted
// lowercase and labels derive from them.
func resolveAgentTabRef(reg *agentctx.AgentViewRegistry, ref string) (string, bool) {
	if ref == "" {
		return "", false
	}
	if _, ok := reg.Get(ref); ok {
		return ref, true
	}
	for _, id := range reg.IDs() {
		if agentctx.TabLabel(id) == ref {
			return id, true
		}
	}
	return "", false
}
