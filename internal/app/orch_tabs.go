// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
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
	// A pending main-input request owns the editor title; do not clobber it
	// with the steer prompt on every orchestration event. The title is
	// restored by clearMainInputRequest.
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

// updateOrchFooterStats writes one rich stats line per active role into the
// footer, mirroring the normal conversation stat/model line so the formats
// match. Multiple handles for the same role are aggregated into a single
// line ("minimal agents"). At most the last 5 distinct roles are shown so
// the footer never grows unbounded.
func (a *App) updateOrchFooterStats() {
	if a.subs.footer == nil || a.subs.agentView == nil {
		return
	}
	v := a.subs.agentView
	if !v.Active() {
		a.subs.footer.SetData(tui.FooterData{OrchestrationStats: ""})
		return
	}
	rows := v.Rows()
	aggregated := aggregateByRole(rows)
	if len(aggregated) > 5 {
		aggregated = aggregated[len(aggregated)-5:]
	}
	lines := make([]string, 0, len(aggregated))
	for _, r := range aggregated {
		lines = append(lines, formatOrchAgentLine(r))
	}
	stats := strings.Join(lines, "\n")
	if stats == "" {
		stats = "orchestration running"
	}
	a.subs.footer.SetData(tui.FooterData{OrchestrationStats: stats})
}

// aggregateByRole groups per-handle rows by role, summing token/cache/tool
// counters and keeping the most active status. The view still tracks rows
// per AgentID; the footer displays one minimal line per role.
func aggregateByRole(rows []orchpanel.AgentEnhancedRow) []orchpanel.AgentEnhancedRow {
	type acc struct {
		row       orchpanel.AgentEnhancedRow
		seenFirst int
	}
	byRole := map[string]*acc{}
	for i, r := range rows {
		a, ok := byRole[r.Role]
		if !ok {
			byRole[r.Role] = &acc{row: r, seenFirst: i}
			continue
		}
		a.row.TokensIn += r.TokensIn
		a.row.TokensOut += r.TokensOut
		a.row.CacheRead += r.CacheRead
		a.row.CacheCreation += r.CacheCreation
		a.row.ToolCalls += r.ToolCalls
		a.row.Turns += r.Turns
		// Upgrade status: running > idle > finished.
		if r.Status == "running" || (r.Status == "idle" && a.row.Status == "finished") {
			a.row.Status = r.Status
		}
		// Keep the latest context estimate when present.
		if r.ContextMax > 0 {
			a.row.ContextEstimate = r.ContextEstimate
			a.row.ContextMax = r.ContextMax
			a.row.ContextAutoMax = r.ContextAutoMax
		}
	}
	out := make([]orchpanel.AgentEnhancedRow, 0, len(byRole))
	seen := map[string]bool{}
	for _, r := range rows {
		if seen[r.Role] {
			continue
		}
		seen[r.Role] = true
		out = append(out, byRole[r.Role].row)
	}
	return out
}

// formatOrchAgentLine renders one aggregated role footer line using the SAME
// stats and model formatting as the normal footer line, prefixed with the
// role label.
func formatOrchAgentLine(r orchpanel.AgentEnhancedRow) string {
	label := roleLabel(r)
	stats := sessionStats{
		PromptN:         r.TokensIn,
		PredictedN:      r.TokensOut,
		CacheReadTotal:  r.CacheRead,
		CacheWriteTotal: r.CacheCreation,
		ToolCalls:       r.ToolCalls,
		ContextEstimate: r.ContextEstimate,
		ContextMax:      r.ContextMax,
		ContextAutoMax:  r.ContextAutoMax,
	}
	active := r.Status == "running"
	busy := active
	return label + ": " + tui.FormatFooterLine(formatFooterStats(stats), r.Model, r.Provider, r.Thinking, "", busy, active)
}

// roleLabel returns a human-readable, title-cased label for the row's role.
func roleLabel(r orchpanel.AgentEnhancedRow) string {
	label := strings.TrimSpace(r.Role)
	if label == "" {
		label = strings.TrimSpace(r.Label)
	}
	if label == "" {
		label = r.AgentID
	}
	if label == "" {
		label = "?"
	}
	return titleFirst(label)
}

// titleFirst uppercases the first ASCII letter of s for readable role labels
// ("orchestrator" -> "Orchestrator"). Non-ASCII and already-uppercase inputs
// are returned unchanged.
func titleFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	if r[0] >= 'a' && r[0] <= 'z' {
		r[0] = r[0] - 'a' + 'A'
	}
	return string(r)
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
