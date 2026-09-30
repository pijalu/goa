// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

// Blackout recovery: a canvas that collapses below its own scrollback
// watermark (the reported stall blackout).
//
// The compositor clamps the viewport top to the scrollback watermark
// (windowTop) so a row already committed to terminal scrollback is never
// repainted — repainting it duplicates the row on screen (the mascot-redraw
// bug, TestCompositor_ShrinkBelowWatermarkDoesNotRedrawScrollbackRows). That
// clamp is ground truth about the TERMINAL, and for a mid-transcript shrink it
// is also correct for the canvas: the shrunken canvas is a prefix of the old
// one, so the rows the terminal shows are still the rows the canvas names.
//
// A stream-retry retraction breaks that premise. handleStreamFailure resets the
// round and the TUI retracts the partial assistant/thinking bubble
// (handleUserOrSystemContent → RemoveLastMessageOfType on the stream_retry
// notification), which can collapse the canvas to fewer rows than the
// watermark already committed. Now the clamp yields vt >= contentEnd: the
// window contains NO transcript row, drawWindow's paint loop never runs, and
// its stale-row clear loop starts at the NEGATIVE screen row
// transcriptEnd-vt+1 — which the terminal clamps to row 1 and uses to erase
// the entire transcript region. The screen goes black with only the pinned
// chrome band, while the status line spins "Reconnecting (attempt N/M)...".
// The watermark is never lowered by a shrink, so no later frame recovers it;
// only a resize (which re-anchors the window) does.
//
// Honoring the watermark and avoiding the blackout are NOT in conflict, and
// this is where they are reconciled. Honoring the watermark means never
// repainting a COMMITTED row; recovering a settled blackout is allowed to
// re-emit them because it WIPES the scrollback first, so nothing is duplicated.
//
// The discriminator is transient vs settled. A collapse still in flux is painted
// exactly as before — a transient shrink must still clear the rows it deleted,
// or removed messages linger on screen. Only a collapse that REPEATS the same
// canvas (a stall holding a retracted bubble for its whole retry) is recovered,
// with ONE full scrollback reset.

// windowBlackedOut reports whether the watermark-clamped window would contain
// no transcript row at all: the canvas is shorter than the watermark it already
// committed. contentEnd > 0 keeps a legitimately transcript-less canvas (an
// empty conversation, chrome only) out of this path.
func (c *Compositor) windowBlackedOut(canvasLen, height int) bool {
	contentEnd := canvasLen - c.chromeH
	if contentEnd <= 0 {
		return false
	}
	return c.windowTop(canvasLen, height) >= contentEnd
}

// blackoutSettled reports whether this collapsed canvas is the SAME one seen on
// the previous frame — i.e. the collapse is not a one-frame transient.
//
// The distinction matters because the first collapsed frame is legitimately
// painted as an empty window: a transient shrink (a thinking block finalizing
// shorter, a message being removed) must still clear the rows it just deleted,
// or the removed content stays on screen
// (TestRenderGolden_ShrinkClearsStaleRows). A repeat of the identical collapsed
// canvas is different — a stream-retry retraction or a chat clear holds the
// short canvas for many frames, and painting it as an empty window every time
// is the permanent blackout the user sees until a resize. Only that settled
// case is recovered.
//
// A canvas that CHANGED since the last collapsed frame is still in flux (the
// retry is re-streaming into it), so it is not settled either.
func (c *Compositor) blackoutSettled(canvas []string) bool {
	return sameLines(c.blackoutPending, canvas)
}

// recoverBlackout re-syncs the scrollback with a canvas that has settled below
// its own watermark. Returns true — the frame is fully handled and the caller
// must return without further painting.
//
// compose(0) drops the cull floor so no row is withheld from the re-emit, and
// drawWindowResetScrollback wipes the stale scrollback and re-emits the whole
// surviving transcript, so the content is visible again instead of the screen
// staying black. The retraction that caused this is permanent, so re-emitting
// rows the terminal still holds in scrollback is correct here: they are wiped
// first, so nothing is duplicated.
func (c *Compositor) recoverBlackout(scene *Scene, canvas []string, width, height int) bool {
	c.blackoutPending = nil
	full, _ := scene.compose(0)
	c.drawWindowResetScrollback(full, scene.Cursor, width, height)
	c.prevMutationGen = scene.MutationGen
	c.prevLines = copySlice(full)
	c.prevW = width
	c.prevH = height
	return true
}

// armBlackout records a collapsed canvas so the next frame can tell a settled
// collapse (recover it) from a transient one (paint it as before).
//
// It deliberately does NOT raise scrollbackDirty: a transient shrink is already
// handled by the dip/regrow machinery, and forcing a deferred resync here would
// wipe and re-emit the whole transcript on the regrow — flashing off-screen
// content (the mascot/logo) back onto the screen
// (TestCompositor_RegrowAfterShrinkDoesNotReEmitOffScreenContent). Only a
// settled collapse earns a full reset, and recoverBlackout performs it.
func (c *Compositor) armBlackout(canvas []string) {
	c.blackoutPending = copySlice(canvas)
	c.blackoutArmed = true
}

// sameLines reports whether two canvases are byte-identical. Used to tell a
// transient collapse (the canvas moves on next frame) from a settled one (the
// same shrunken canvas persists — a stall holding a retracted bubble).
func sameLines(a, b []string) bool {
	if len(a) != len(b) || len(a) == 0 {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
