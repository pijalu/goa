// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"strings"
	"testing"
)

// TestCompositor_ScrolledOffToolTogglePreservesScrollback is the byte-level
// filmstrip regression for bugs.md "Out-of-screen tool call results corrupt
// the terminal UI": a completed tool widget whose rows are committed to
// terminal scrollback must be IMMUNE to expansion — both the per-widget
// Enter/Ctrl+O and the global Ctrl+O toggle-all. Expanding it would change
// its line count, shifting every later entry's repaint geometry so the
// compositor's diffs no longer match the physical screen (the corruption in
// the bug report). The test replays the raw byte stream through the screen
// emulator and asserts the committed scrollback is byte-identical after the
// toggles and the visible chrome (editor row) stays put.
func TestCompositor_ScrolledOffToolTogglePreservesScrollback(t *testing.T) {
	term, engine, chat, victim := scrolledOffToggleScene(t)
	emu := newScreenEmulator(term.h, term.w)
	for _, w := range term.writes {
		emu.Process(w)
	}
	scrollbackBefore := append([]string(nil), emu.Scrollback()...)
	visibleBefore := dumpScreen(emu, term.h)
	writesBefore := len(term.writes)

	// The bug's triggers: per-widget toggle on the scrolled-off widget and
	// the global Ctrl+O toggle-all.
	victim.HandleInput("ctrl+o")
	chat.ToggleAllToolsView()
	engine.RenderNow()

	// Replay the post-toggle writes onto the same emulator state.
	for _, w := range term.writes[writesBefore:] {
		emu.Process(w)
	}

	assertScrollbackImmutable(t, scrollbackBefore, emu.Scrollback())
	assertToggleScreenIntact(t, emu, term.h, visibleBefore)
	// And the victim widget kept its committed (collapsed) height.
	if victim.effectiveExpanded() && victim.expandedSet {
		t.Error("scrolled-off widget recorded an explicit expand — guard bypassed")
	}
}

// scrolledOffToggleScene builds an 80x15 engine with a completed long-output
// bash tool followed by enough trailing content to push the tool's rows into
// terminal scrollback, renders once, and returns the pieces under test.
func scrolledOffToggleScene(t *testing.T) (*fakeTerminal, *TUI, *ChatViewport, *ToolExecutionComponent) {
	t.Helper()
	term := &fakeTerminal{w: 80, h: 15}
	engine := NewTUI(term)
	chat := NewChatViewport()
	inp := NewEditor()
	engine.AddChild(chat)
	engine.AddChild(inp)
	engine.SetFocus(inp)
	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(engine.Stop)

	victim := chat.AddToolExecution("bash", `{"command":"go test ./..."}`)
	var longOut strings.Builder
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&longOut, "=== test log line %d\n", i)
	}
	victim.SetOutput(longOut.String())
	victim.SetStatus(ToolSuccess)
	for i := 0; i < 30; i++ {
		chat.AddSystemMessage(fmt.Sprintf("TRAILING-%02d", i))
	}
	engine.RenderNow()

	if !chat.IsScrolledOff(victim) {
		t.Fatalf("scenario precondition unmet: victim widget is not scrolled off (chat height too small for %dx%d)", term.w, term.h)
	}
	return term, engine, chat, victim
}

// assertScrollbackImmutable asserts every row committed before the toggles is
// byte-identical after them. Appending the flash notice pushes MORE rows into
// scrollback (legitimate growth at the end); corruption would show as changed
// earlier rows (a rebuilt, taller victim widget shifting everything below).
func assertScrollbackImmutable(t *testing.T, before, after []string) {
	t.Helper()
	if len(after) < len(before) {
		t.Fatalf("scrollback shrank (%d → %d rows) — committed rows lost", len(before), len(after))
	}
	for i, row := range before {
		if after[i] != row {
			t.Errorf("committed scrollback row %d changed by the scrolled-off toggles\nbefore: %q\nafter:  %q", i, row, after[i])
		}
	}
}

// assertToggleScreenIntact asserts the visible layout kept its shape after
// the toggles: the last trailing entry is still on screen (no repaint
// misalignment) and the user-visible notice explains the no-op.
func assertToggleScreenIntact(t *testing.T, emu *screenEmulator, h int, visibleBefore string) {
	t.Helper()
	if !visibleContains(emu, h, "TRAILING-29") {
		t.Errorf("last trailing entry not visible after toggles — layout shifted\nbefore:\n%s\nafter:\n%s", visibleBefore, dumpScreen(emu, h))
	}
	if !visibleContains(emu, h, "scrolled off-screen") {
		t.Errorf("no user-visible notice after the blocked toggles; screen:\n%s", dumpScreen(emu, h))
	}
}
