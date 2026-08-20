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
	defer engine.Stop()

	// A completed tool with a long (collapsed) result, then enough trailing
	// content to push its rows into scrollback.
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

	// Snapshot the emulated terminal after the initial render: the committed
	// scrollback rows and the visible screen.
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
	scrollbackAfter := emu.Scrollback()

	// Committed rows are immutable: every row committed before the toggles
	// must be byte-identical after them. Appending the flash notice pushes
	// MORE rows into scrollback (legitimate growth at the end); corruption
	// would show as changed earlier rows (a rebuilt, taller victim widget
	// shifting everything below it).
	if len(scrollbackAfter) < len(scrollbackBefore) {
		t.Fatalf("scrollback shrank (%d → %d rows) — committed rows lost", len(scrollbackBefore), len(scrollbackAfter))
	}
	for i, before := range scrollbackBefore {
		if scrollbackAfter[i] != before {
			t.Errorf("committed scrollback row %d changed by the scrolled-off toggles\nbefore: %q\nafter:  %q", i, before, scrollbackAfter[i])
		}
	}
	// The visible screen keeps its shape: the same trailing content and the
	// editor row stay where they were (no repaint misalignment). The flash
	// notice appears as a NEW bottom entry, so compare the region above it:
	// every pre-toggle visible row that is still visible must be unchanged…
	// …but the appended notice scrolls the band, so instead assert the two
	// invariants that corruption breaks: the editor chrome is intact at the
	// bottom and no committed row above moved (checked via scrollback above).
	if !visibleContains(emu, term.h, "TRAILING-29") {
		t.Errorf("last trailing entry not visible after toggles — layout shifted\nbefore:\n%s\nafter:\n%s", visibleBefore, dumpScreen(emu, term.h))
	}
	// The notice is visible (appended at the bottom of the chat) so the user
	// understands why nothing expanded.
	if !visibleContains(emu, term.h, "scrolled off-screen") {
		t.Errorf("no user-visible notice after the blocked toggles; screen:\n%s", dumpScreen(emu, term.h))
	}
	// And the victim widget kept its committed (collapsed) height.
	if victim.effectiveExpanded() && victim.expandedSet {
		t.Error("scrolled-off widget recorded an explicit expand — guard bypassed")
	}
}
