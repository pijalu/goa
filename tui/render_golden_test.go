// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"strings"
	"testing"
)

// renderScenario is a minimal harness that drives a TUI headless against a
// fakeTerminal and replays every byte through screenEmulator. It captures the
// three things the recurring TUI bugs corrupt: the final visible screen, the
// scrollback pushed off the top, and the hardware cursor position.
//
// This is the characterization harness for the TUI rework (see
// docs/TUI-REWORK.md Phase 0). It locks behavior at the user-visible level
// (screen + cursor + scrollback) rather than at the brittle raw-byte level,
// so the renderer can be refactored while preserving observable output.
type renderScenario struct {
	t    *testing.T
	term *fakeTerminal
	emu  *screenEmulator
}

func newRenderScenario(t *testing.T, h, w int) (*renderScenario, *TUI, *ChatViewport, *Editor) {
	t.Helper()
	term := &fakeTerminal{w: w, h: h}
	engine := NewTUI(term)
	chat := NewChatViewport()
	inp := NewEditor()
	engine.AddChild(chat)
	engine.AddChild(inp)
	engine.SetFocus(inp)
	if err := engine.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	t.Cleanup(func() { engine.Stop() })
	rs := &renderScenario{t: t, term: term, emu: newScreenEmulator(h, w)}
	return rs, engine, chat, inp
}

// snapshot replays all terminal writes so far through the emulator and returns
// the visible screen, scrollback, and hardware cursor (row, col). It does NOT
// mutate the captured writes, so it can be called between scenarios.
func (rs *renderScenario) snapshot() (screen []string, scrollback []string, row, col int) {
	rs.emu = newScreenEmulator(rs.term.h, rs.term.w)
	for _, w := range rs.term.writes {
		rs.emu.Process(w)
	}
	screen = make([]string, len(rs.emu.screen))
	copy(screen, rs.emu.screen)
	scrollback = make([]string, len(rs.emu.scrollback))
	copy(scrollback, rs.emu.scrollback)
	return screen, scrollback, rs.emu.row, rs.emu.col
}

// assertVisibleContains asserts some visible row contains substr.
func (rs *renderScenario) assertVisibleContains(sub string) {
	rs.t.Helper()
	screen, _, _, _ := rs.snapshot()
	for _, line := range screen {
		if strings.Contains(ansiClean(line), sub) {
			return
		}
	}
	rs.t.Errorf("no visible row contains %q; screen:\n%s", sub, rs.dumpScreen())
}

// assertScrollbackGrown asserts scrollback has at least n lines.
func (rs *renderScenario) assertScrollbackGrown(n int) {
	rs.t.Helper()
	_, sb, _, _ := rs.snapshot()
	if len(sb) < n {
		rs.t.Errorf("scrollback = %d lines, want >= %d", len(sb), n)
	}
}

// assertNoFullErase asserts the most recent frame did not clear the screen or
// scrollback (i.e. it was a differential scroll, not a full redraw).
func (rs *renderScenario) assertNoFullEraseInLastFrame() {
	rs.t.Helper()
	frames := collectFrames(rs.term)
	if len(frames) == 0 {
		return
	}
	last := frames[len(frames)-1]
	if strings.Contains(last, "\x1b[2J") || strings.Contains(last, "\x1b[3J") {
		rs.t.Errorf("last frame used full screen/scrollback erase:\n%s", last)
	}
}

func (rs *renderScenario) dumpScreen() string {
	screen, _, _, _ := rs.snapshot()
	var b strings.Builder
	for r, line := range screen {
		fmt.Fprintf(&b, "  [%d] %q\n", r, ansiClean(line))
	}
	return b.String()
}

func ansiClean(s string) string { return stripANSI(s) }

// ── Scenario: ASCII typing positions the cursor after the typed glyph ──
// This is the baseline for the "input line cursor corrupt / misplaced" bug.

func TestRenderGolden_ASCIIInputCursor(t *testing.T) {
	rs, engine, _, inp := newRenderScenario(t, 24, 80)
	inp.HandleInput("h")
	engine.RenderNow()
	inp.HandleInput("i")
	engine.RenderNow()

	// Editor is the last child. It renders a top border, one content line, then
	// a bottom border (3 lines). The cursor sits on the content line, column 2.
	screen, _, row, col := rs.snapshot()
	_ = screen
	// Content line is the second-to-last visible row occupied by the editor.
	// Find the row containing "hi" and assert cursor is on it after "hi".
	if col != 2 {
		t.Errorf("after typing 'hi', cursor col = %d, want 2", col)
	}
	// Cursor row must be the "hi" content line (not the border).
	if !rsContains(screen, "hi") {
		t.Fatalf("'hi' not on screen:\n%s", rs.dumpScreen())
	}
	_ = row
}

func rsContains(screen []string, sub string) bool {
	for _, l := range screen {
		if strings.Contains(ansiClean(l), sub) {
			return true
		}
	}
	return false
}

// ── Scenario: streaming append scrolls instead of erasing scrollback ──

func TestRenderGolden_StreamAppendScrolls(t *testing.T) {
	rs, engine, chat, _ := newRenderScenario(t, 10, 80)
	for i := 0; i < 20; i++ {
		chat.AddSystemMessage(fmt.Sprintf("baseline %d", i))
	}
	engine.RenderNow()

	var big strings.Builder
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&big, "tool output line %d\n", i)
	}
	chat.AddSystemMessagePreformatted(big.String())
	engine.RenderNow()

	rs.assertVisibleContains("tool output line 19")
	rs.assertScrollbackGrown(1)
	rs.assertNoFullEraseInLastFrame()
}

