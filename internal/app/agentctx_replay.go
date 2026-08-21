// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"github.com/pijalu/goa/tui"
	"github.com/pijalu/goa/tui/agentctx"
)

// agentctx_replay.go wires the T3 scrollback-replay path that runs when the
// features.multi_agent_scrollback_replay gate resolves ON. It layers on top of
// the T2 mount/swap (remountAgentView): instead of letting the resume frame
// scroll the target's committed-but-unemitted rows off inline, the switch
// suppresses frame rendering, hands the target's backlog to the dedicated
// ReplayRunner goroutine, and only resumes rendering once the runner reports
// the watermark it physically reached.
//
// Invariants this preserves (plan §5 T3, §9):
//   - R1 single-owner: the runner never touches live compositor state or the
//     engine tree; it emits precomputed bytes and hands ONE watermark back per
//     run, which the command loop applies via a.apply. All mutations here run
//     inside engine.Apply.
//   - Exactly-once scrollback: each agent's saved ScrollTop always equals the
//     count of its canvas rows physically present in scrollback. A replay
//     emits [savedScrollTop, naturalVt); a partial (cancelled) run's Emitted
//     is still applied so already-physically-present rows are never re-emitted.
//   - Flag OFF keeps T2 behavior byte-for-byte: driveReplayOnSwitch returns
//     false and remountAgentView's RestoreFrame repaint runs unchanged.

// driveReplayOnSwitch performs the flag-ON switch path after remountAgentView
// has detached oldID and mounted newID. It reports whether a replay was
// scheduled (true ⇒ the resume frame is deferred to the replay's completion;
// false ⇒ the caller must fall back to the T2 RestoreFrame repaint).
//
// Must be called on the command loop (inside engine.Apply).
func (a *App) driveReplayOnSwitch(eng *tui.TUI, newID string, nextView *agentctx.AgentView) bool {
	if !a.replayEnabled() {
		return false
	}
	runner := a.subs.replayRunner
	if runner == nil {
		return false
	}
	// Capture the mounted target's full canvas + geometry on the command loop
	// so the emitted bytes match what the resume frame will paint.
	canvas, naturalVt, _, width, height := eng.ReplaySnapshot()
	from := nextView.Compositor.Snapshot().ScrollTop
	if from >= naturalVt {
		// Nothing accumulated beyond what is already physically in scrollback:
		// no emission needed. Take the T2 repaint so the window repaints in
		// place without the runner.
		return false
	}
	// Suppress frame rendering BEFORE the runner can emit and before any
	// post-switch snapshot is consumed: a frame between the swap and the
	// replay-end would emit the same backlog rows the runner owns (the
	// double-emission race). Set within this Apply so it is atomic with the
	// pointer move. The completion handler clears it and requests the resume.
	eng.SetReplaySuppressed(true)
	runner.Submit(agentctx.ReplayRequest{
		AgentID: newID,
		Canvas:  canvas,
		From:    from,
		To:      naturalVt,
		Width:   width,
		Height:  height,
	})
	return true
}

// applyReplayResult applies one runner result on the command loop. It advances
// the view's saved watermark to the row the runner physically reached, sets
// the live watermark so the resume frame never re-emits those rows, clears the
// suppression, and arms the resume frame (a frameFirst in-place repaint of the
// visible window — the runner only emitted the scrollback backlog).
//
// Safe to call for a cancelled/failed run: res.Emitted is still the count of
// rows that reached scrollback, so the watermark advances by exactly that and
// no row is ever emitted twice. Runs on the command loop via a.apply.
func (a *App) applyReplayResult(res agentctx.ReplayResult) {
	eng := a.subs.tuiEngine
	if eng == nil {
		return
	}
	// Advance the view's saved watermark to the physically-emitted row. The
	// rest of the saved baseline (PrevLines, VT) is preserved.
	if view, ok := a.subs.agentRegistry.Get(res.AgentID); ok && view != nil {
		snap := view.Compositor.Snapshot()
		if res.Emitted > snap.ScrollTop {
			snap.ScrollTop = res.Emitted
			view.Compositor.Save(snap)
		}
	}
	// Anchor the live window at the emitted watermark: the resume frame paints
	// [Emitted, Emitted+windowH) and treats rows below Emitted as committed.
	// RestoreFrame drops prevLines (forces the full repaint) and bumps clearGen
	// so any snapshot taken mid-replay is dropped rather than diffed.
	eng.RestoreFrame(tui.FrameState{ScrollTop: res.Emitted, VT: res.Emitted})
	eng.SetReplaySuppressed(false)
	eng.RequestRender()
}

// replayEnabled reports whether the scrollback-replay gate resolves ON.
func (a *App) replayEnabled() bool {
	return a.subs.cfg != nil && a.subs.cfg.Features.MultiAgentScrollbackReplayEnabled()
}

// runReplayResultReader drains the runner's results channel, applying each
// watermark on the command loop until shutdown. Mirrors the other event
// readers in events.go.
func (a *App) runReplayResultReader(done chan struct{}, runner *agentctx.ReplayRunner) {
	results := runner.Results()
	for {
		select {
		case <-done:
			return
		case res, ok := <-results:
			if !ok {
				return
			}
			r := res
			a.apply(func() { a.applyReplayResult(r) })
		}
	}
}
