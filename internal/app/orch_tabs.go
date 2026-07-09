// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/tui"
	orchpanel "github.com/pijalu/goa/tui/orchestrator"
)

// updateOrchInputPrompt sets the input editor's title to reflect the current
// steering target: "steer <role>" for a selected agent, "steer all" for the
// broadcast target, and "" when no run is active. The label is rendered inside
// the editor's bordered title ("┨ steer <role> ┠"), so it must not end with ":"
// — the colon would collide with the closing "┠" bracket (see
// Editor.SetTitle normalization). Must be called on the command loop (it
// mutates the editor + reads the view).
func (a *App) updateOrchInputPrompt() {
	inp := a.subs.getInput()
	if inp == nil {
		return
	}
	// A pending main-input request owns the editor title (and the
	// PendingInputBox); do not clobber it with the steer prompt on every
	// orchestration event. The title is restored by clearMainInputRequest.
	if a.pendingInput != nil {
		return
	}
	v := a.subs.agentView
	if v == nil || !v.Active() {
		inp.SetTitle("")
		return
	}
	inp.SetTitle(orchSteerPrompt(v))
}

// updateOrchFooterStats writes the per-model orchestration counters into the
// footer as a single line below the input. No aggregate sum is shown; each
// model row is listed separately and separated by " | ".
func (a *App) updateOrchFooterStats() {
	if a.subs.footer == nil || a.subs.agentView == nil {
		return
	}
	v := a.subs.agentView
	if !v.Active() {
		a.subs.footer.SetData(tui.FooterData{OrchestrationStats: ""})
		return
	}
	var parts []string
	for _, r := range v.Rows() {
		ch := "-"
		if r.CacheRead > 0 {
			ch = formatK(r.CacheRead)
		}
		parts = append(parts, fmt.Sprintf("%s ↑%s ↓%s CH=%s", r.Role, formatK(r.TokensIn), formatK(r.TokensOut), ch))
	}
	stats := strings.Join(parts, " | ")
	if stats == "" {
		stats = "orchestration running"
	}
	a.subs.footer.SetData(tui.FooterData{OrchestrationStats: stats})
}

func formatK(n int) string {
	if n == 0 {
		return "0"
	}
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	if n%1000 == 0 {
		return fmt.Sprintf("%dk", n/1000)
	}
	return fmt.Sprintf("%.1fk", float64(n)/1000)
}

// orchSteerPrompt returns the prompt label for the current steering target.
// The label is used verbatim as the editor title and must not carry a trailing
// colon.
func orchSteerPrompt(v *orchpanel.MultiAgentView) string {
	if id := v.SteerTarget(); id != "" {
		return "steer " + orchRoleLabel(v, id)
	}
	return "steer all"
}

// orchRoleLabel returns the human-readable role for an agent id (falls back to
// the id when the role is unknown).
func orchRoleLabel(v *orchpanel.MultiAgentView, id string) string {
	if l := v.LogFor(id); l != nil && l.Role != "" {
		return l.Role
	}
	return id
}

// openSteerTargetSelector opens the steering target picker overlay. It lists
// the run's targets ("all", orchestrator, each agent) as a numbered menu and
// lets the user jump by number, navigate with arrows, and confirm with enter
// (esc cancels). No-op when no run is active. Invoked from ctrl+x.
func (a *App) openSteerTargetSelector() {
	v := a.subs.agentView
	if v == nil || !v.Active() {
		return
	}
	engine := a.subs.tuiEngine
	if engine == nil {
		return
	}
	picker := orchpanel.NewSteerTargetPicker(v)
	handle := engine.ShowOverlay(picker, tui.OverlayOptions{CaptureInput: true})
	picker.SetCloseFunc(func() { handle.Hide() })
	picker.SetPickFunc(func(target string) {
		if target != "" {
			v.SetSteerTarget(target)
			a.updateOrchInputPrompt()
		}
	})
}

// openAgentTabSelector is the legacy name for the picker overlay; ctrl+x now
// opens the steering target selector instead of a tab switcher.
func (a *App) openAgentTabSelector() {
	a.openSteerTargetSelector()
}

// selectAgentTab is a legacy no-op kept so the /orchestrate:tab command does
// not crash. Tab switching was removed; the chat is always visible.
func (a *App) selectAgentTab(sel string) bool {
	v := a.subs.agentView
	if v == nil || !v.Active() {
		return false
	}
	return v.SelectByKey(sel)
}

func (a *App) orchSelectTabLabel(key string) (string, bool) {
	if !a.selectAgentTab(key) {
		return "", false
	}
	if tab, ok := a.subs.agentView.ActiveTab(); ok {
		return tab.Label, true
	}
	return "", true
}

// wireOrchCommandCallbacks connects the /orchestrate command's host callbacks.
// Called once during TUI assembly; safe to call before any run is active (the
// callbacks no-op then).
func (a *App) wireOrchCommandCallbacks() {
	if cmd := a.subs.orchCmd; cmd != nil {
		cmd.SelectAgentTab = a.orchSelectTabLabel
	}
}
