// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"strings"
	"testing"
)

// cursorChromeScene builds a Scene with `transcriptRows` transcript rows followed by
// a 2-row chrome band (editor + footer). The cursor sits on the editor row
// (canvas row transcriptRows), as the focused editor does in the real TUI.
func cursorChromeScene(w, h, transcriptRows int) *Scene {
	content := make([]string, transcriptRows)
	for i := range content {
		content[i] = strings.Repeat("x", 4) + " transcript row " + itoaStr(i)
	}
	return &Scene{
		TerminalW:    w,
		TerminalH:    h,
		ChromeHeight: 2,
		Layers: []Layer{
			{Name: "transcript", Kind: LayerBase, Rect: Rect{Y: 0, H: transcriptRows, W: w}, Content: content},
			{Name: "editor", Kind: LayerBase, Rect: Rect{Y: transcriptRows, H: 1, W: w}, Content: []string{"> input with cursor"}},
			{Name: "footer", Kind: LayerBase, Rect: Rect{Y: transcriptRows + 1, H: 1, W: w}, Content: []string{"footer"}},
		},
		Cursor: &CursorPos{Row: transcriptRows, Col: 12},
	}
}

// TestCompositor_CursorStaysOnEditorWhenWindowClamped is the regression test
// for the "input cursor one line too high / cursor jumps out of the input box
// during a tool status change" reports.
//
// The two-phase repaint pins the chrome band (editor + footer) to the screen
// bottom even when the scrollback watermark clamps the viewport top above the
// natural bottom anchor (a transient canvas shrink, e.g. a tool widget
// collapsing after `read` completes). The hardware cursor must follow the
// SAME mapping: on the editor's painted row, never on the linear
// (cursorRow - vt + 1) row above it.
func TestCompositor_CursorStaysOnEditorWhenWindowClamped(t *testing.T) {
	const w, h = 80, 24
	term := &fakeTerminal{w: w, h: h}
	comp := NewCompositor(term)

	// Frame 1: long transcript, scrolled to the bottom. scrollTop = 60-24+2.
	comp.Render(cursorChromeScene(w, h, 60))

	// Frame 2: the transcript shrinks by 3 rows (tool widget collapse). The
	// canvas is now 57 rows; the natural anchor is 57-24 = 33 while the
	// watermark is still 58, so the window top clamps at 58 — 2 rows above
	// the natural anchor. The chrome band stays pinned at screen rows 23-24.
	shrunk := cursorChromeScene(w, h, 54)
	comp.Render(shrunk)

	emu := newScreenEmulator(h, w)
	for _, wr := range term.Writes() {
		emu.Process(wr)
	}

	// The editor must be painted on screen row h-1 (0-indexed h-2): the chrome
	// band is pinned at the bottom. Where did the hardware cursor land?
	wantRow := h - 2 // 0-indexed screen row of "> input with cursor"
	if !strings.Contains(emu.screen[wantRow], "input with cursor") {
		t.Fatalf("editor not painted on the pinned chrome row %d:\n%s", wantRow, strings.Join(emu.screen, "\n"))
	}
	if emu.row != wantRow {
		t.Errorf("hardware cursor row = %d (0-indexed), want %d — on the editor's painted row; "+
			"the linear mapping placed it %d row(s) too high (the reported cursor-jump / one-line-too-high glitch)",
			emu.row, wantRow, wantRow-emu.row)
	}
	// The footer is the row below the editor.
	if !strings.Contains(emu.screen[h-1], "footer") {
		t.Errorf("footer not painted on the last screen row:\n%s", strings.Join(emu.screen, "\n"))
	}
}

// TestCompositor_CursorStaysOnEditorAcrossShrinkAndRegrow pins the transient
// sequence reported with a queued user message: widget collapse (shrink) then
// transcript growth on the following frames. The cursor must stay on the
// editor row through the whole sequence.
func TestCompositor_CursorStaysOnEditorAcrossShrinkAndRegrow(t *testing.T) {
	const w, h = 80, 24
	term := &fakeTerminal{w: w, h: h}
	comp := NewCompositor(term)

	comp.Render(cursorChromeScene(w, h, 60))
	comp.Render(cursorChromeScene(w, h, 54)) // shrink: tool widget collapses
	comp.Render(cursorChromeScene(w, h, 56)) // regrow: user message appended
	comp.Render(cursorChromeScene(w, h, 58)) // regrow

	emu := newScreenEmulator(h, w)
	for _, wr := range term.Writes() {
		emu.Process(wr)
	}
	if emu.row != h-2 {
		t.Errorf("hardware cursor row = %d, want %d (editor row) after shrink+regrow", emu.row, h-2)
	}
}

