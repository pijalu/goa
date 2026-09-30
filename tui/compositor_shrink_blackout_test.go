// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"
)

// TestCompositor_ShrinkBelowWatermark_Blackout reproduces the reported stall
// blackout: during a thinking stall the stream-retry path retracts the partial
// assistant/thinking bubble (handleUserOrSystemContent → RemoveLastMessageOfType
// on the stream_retry notification), collapsing the chat canvas. On a
// long, already-scrolled conversation the watermark scrollTop is far larger than
// the shrunken canvas, so windowTop clamps the viewport top to scrollTop — an
// index that no longer exists in the canvas. Every visible transcript row is then
// an out-of-range blank, the conversation disappears behind the pinned chrome
// band, and the screen shows only the bottom band (status/editor) with the
// "Reconnecting (attempt N/M)..." spinner. The frame is byte-for-byte a
// no-op-free but visually empty repaint, so nothing recovers it until a resize
// re-anchors the window.
//
// The invariant: after a shrink the visible window must still show the tail of
// the SURVIVING transcript. Terminal scrollback cannot be un-scrolled, but a
// window whose top exceeds the canvas length is not a clamp — it is a blackout.
func TestCompositor_ShrinkBelowWatermark_Blackout(t *testing.T) {
	const w, h, chromeH = 40, 10, 2 // transcript window = 8 rows
	term := &fakeTerminal{w: w, h: h}
	comp := NewCompositor(term)

	// A long conversation streamed to a steady scrolled state: the watermark
	// scrollTop is far above 0, exactly like a real session.
	rows := make([]string, 120)
	for i := range rows {
		rows[i] = "row-" + itoaStr(i)
	}
	for n := 1; n <= 120; n++ {
		comp.Render(dipScene(w, h, chromeH, rows[:n]))
	}

	// Sanity: the watermark really is deep, so the clamp is reachable.
	if comp.ScrollWatermark() < 100 {
		t.Fatalf("precondition: expected a deep scrollback watermark, got %d", comp.ScrollWatermark())
	}

	// The stall-retry collapse: the whole visible tail of the transcript is
	// retracted and replaced by the reconnect notification. The surviving
	// canvas is far shorter than the watermark.
	shrunken := []string{"row-0", "Reconnecting (attempt 1/15)..."}

	// A stall holds the same retracted canvas across many frames. It must NOT
	// stay black: the compositor recognizes the settled collapse and re-syncs
	// the scrollback, so the surviving tail becomes visible again.
	comp.Render(dipScene(w, h, chromeH, shrunken))
	comp.Render(dipScene(w, h, chromeH, shrunken))
	assertNotBlackedOut(t, term, h, chromeH, "settled collapse")

	emu := NewTermEmulator(h, w)
	for _, wr := range term.Writes() {
		emu.Process(wr)
	}
	var screen []string
	for r := 0; r < h; r++ {
		screen = append(screen, strings.TrimSpace(emu.Visible(r)))
	}
	dump := "\n--- screen ---\n" + strings.Join(screen, "\n")

	// The surviving transcript must be visible: at least one conversation row
	// AND the reconnect notification the user is actually looking at.
	visible := 0
	for _, r := range screen[:h-chromeH] {
		if strings.TrimSpace(r) != "" {
			visible++
		}
	}
	if visible == 0 {
		t.Fatalf("BLACKOUT: transcript region is entirely blank after the shrink (chrome band only)%s", dump)
	}
	if !strings.Contains(strings.Join(screen, "\n"), "Reconnecting") {
		t.Errorf("reconnect notification not visible after the shrink%s", dump)
	}
}

