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

// TestAutocompletePopupDoesNotPushScrollback is the regression test for the
// "TUI selection widget breaks scrolling/history redraw" bug.
//
// Root cause: the editor's autocomplete popup used to be APPENDED to the
// editor's base Render() output. Opening it therefore grew the base canvas
// beyond the terminal height, and the Compositor (emitFirstScroll) pushed the
// top base rows (header / chat content / editor border) into terminal
// scrollback. When the popup closed, the canvas shrank but the pushed rows
// stayed in scrollback permanently — so each open/close cycle leaked content
// into scrollback and the history redraw never restored the prior view.
//
// Fix: the popup is now a LayerOverlay (PopupRenderer) that floats above base
// content, so the base canvas height never changes and opening/closing the
// popup can never touch scrollback.
//
// This test drives the real engine + compositor byte stream through a terminal
// emulator and asserts the invariant directly.
func TestAutocompletePopupDoesNotPushScrollback(t *testing.T) {
	const (
		w = 60
		h = 10
	)
	term := &fakeTerminal{w: w, h: h}
	engine := NewTUI(term)

	// Identifiable base content above the editor so we can prove it is never
	// pushed into scrollback and is fully restored after the popup closes.
	chat := NewChatViewport()
	chat.AddSystemMessage("KEEP_VISIBLE_TOP")
	engine.AddChild(chat)

	ed := NewEditor()
	ed.SetTUI(engine)
	ed.SetFocused(true)
	// Many commands so the popup is taller than a couple of lines (the case
	// that used to overflow the screen the most).
	cmds := make([]string, 30)
	descs := map[string]string{}
	for i := range cmds {
		cmds[i] = fmt.Sprintf("/cmd%02d", i)
		descs[cmds[i]] = "desc"
	}
	ed.SetCompleter(NewCommandCompleter(cmds, descs))
	engine.AddChild(ed)
	engine.SetFocus(ed)

	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer engine.Stop()

	engine.RenderNow()

	// Sanity: base fills the terminal exactly before the popup opens, so any
	// base-render growth would overflow.
	requireBaseFitsTerm(t, engine, w, h)

	// --- Open the popup by typing "./" ---
	ed.HandleInput("/")
	engine.RenderNow()

	// While open: the popup must be an overlay and the base canvas must NOT
	// have grown (a grown base canvas is exactly what pushes scrollback).
	requirePopupIsOverlay(t, engine, w, h)
	requireScrollbackEmpty(t, term, h, w, "popup open")

	// The popup content must be visible somewhere on screen.
	if !screenContainsAny(term, h, w, "/cmd") {
		t.Errorf("popup open: completion items not visible on screen")
	}

	// --- Close the popup by deleting "/" ---
	ed.HandleInput(KeyBackspace)
	engine.RenderNow()

	requireScrollbackEmpty(t, term, h, w, "popup close")
	requireNoPopupOverlay(t, engine, w, h)
	// The base content must be fully restored on the visible screen.
	if !screenContains(term, h, w, "KEEP_VISIBLE_TOP") {
		t.Errorf("popup close: KEEP_VISIBLE_TOP not restored on screen")
	}
}

