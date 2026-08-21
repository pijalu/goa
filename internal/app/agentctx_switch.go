// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"github.com/pijalu/goa/tui"
	"github.com/pijalu/goa/tui/agentctx"
)

// agentctx_switch.go wires the T2 view switch: moving the registry's active
// pointer is pure bookkeeping, so the mechanical part lives here — save the
// outgoing view's compositor baseline, unmount its transcript, mount the
// target's at the SAME tree position, restore the target's baseline, and arm
// a full visible-window repaint (no scrollback replay — that is T3).
//
// R1 (single-owner): every step runs inside one engine.Apply closure on the
// command loop, so the registry, the transcripts' mount flags, and the engine
// children change atomically with respect to scene snapshots.

// switchAgentView makes id the active agent view. It reports whether the
// switch happened (false: no registry/engine yet, or unknown id). Selecting
// the already-active view succeeds as a no-op.
func (a *App) switchAgentView(id string) bool {
	reg, eng := a.subs.agentRegistry, a.subs.tuiEngine
	if reg == nil || eng == nil {
		return false
	}
	switched := false
	eng.Apply(func() {
		oldID, oldView := reg.Active()
		nextView, ok := reg.SelectByID(id)
		if !ok {
			return
		}
		a.remountAgentView(eng, oldID, oldView, id, nextView)
		switched = true
	})
	return switched
}

// cycleAgentView advances the active view by dir steps (sign matters) through
// the tab order, wrapping. No-op with fewer than two views.
func (a *App) cycleAgentView(dir int) {
	reg, eng := a.subs.agentRegistry, a.subs.tuiEngine
	if reg == nil || eng == nil {
		return
	}
	eng.Apply(func() {
		oldID, oldView := reg.Active()
		newID, nextView := reg.Cycle(dir)
		a.remountAgentView(eng, oldID, oldView, newID, nextView)
	})
}

// remountAgentView performs the mount-side of a view switch once the registry
// pointer has moved from oldID to newID. Must be called on the command loop
// (inside engine.Apply) after the pointer move.
func (a *App) remountAgentView(eng *tui.TUI, oldID string, oldView *agentctx.AgentView, newID string, nextView *agentctx.AgentView) {
	if oldID == newID || oldView == nil || nextView == nil {
		return
	}
	// Save the outgoing view's exact frame before detaching it.
	oldView.Compositor.Save(eng.ExportFrame())
	oldView.Transcript.Unmount()
	nextView.Transcript.Mount()
	// Swap the mounted ChatViewport in place so the layout (header →
	// transcript → … → input → footer) is unchanged.
	if !eng.ReplaceChild(oldView.Transcript.View(), nextView.Transcript.View()) {
		// The outgoing view was not in the tree (defensive: should not happen
		// — the active view is always mounted). Fall back to appending so the
		// new view is at least visible.
		eng.AddChild(nextView.Transcript.View())
	}
	// Restore the target's baseline and arm the full window repaint. The
	// repaint is in-place (per-row erase + rewrite), which is the
	// compositor's region-scoped clear: committed scrollback is untouched and
	// never re-emitted (T2; replay lands in T3).
	eng.RestoreFrame(nextView.Compositor.Snapshot())
	// Keep the app's main-chat binding honest: subs.chat must point at the
	// MAIN agent's viewport regardless of which tab is visible, so main-agent
	// events keep landing in their own transcript (routing is per-agent, T4).
	if mainView, ok := a.subs.agentRegistry.Get(agentctx.MainAgentID); ok {
		a.subs.chat = mainView.Transcript.View()
	}
}