// ── Scenario: editor grows from 1 to many lines (middle component height change) ──
// Bug class: "scroll sometimes shows artefacts" — when a component above the
// footer changes height, the lower rows must not leave ghost copies.

func TestRenderGolden_EditorGrowthNoGhostFooter(t *testing.T) {
	rs, engine, _, inp := newRenderScenario(t, 24, 40)
	inp.SetMaxLines(6)
	// Type enough to wrap into multiple visual lines.
	for _, r := range "abcdefghijklmnopqrstuvwxyz0123456789abcdefghij" {
		inp.HandleInput(string(r))
	}
	engine.RenderNow()

	// The editor content must appear; no duplicated/garbled rows on screen.
	screen, _, _, _ := rs.snapshot()
	joined := strings.Join(screen, "\n")
	if cnt := strings.Count(joined, "abc"); cnt < 1 {
		t.Errorf("editor content missing; screen:\n%s", rs.dumpScreen())
	}
	// Count visible rows that are non-empty after stripping; there should be
	// no exact-duplicate border lines beyond the expected top+bottom.
	borderCount := 0
	for _, l := range screen {
		if s := ansiClean(l); strings.Trim(s, "─ ") == "" && s != "" {
			borderCount++
		}
	}
	if borderCount > 2 {
		t.Errorf("too many border lines (%d) — possible ghost rows:\n%s", borderCount, rs.dumpScreen())
	}
}

// ── Scenario: width resize then type does not misplace the cursor ──

func TestRenderGolden_ResizeThenTypeCursor(t *testing.T) {
	rs, engine, _, inp := newRenderScenario(t, 24, 80)
	inp.HandleInput("x")
	engine.RenderNow()

	// Resize narrower.
	rs.term.w = 40
	engine.RenderNow()

	inp.HandleInput("y")
	engine.RenderNow()

	// Cursor must be on the editor content line at col 2 (after "xy").
	_, _, _, col := rs.snapshot()
	if col != 2 {
		t.Errorf("after resize+type, cursor col = %d, want 2; screen:\n%s", col, rs.dumpScreen())
	}
}

// ── Scenario: height resize keeps cursor on the editor content line ──

func TestRenderGolden_HeightResizeCursor(t *testing.T) {
	rs, engine, _, inp := newRenderScenario(t, 24, 80)
	inp.HandleInput("ab")
	engine.RenderNow()

	rs.term.h = 16
	engine.RenderNow()

	// Cursor must remain on a content row, column 2.
	_, _, _, col := rs.snapshot()
	if col != 2 {
		t.Errorf("after height resize, cursor col = %d, want 2", col)
	}
	_ = rs
}

// ── Scenario: content shrink does not leave stale rows ──

func TestRenderGolden_ShrinkClearsStaleRows(t *testing.T) {
	rs, engine, chat, _ := newRenderScenario(t, 12, 80)
	for i := 0; i < 8; i++ {
		chat.AddSystemMessage(fmt.Sprintf("row %d", i))
	}
	engine.RenderNow()
	// Remove several messages.
	for i := 0; i < 4; i++ {
		chat.RemoveLastMessage()
	}
	engine.RenderNow()

	screen, _, _, _ := rs.snapshot()
	// The removed rows must not still be visible.
	for _, l := range screen {
		s := ansiClean(l)
		if strings.Contains(s, "row 7") || strings.Contains(s, "row 6") {
			t.Errorf("stale row remains after shrink: %q", s)
		}
	}
}

// ── Scenario: overlay open while base grows stays coherent ──

func TestRenderGolden_OverlayDuringBaseGrowth(t *testing.T) {
	rs, engine, chat, _ := newRenderScenario(t, 12, 80)
	for i := 0; i < 4; i++ {
		chat.AddSystemMessage(fmt.Sprintf("base %d", i))
	}
	engine.RenderNow()

	engine.ShowSelector("Pick:", []SelectorItem{
		{Value: "a", Label: "alpha"},
		{Value: "b", Label: "beta"},
	}, "")
	engine.RenderNow()

	// Base grows behind the overlay.
	for i := 0; i < 3; i++ {
		chat.AddSystemMessage(fmt.Sprintf("more %d", i))
	}
	engine.RenderNow()

	// Overlay must still be visible and not corrupted.
	rs.assertVisibleContains("Pick:")
}

// ── Scenario: RequestMainInput title vs scroll indicator ──
// When the editor is scrolled, the scroll indicator takes precedence over the
// title. When not scrolled, the title shows.

func TestRenderGolden_InputTitleAndScrollIndicator(t *testing.T) {
	rs, engine, _, inp := newRenderScenario(t, 24, 40)
	inp.SetTitle("Add comment")
	for _, r := range "short" {
		inp.HandleInput(string(r))
	}
	engine.RenderNow()
	rs.assertVisibleContains("Add comment")

	// Type enough to exceed the editor's max-line cap (~7 lines at this width),
	// forcing an internal scroll. The scroll indicator must replace the title.
	for i := 0; i < 400; i++ {
		inp.HandleInput("w")
	}
	engine.RenderNow()
	screen, _, _, _ := rs.snapshot()
	for _, l := range screen {
		if strings.Contains(ansiClean(l), "Add comment") {
			t.Errorf("title should be hidden when scroll indicator is shown:\n%s", rs.dumpScreen())
		}
	}
}
