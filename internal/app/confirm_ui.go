// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"github.com/pijalu/goa/plugins"
	"github.com/pijalu/goa/tui"
)

// Plugin confirm presentation (plugins plan §4, Phase M3).
//
// drainPluginConfirms is started by activatePluginUI once the TUI exists. It
// is a dedicated goroutine — the same ownership pattern as
// drainSegmentRefreshes — that pops queued confirm requests FIFO and presents
// them ONE AT A TIME (§9 Q3: selector-style capturing overlays serialize, so
// only one modal ever competes for focus). Each presented card blocks the
// drain goroutine until the user answers or the engine stops; the TUI command
// loop itself stays free because plugin commands run async (their wrapper
// implements core.AsyncCommand) and goa.ui.confirm fails closed when invoked
// from the UI thread (SetBlockGuard below).

// startConfirmDrain attaches the bridge's confirm consumer to this app's
// presentation loop. Called on the command loop (activatePluginUI).
func (a *App) startConfirmDrain(engine *tui.TUI, rt *pluginRuntime) {
	rt.ui.SetBlockGuard(engine.OnCommandLoop)
	rt.ui.SetConfirmConsumer(true)
	go a.drainPluginConfirms(engine, rt)
}

// drainPluginConfirms presents queued confirms FIFO, one visible at a time.
// Exits when the engine stops; outstanding requests are failed with
// "shutdown" so no plugin stays parked on a dead UI.
func (a *App) drainPluginConfirms(engine *tui.TUI, rt *pluginRuntime) {
	defer rt.ui.SetConfirmConsumer(false)
	for {
		select {
		case <-rt.ui.ConfirmRequests():
		case <-engine.Stopped():
			rt.ui.CancelAllConfirms("shutdown")
			return
		}
		for {
			job, ok := rt.ui.NextConfirm()
			if !ok {
				break
			}
			if !a.presentConfirm(engine, job) {
				rt.ui.CancelAllConfirms("shutdown")
				return
			}
		}
	}
}

// presentConfirm shows one card on the command loop and blocks until it ends:
//
//   - user choice / dismissal  → resolve {ID} | {Cancelled}, next card may show
//   - out-of-band finish (bridge timeout, CancelAll while shown) → hide the
//     ghost card via Done(); the reply was already delivered by the bridge
//   - engine stop              → resolve {Cancelled, Err:"shutdown"}, abort
//
// Returns false only when the engine stopped while presenting.
func (a *App) presentConfirm(engine *tui.TUI, job plugins.ConfirmJob) bool {
	var (
		results <-chan string
		handle  *tui.OverlayHandle
	)
	engine.ApplySync(func() {
		results, handle = engine.ShowConfirm(
			job.Req.Title, job.Req.Body,
			confirmOptionsFor(job.Req.Options),
			job.Req.DefaultID, job.Req.AllowCancel,
		)
	})
	stopped := engine.Stopped()
	for {
		select {
		case id := <-results:
			job.Resolve(confirmResponseFor(id))
			return true
		case <-job.Done():
			// Timeout/cancel-all resolved the wait behind our back; drop the
			// now-inert card so it cannot ghost over the conversation.
			handle.Hide()
			return true
		case <-stopped:
			handle.Hide()
			job.Resolve(plugins.ConfirmResponse{Cancelled: true, Err: "shutdown"})
			return false
		}
	}
}

// confirmOptionsFor converts bridge options into card options (plain data —
// the card never touches VM state, preserving the §4 deadlock invariant).
func confirmOptionsFor(opts []plugins.ConfirmOption) []tui.ConfirmOption {
	out := make([]tui.ConfirmOption, len(opts))
	for i, o := range opts {
		out[i] = tui.ConfirmOption{ID: o.ID, Label: o.Label, Style: o.Style}
	}
	return out
}

// confirmResponseFor maps ShowConfirm's string result onto the wire response:
// "" means dismissed, anything else is the chosen option ID.
func confirmResponseFor(id string) plugins.ConfirmResponse {
	if id == "" {
		return plugins.ConfirmResponse{Cancelled: true}
	}
	return plugins.ConfirmResponse{ID: id}
}
