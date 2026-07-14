// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"
)

// TestStreaming_Growth_NoClobber is the regression test for the streaming
// "doesn't scroll / stacked on a couple lines" artifact reported against the
// absolute-CUP compositor.
//
// Root cause: writeDifferential wrote each changed line with absolute CUP at
// screenRow = clampRow(i-viewportTop+1, height). When a streaming message
// grows PAST the viewport bottom while its last visible line also changes
// (firstChanged inside the viewport), the viewport never advanced and every
// overflow line was clamped to the bottom screen row — clobbering each other
// so the user saw only one or two stacked lines instead of a scrolling feed.
//
// Relative \r\n moves trigger the terminal's native scroll at the bottom row,
// which implicitly bottom-anchors the viewport. The absolute-CUP path must
// anchor the viewport to the new content bottom explicitly (emit scrollback
// newlines) BEFORE the CUP write loop.
//
// This test uses the FAITHFUL termEmulator (per-cell, scrollback-aware) and
// asserts that the trailing lines of a streamed message land on DISTINCT
// screen rows — i.e. nothing is clobbered and the viewport scrolled.
func TestStreaming_Growth_NoClobber(t *testing.T) {
	const h = 10
	term := &fakeTerminal{w: 60, h: h}
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

	// Pre-fill so the viewport is already bottom-anchored below the top.
	for i := 0; i < 15; i++ {
		chat.AddSystemMessage("history line that wraps the viewport")
	}
	engine.RenderNow()

	// Stream a growing assistant message whose final form spans several lines
	// and pushes the viewport up. Each step changes the last visible line AND
	// appends new lines past the bottom — the exact clobber trigger. Use a
	// markdown ordered list so each item is its own canvas line (a soft-wrapped
	// paragraph would collapse to one wrapped line and not exercise the bug).
	chat.AddAssistantMessage("")
	steps := []string{
		"1. line alpha",
		"1. line alpha\n2. line beta",
		"1. line alpha\n2. line beta\n3. line gamma",
		"1. line alpha\n2. line beta\n3. line gamma\n4. line delta",
		"1. line alpha\n2. line beta\n3. line gamma\n4. line delta\n5. line epsilon",
	}
	for _, s := range steps {
		chat.UpdateLastMessage(s, ConsoleAssistantMessage)
		engine.RenderNow()
	}

	emu := NewTermEmulator(h, 60)
	for _, w := range term.writes {
		emu.Process(w)
	}

	// The final streamed lines must each be visible on a DISTINCT row. The
	// clobber bug wrote them all to the bottom row, so only the last survived.
	want := []string{"line alpha", "line beta", "line gamma", "line delta", "line epsilon"}
	seen := map[string]int{}
	for r := 0; r < h; r++ {
		row := emu.Visible(r)
		for _, w := range want {
			if strings.Contains(row, w) {
				seen[w]++
			}
		}
	}
	for _, w := range want {
		if seen[w] != 1 {
			t.Errorf("clobber: %q visible on %d rows (want exactly 1); screen:\n%s",
				w, seen[w], dumpTerm(emu, h))
		}
	}
}