// TestCompositor_RenderDropsStaleSceneAfterClear is the regression test for
// the "/new leaves a blank screen with the cursor on line 1 until a resize"
// reports.
//
// The renderLoop can hold a Scene snapshot when /new clears the session on
// the commandLoop: chat.Clear() + compositor.Clear() land BETWEEN the snapshot
// and compositor.Render. Without protection the stale pre-clear scene consumes
// clearRequested, repaints the OLD canvas, and restores the stale scrollback
// watermark — every later frame is diffed against a baseline that no longer
// maps onto the screen (blank window, cursor clamped to row 1) until a resize
// forces a scrollback reset. Render must DROP scenes older than the last
// Clear so the wipe stays pending for the next fresh frame.
func TestCompositor_RenderDropsStaleSceneAfterClear(t *testing.T) {
	const w, h = 80, 24
	term := &fakeTerminal{w: w, h: h}
	comp := NewCompositor(term)

	// Establish a scrolled session.
	comp.Render(cursorChromeScene(w, h, 60))

	// Snapshot taken BEFORE the clear (the renderLoop's in-flight scene), then
	// /new fires: chat.Clear + compositor.Clear on the commandLoop.
	stale := cursorChromeScene(w, h, 60)
	stale.ClearGen = comp.ClearGen() // stamped at snapshot time
	comp.Clear()

	writesBefore := len(term.Writes())
	comp.Render(stale) // must be dropped: no terminal bytes, no state change

	if got := len(term.Writes()); got != writesBefore {
		t.Fatalf("stale scene was rendered (%d new writes) — it must be dropped after a Clear", got-writesBefore)
	}
	if comp.prevLines != nil {
		t.Errorf("stale scene consumed the clear: prevLines was repopulated (len %d); it must stay nil so the next frame is a first frame with the wipe", len(comp.prevLines))
	}
	if !comp.clearRequested {
		t.Errorf("clearRequested was consumed by the stale scene; the wipe must stay pending for the fresh frame")
	}
	if comp.scrollTop != 0 {
		t.Errorf("scrollTop = %d after stale render; the stale frame must not restore the old watermark", comp.scrollTop)
	}

	// The next fresh frame (new, short session) must wipe and paint fully.
	fresh := cursorChromeScene(w, h, 3)
	fresh.ClearGen = comp.ClearGen()
	comp.Render(fresh)

	emu := newScreenEmulator(h, w)
	for _, wr := range term.Writes() {
		emu.Process(wr)
	}
	if !strings.Contains(strings.Join(emu.screen, "\n"), "transcript row") {
		t.Fatalf("fresh frame after Clear did not paint the new transcript:\n%s", strings.Join(emu.screen, "\n"))
	}
	if len(emu.scrollback) != 0 {
		t.Errorf("fresh frame after Clear left %d stale scrollback rows (wipe missing):\n%s", len(emu.scrollback), strings.Join(emu.scrollback, "\n"))
	}
	if emu.row != h-2 {
		t.Errorf("hardware cursor row = %d after fresh frame, want %d (editor row)", emu.row, h-2)
	}
}

// TestTUI_ClearTranscriptNextFrameIsFresh drives the same race at the TUI
// level: a snapshot built before the clear is delivered after ClearTranscript;
// the following full render must still produce the fresh session (this is the
// frame sequence the real renderLoop produces, since ClearTranscript always
// requests a render).
func TestTUI_ClearTranscriptNextFrameIsFresh(t *testing.T) {
	const w, h = 80, 24
	term := &fakeTerminal{w: w, h: h}
	engine := NewTUI(term)
	chat := NewChatViewport()
	inp := NewEditor()
	engine.AddChild(chat)
	engine.AddChild(inp)
	engine.SetFocus(inp)
	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer engine.Stop()

	for i := 0; i < h+10; i++ {
		chat.AddSystemMessage("session line " + itoaStr(i))
	}
	engine.RenderNow()

	// The racy delivery: snapshot (stale), then /new clears chat + compositor,
	// then the stale snapshot reaches the compositor.
	stale := engine.buildSnapshot()
	chat.Clear()
	engine.ClearTranscript()
	engine.compositor.Render(stale)

	// The pending render request produces the fresh frame.
	engine.renderNow()

	emu := newScreenEmulator(h, w)
	for _, wr := range term.Writes() {
		emu.Process(wr)
	}
	if strings.Contains(strings.Join(emu.screen, "\n"), "session line") {
		t.Errorf("stale session content survived /new:\n%s", strings.Join(emu.screen, "\n"))
	}
	if len(emu.scrollback) != 0 {
		t.Errorf("stale scrollback survived /new (%d rows):\n%s", len(emu.scrollback), strings.Join(emu.scrollback, "\n"))
	}
	if emu.row != h-2 {
		t.Errorf("hardware cursor row = %d after /new, want %d (editor row) — blank screen + cursor on line 1 is the reported bug", emu.row, h-2)
	}
}
