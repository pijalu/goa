// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

// Off-screen tool resync (bugs.md: "tool execution scrolled out of view stops
// updating"). Rows committed to terminal scrollback can never be repainted —
// the compositor clamps the window at its watermark — so a running tool whose
// widget scrolled fully off-screen would leave a frozen "elapsed Ns" in
// scrollback forever, and its completion would never write the true final
// duration there. A terminal cannot rewrite scrollback rows in place, and a
// per-tick full reset is the CPU storm the compositor explicitly rejects, so
// the viewport requests ONE scrollback resync (wipe + re-emit from the canvas)
// at each boundary:
//
//   - going off-screen while still live (next invalidate: streamed progress),
//   - completing while off-screen (status → terminal).
//
// The sync.Map guard keeps it once per episode; the completion transition
// re-arms the guard (a fresh boundary). All hooks run on the command loop,
// like every other closure the viewport installs on its widgets.

// SetScrollbackResyncRequest installs the resync sink (normally the
// compositor's RequestScrollbackResync, wired by TUI.Start before the loops
// run). nil (or never called) disables the boundary resyncs — e.g. isolated
// viewport tests.
func (cv *ChatViewport) SetScrollbackResyncRequest(fn func()) {
	cv.scrollbackResync = fn
}

// maybeResyncOffscreenTool fires the one-time scrollback resync when tc's
// rows are fully committed to scrollback and this episode has not requested
// one yet. Called from the widget's invalidate/status hooks.
func (cv *ChatViewport) maybeResyncOffscreenTool(tc *ToolExecutionComponent) {
	if cv.scrollbackResync == nil || !cv.IsScrolledOff(tc) {
		return
	}
	if _, already := cv.offscreenResynced.LoadOrStore(tc, true); already {
		return // once per off-screen episode: never a per-tick reset
	}
	cv.scrollbackResync()
}

// rearmOffscreenResync allows one further resync for tc — used at the
// non-terminal → terminal status transition, which is a new boundary: the
// completion must rewrite the stale running rows with the final "Took" line.
func (cv *ChatViewport) rearmOffscreenResync(tc *ToolExecutionComponent) {
	cv.offscreenResynced.Delete(tc)
}
