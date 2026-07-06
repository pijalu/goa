// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"github.com/pijalu/goa/core/orchestrator"
	orchpanel "github.com/pijalu/goa/tui/orchestrator"
	"github.com/pijalu/goa/tui"
)

// runOrchestratorPanelForwarder is the single owner of the orchestrator
// Summary overlay. It watches the active-runtime holder; whenever a new run
// becomes active it shows a Panel overlay and drains that run's events ON THE
// COMMAND LOOP (via a.apply) so component state is mutated only by the loop —
// preserving the R1 single-owner invariant. When the run finishes the overlay
// is hidden after a short dwell so the user can read the final table.
func (a *App) runOrchestratorPanelForwarder(done <-chan struct{}) {
	for {
		select {
		case <-done:
			return
		case <-a.subs.orchActive.Notify():
			a.drainActiveOrchRun(done)
		}
	}
}

// drainActiveOrchRun attaches a Panel to the active run and forwards events
// until the run completes. Returns when the run is done or the app is stopping.
func (a *App) drainActiveOrchRun(done <-chan struct{}) {
	rt := a.subs.orchActive.Get()
	if rt == nil {
		return
	}
	panel := orchpanel.NewPanel()
	a.apply(func() {
		a.subs.orchPanel = panel
		if inp := a.subs.getInput(); inp != nil {
			inp.SetTitle("steer all:")
		}
	})
	handle := a.subs.tuiEngine.ShowOverlay(panel, tui.OverlayOptions{Width: 0, Height: 0, CaptureInput: false})
	a.apply(func() { a.subs.orchPanelHandle = handle })
	defer func() {
		handle.Hide()
		a.apply(func() {
			a.subs.orchPanel = nil
			a.subs.orchPanelHandle = nil
			if inp := a.subs.getInput(); inp != nil {
				inp.SetTitle("")
			}
		})
	}()

	sub := rt.Subscribe()
	for {
		select {
		case <-done:
			return
		case <-rt.Done():
			a.apply(func() { panel.SetRows(snapshotRows(rt)) })
			return
		case ev := <-sub:
			a.apply(func() {
				panel.ApplyEvent(ev)
				panel.SetRows(snapshotRows(rt))
			})
		}
	}
}

// snapshotRows converts the runtime's live snapshot into panel rows.
func snapshotRows(rt *orchestrator.Runtime) []orchpanel.Row {
	if rt == nil {
		return nil
	}
	src := rt.Snapshot()
	out := make([]orchpanel.Row, 0, len(src))
	for _, r := range src {
		out = append(out, orchpanel.Row{
			ID: r.ID, Role: r.Role, Model: r.Model, Status: string(r.Status),
			Turns: r.Turns, TokensIn: r.TokensIn, TokensOut: r.TokensOut, ToolCalls: r.ToolCalls,
		})
	}
	return out
}
