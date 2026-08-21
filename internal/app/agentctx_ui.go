// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strconv"

	"github.com/pijalu/goa/multiagent"
	"github.com/pijalu/goa/tui/agentctx"
)

// agentctx_ui.go keeps the input prompt and the footer in sync with the
// ACTIVE multi-agent tab (T5): the editor title names the steering target
// ("steer coder·dlg-03" on a running delegation tab, "steer all" on main
// while delegations run), and the footer carries the active tab's stat line.
//
// Ownership: both refreshers read registry/status state and mutate the
// editor/footer, so they must run on the TUI command loop — every caller
// here is already inside a.apply / ApplySync.

// refreshAgentCtxChrome updates BOTH active-tab chrome pieces. Cheap enough
// to call on every delegation event; it no-ops without an engine/editor.
func (a *App) refreshAgentCtxChrome() {
	a.updateAgentCtxPrompt()
	a.updateAgentCtxFooter()
}

// updateAgentCtxPrompt reflects the ACTIVE tab in the input editor's title:
//   - a RUNNING delegation tab  → "steer <label>"  (input steers that run)
//   - the main tab, delegations live → "steer all" (input steers the main agent)
//   - otherwise                 → no title (plain prompt)
//
// A pending main-input request owns the title; like updateOrchInputPrompt we
// never clobber it (clearMainInputRequest restores the steer label).
func (a *App) updateAgentCtxPrompt() {
	inp := a.subs.getInput()
	if inp == nil || a.pendingInput != nil {
		return
	}
	reg := a.subs.agentRegistry
	if reg == nil {
		return
	}
	id, _ := reg.Active()
	switch {
	case id == "" || id == agentctx.MainAgentID:
		if reg.Len() > 1 && a.hasRunningDelegations() {
			inp.SetTitle("steer all")
			return
		}
		inp.SetTitle("")
	case a.delegationStatus(id) == multiagent.DelegationRunning:
		inp.SetTitle("steer " + agentctx.TabLabel(id))
	default:
		inp.SetTitle("")
	}
}

// updateAgentCtxFooter writes the ACTIVE tab's stat line into the footer via
// SetAgentStats (the sole writer): "Coder·dlg-03 · running · 4 blocks". The
// main tab clears the line — its session stats already live in the footer's
// second line.
func (a *App) updateAgentCtxFooter() {
	if a.subs.footer == nil {
		return
	}
	reg := a.subs.agentRegistry
	if reg == nil {
		return
	}
	id, view := reg.Active()
	if id == "" || id == agentctx.MainAgentID || view == nil {
		a.subs.footer.SetAgentStats("")
		return
	}
	a.subs.footer.SetAgentStats(formatAgentTabStats(a.delegationStatus(id), id, view.Transcript.Len()))
}

// formatAgentTabStats renders one delegation tab's footer line, mirroring the
// orchestration stats shape ("Label: status") plus what the neutral feed
// actually carries: lifecycle status and accumulated transcript blocks.
func formatAgentTabStats(status, id string, entries int) string {
	label := titleFirst(agentctx.TabLabel(id))
	if status == "" {
		status = "idle"
	}
	return label + ": " + status + " · " + strconv.Itoa(entries) + " blocks"
}
