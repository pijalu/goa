// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

// TestAutocompletePopupOccludesTranscript_NoInterleave is the regression test
// for the "terminal corruption when running /quota then /skill" bug
// (bugs.md 2026-08-27): with the transcript viewport filled by tall command
// output (the /quota tables) reaching the rows where the autocomplete popup
// floats, opening the popup interleaved popup text and table text on the SAME
// screen rows (e.g. "──┌────…", "› /sID │ Title │ …"), producing unreadable
// garbage.
//
// The popup is a LayerOverlay; on the composed canvas its rows fully replace
// the transcript rows it covers (popup lines are padded to full width), so any
// on-screen interleave must come from the EMISSION step leaving stale cells
// under the popup. This test drives the real engine + compositor byte stream
// through a terminal emulator and asserts the popup rows are pure popup.
func TestAutocompletePopupOccludesTranscript_NoInterleave(t *testing.T) {
	const (
		w = 120 // wide enough that table rows extend well past the popup width
		h = 24
	)
	term := &fakeTerminal{w: w, h: h}
	engine := NewTUI(term)

	chat := NewChatViewport()
	engine.AddChild(chat)

	ed := NewEditor()
	ed.SetTUI(engine)
	ed.SetFocused(true)
	// Commands whose rows mirror the real popup ("/sk" → /skill …).
	cmds := []string{"/skill", "/skill:run", "/skill:run:dream", "/skill:run:telegram", "/skill:show", "/skill:show:dream"}
	descs := map[string]string{}
	for _, c := range cmds {
		descs[c] = "desc"
	}
	ed.SetCompleter(NewCommandCompleter(cmds, descs))
	engine.AddChild(ed)
	engine.SetFocus(ed)

	footer := NewFooter()
	footer.SetData(FooterData{Workdir: "/test", Mode: "yolo", Profile: "coder", Model: "m"})
	engine.AddChild(footer)

	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer engine.Stop()

	// Fill the transcript with full-width table rows so it overflows the
	// viewport and the bottom rows sit exactly where the popup will float.
	// The row marker "│ TBLROWnn … │" is full-width (w columns), like the
	// /quota table — so a stale (un-repainted) cell is immediately visible as
	// table text surviving inside a popup row.
	for i := 0; i < 40; i++ {
		mid := strings.Repeat("x", w-16)
		chat.AddSystemMessage(fmt.Sprintf("TBLROW%02d %s", i, mid))
		engine.RenderNow()
	}

	// Open the autocomplete popup over the table.
	ed.HandleInput("/")
	ed.HandleInput("s")
	engine.RenderNow()

	// Replay the byte stream and inspect the popup rows: any row showing a
	// popup suggestion must NOT also carry table text (cell-level interleave).
	emu := replayScreen(term, h, w)
	for r := 0; r < h; r++ {
		row := ansi.Strip(emu.Visible(r))
		hasPopup := strings.Contains(row, "/skill")
		hasTable := strings.Contains(row, "TBLROW")
		if hasPopup && hasTable {
			t.Fatalf("row %d interleaves popup and transcript content:\n%s", r, row)
		}
	}

	// Sanity: the popup must actually be on screen (the test is vacuous
	// otherwise).
	if !screenContainsAny(term, h, w, "/skill") {
		t.Fatal("popup open: no /skill suggestion visible on screen")
	}
}

// TestAutocompletePopupClose_RestoresTranscriptRows is the close-half of the
// same bug: when the popup closes, the transcript rows it covered must be
// repainted exactly once — fully restored, no leftover popup cells, no
// duplicated or lost rows.
func TestAutocompletePopupClose_RestoresTranscriptRows(t *testing.T) {
	const (
		w = 120
		h = 24
	)
	term := &fakeTerminal{w: w, h: h}
	engine := NewTUI(term)

	chat := NewChatViewport()
	engine.AddChild(chat)

	ed := NewEditor()
	ed.SetTUI(engine)
	ed.SetFocused(true)
	cmds := []string{"/skill", "/skill:run", "/skill:show"}
	descs := map[string]string{}
	for _, c := range cmds {
		descs[c] = "desc"
	}
	ed.SetCompleter(NewCommandCompleter(cmds, descs))
	engine.AddChild(ed)
	engine.SetFocus(ed)

	footer := NewFooter()
	footer.SetData(FooterData{Workdir: "/test", Mode: "yolo", Profile: "coder", Model: "m"})
	engine.AddChild(footer)

	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer engine.Stop()

	// Bottom-anchored transcript whose last rows carry unique markers.
	for i := 0; i < 40; i++ {
		chat.AddSystemMessage(fmt.Sprintf("TBLROW%02d %s", i, strings.Repeat("x", w-16)))
		engine.RenderNow()
	}

	// Open then fully clear the popup.
	ed.HandleInput("/")
	engine.RenderNow()
	if !screenContainsAny(term, h, w, "/skill") {
		t.Fatal("popup open: no /skill suggestion visible")
	}
	ed.HandleInput(KeyBackspace)
	engine.RenderNow()

	// After close, no screen row may retain popup text, and the deepest
	// transcript marker must be back on screen exactly once.
	emu := replayScreen(term, h, w)
	last := "TBLROW39"
	lastCount := 0
	for r := 0; r < h; r++ {
		row := ansi.Strip(emu.Visible(r))
		if strings.Contains(row, "/skill:run") {
			t.Fatalf("row %d still shows popup content after close:\n%s", r, row)
		}
		if strings.Contains(row, last) {
			lastCount++
		}
	}
	if lastCount != 1 {
		t.Fatalf("%s appears %d times on screen after popup close, want exactly 1", last, lastCount)
	}
}