// TestCompositor_TransientShrinkStillClearsDeletedRows pins the OTHER half of
// the contract, the one TestRenderGolden_ShrinkClearsStaleRows owns: a
// one-frame shrink must still be painted, so the rows it deleted are erased
// from the screen. The blackout recovery must not swallow that frame — otherwise
// removed messages linger on screen.
func TestCompositor_TransientShrinkStillClearsDeletedRows(t *testing.T) {
	const w, h, chromeH = 40, 10, 2
	term := &fakeTerminal{w: w, h: h}
	comp := NewCompositor(term)

	rows := make([]string, 120)
	for i := range rows {
		rows[i] = "row-" + itoaStr(i)
	}
	for n := 1; n <= 120; n++ {
		comp.Render(dipScene(w, h, chromeH, rows[:n]))
	}

	// ONE collapsed frame, then the canvas regrows: a transient shrink, which
	// must be painted (deleting the rows it removed) rather than recovered.
	w0 := len(term.Writes())
	comp.Render(dipScene(w, h, chromeH, []string{"row-0", "row-1", "row-2"}))
	shrinkFrame := strings.Join(term.Writes()[w0:], "")
	w1 := len(term.Writes())
	comp.Render(dipScene(w, h, chromeH, rows[:120]))
	regrowFrame := strings.Join(term.Writes()[w1:], "")

	emu := NewTermEmulator(h, w)
	for _, wr := range term.Writes() {
		emu.Process(wr)
	}
	var screen []string
	for r := 0; r < h; r++ {
		screen = append(screen, strings.TrimSpace(emu.Visible(r)))
	}
	joined := strings.Join(screen, "\n")
	// The canvas is back to its full length, so its tail must be on screen.
	if !strings.Contains(joined, "row-119") {
		t.Errorf("after a transient shrink and regrow the tail is missing:\n%s", joined)
	}
	// The blackout recovery must NOT fire on the transient collapse itself: it
	// would wipe scrollback and re-emit the whole transcript mid-shrink (the
	// reset storm the compositor rejects everywhere else).
	if strings.Contains(shrinkFrame, "\x1b[3J") {
		t.Errorf("transient collapse frame wiped scrollback — the blackout recovery fired too early")
	}
	// A single deferred resync after the regrow is correct (the terminal's
	// scrollback diverged by the retracted rows); a per-frame one is the storm.
	wipes := 0
	for _, wr := range term.Writes() {
		if strings.Contains(wr, "\x1b[3J") {
			wipes++
		}
	}
	if wipes > 1 {
		t.Errorf("transient shrink caused %d scrollback wipes (reset storm); want at most 1 deferred resync", wipes)
	}
	if !strings.Contains(regrowFrame, "row-119") {
		t.Errorf("regrow frame did not repaint the tail:\n%s", regrowFrame)
	}
}

// assertNotBlackedOut replays every emitted byte and fails when the transcript
// region is entirely blank — the blackout signature. chromeH trailing rows are
// the pinned band, which is legitimately present in every state.
func assertNotBlackedOut(t *testing.T, term *fakeTerminal, h, chromeH int, when string) {
	t.Helper()
	emu := NewTermEmulator(h, term.w)
	for _, wr := range term.Writes() {
		emu.Process(wr)
	}
	visible := 0
	for r := 0; r < h-chromeH; r++ {
		if strings.TrimSpace(emu.Visible(r)) != "" {
			visible++
		}
	}
	if visible == 0 {
		t.Errorf("BLACKOUT at %s: transcript region entirely blank", when)
	}
}

// TestCompositor_ShrinkToTail_RendersSurvivingRows is the sharper form: even
// when the canvas shrinks to a tail SHORTER than one window, the compositor must
// show those rows rather than clamping the window top past the canvas end.
func TestCompositor_ShrinkToTail_RendersSurvivingRows(t *testing.T) {
	const w, h, chromeH = 40, 10, 2
	term := &fakeTerminal{w: w, h: h}
	comp := NewCompositor(term)

	rows := make([]string, 120)
	for i := range rows {
		rows[i] = "row-" + itoaStr(i)
	}
	for n := 1; n <= 120; n++ {
		comp.Render(dipScene(w, h, chromeH, rows[:n]))
	}

	// Collapse to a 3-row tail — shorter than the 8-row transcript window, so
	// the natural bottom anchor (canvasLen-height) dips below the watermark.
	tail := []string{"row-0", "Reconnecting (attempt 2/15)...", "row-119"}
	comp.Render(dipScene(w, h, chromeH, tail)) // deferred, no paint
	comp.Render(dipScene(w, h, chromeH, tail)) // settled → scrollback re-synced

	emu := NewTermEmulator(h, w)
	for _, wr := range term.Writes() {
		emu.Process(wr)
	}
	var screen []string
	for r := 0; r < h; r++ {
		screen = append(screen, strings.TrimSpace(emu.Visible(r)))
	}
	dump := "\n--- screen ---\n" + strings.Join(screen, "\n")

	joined := strings.Join(screen, "\n")
	for _, want := range tail {
		if !strings.Contains(joined, want) {
			t.Errorf("surviving transcript row %q is not on screen after the collapse%s", want, dump)
		}
	}
}