// TestAutocompletePopupScrollbackInvariant_RealTree repeats the invariant with
// the full production component tree (header + chat + editor + footer) at the
// recorded terminal size, mirroring the exact scenario from the bug recording
// at /tmp/goa-term-scroll.log. The header height is dynamic, so we assert the
// invariant (no scrollback growth across an open/close cycle) rather than any
// exact geometry.
func TestAutocompletePopupScrollbackInvariant_RealTree(t *testing.T) {
	const (
		w = 80
		h = 29
	)
	term := &fakeTerminal{w: w, h: h}
	engine := NewTUI(term)

	header := NewHeader("goa", "0.1.0-dev")
	header.SetSkills([]string{"skill1"})
	engine.AddChild(header)

	chat := NewChatViewport()
	chat.AddSystemMessage("Context loaded")
	chat.AddSystemMessage("Connected")
	engine.AddChild(chat)

	ed := NewEditor()
	ed.SetTUI(engine)
	ed.SetFocused(true)
	cmds := make([]string, 40)
	descs := map[string]string{}
	for i := range cmds {
		cmds[i] = fmt.Sprintf("/command%02d", i)
		descs[cmds[i]] = "desc"
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

	engine.RenderNow()

	// Open then close the popup a few times (the bug recording used repeated
	// "/" + backspace cycles). Scrollback must never accumulate base content.
	for cycle := 0; cycle < 3; cycle++ {
		ed.HandleInput("/")
		engine.RenderNow()
		ed.HandleInput(KeyBackspace)
		engine.RenderNow()
	}

	emu := newScreenEmulator(h, w)
	for _, wr := range term.writes {
		emu.Process(wr)
	}
	if sb := emu.Scrollback(); len(sb) != 0 {
		t.Errorf("scrollback accumulated %d line(s) across open/close cycles; want 0; scrollback=%q",
			len(sb), joinStripped(sb))
	}
}

// requireBaseFitsTerm fails if the stacked base canvas height is not exactly
// the terminal height (the precondition for the overflow scenario).
func requireBaseFitsTerm(t *testing.T, engine *TUI, w, h int) {
	t.Helper()
	scene := engine.buildScene(w, h)
	if baseH := baseCanvasHeight(scene.Layers); baseH != h {
		t.Fatalf("precondition: base canvas height=%d, want exactly %d (terminal height)", baseH, h)
	}
}

// requirePopupIsOverlay fails unless the popup is composited as a LayerOverlay
// AND the base canvas height is unchanged (the core invariant of the fix).
func requirePopupIsOverlay(t *testing.T, engine *TUI, w, h int) {
	t.Helper()
	scene := engine.buildScene(w, h)
	if baseH := baseCanvasHeight(scene.Layers); baseH != h {
		t.Errorf("popup open: base canvas grew to %d, want %d (popup must be an overlay, not base)", baseH, h)
	}
	if !hasPopupOverlay(scene.Layers) {
		t.Errorf("popup open: no LayerOverlay popup present in scene")
	}
}

// requireNoPopupOverlay fails if any popup overlay remains in the scene.
func requireNoPopupOverlay(t *testing.T, engine *TUI, w, h int) {
	t.Helper()
	scene := engine.buildScene(w, h)
	if hasPopupOverlay(scene.Layers) {
		t.Errorf("popup close: a popup overlay is still present in the scene")
	}
}

// requireScrollbackEmpty replays the whole terminal byte stream through a fresh
// emulator and fails if any scrollback has accumulated.
func requireScrollbackEmpty(t *testing.T, term *fakeTerminal, h, w int, msg string) {
	t.Helper()
	emu := newScreenEmulator(h, w)
	for _, wr := range term.writes {
		emu.Process(wr)
	}
	if sb := emu.Scrollback(); len(sb) != 0 {
		t.Errorf("%s: %d line(s) in scrollback; want 0; scrollback=%q", msg, len(sb), joinStripped(sb))
	}
}

// hasPopupOverlay reports whether any LayerOverlay named "popup" is present.
func hasPopupOverlay(layers []Layer) bool {
	for _, l := range layers {
		if l.Kind == LayerOverlay && l.Name == "popup" {
			return true
		}
	}
	return false
}

func joinStripped(lines []string) string {
	var b strings.Builder
	for i, l := range lines {
		if i > 0 {
			b.WriteString(" | ")
		}
		b.WriteString(strings.TrimSpace(ansi.Strip(l)))
	}
	return b.String()
}

// replayScreen rebuilds the current visible screen from the terminal stream.
func replayScreen(term *fakeTerminal, h, w int) *screenEmulator {
	emu := newScreenEmulator(h, w)
	for _, wr := range term.writes {
		emu.Process(wr)
	}
	return emu
}

func screenContains(term *fakeTerminal, h, w int, needle string) bool {
	emu := replayScreen(term, h, w)
	for r := 0; r < h; r++ {
		if strings.Contains(ansi.Strip(emu.Visible(r)), needle) {
			return true
		}
	}
	return false
}

func screenContainsAny(term *fakeTerminal, h, w int, needles ...string) bool {
	emu := replayScreen(term, h, w)
	for r := 0; r < h; r++ {
		row := ansi.Strip(emu.Visible(r))
		for _, n := range needles {
			if strings.Contains(row, n) {
				return true
			}
		}
	}
	return false
}
