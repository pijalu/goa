// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

// TestBytePosForCol_GraphemeAware verifies the cursor-marker byte offset lands
// on a grapheme boundary and that its preceding width matches the target
// column. This is the core of the "input line cursor corrupt" fix: with ZWJ
// emoji the old rune-based math placed the marker mid-cluster or past the
// glyph, so the hardware cursor column disagreed with the terminal.
func TestBytePosForCol_GraphemeAware(t *testing.T) {
	cases := []struct {
		name string
		line string
		col  int
		// wantBytes is the expected byte offset of the marker insertion point.
		wantBytes int
		// wantWidth is visibleWidth of line[:wantBytes]; it must equal col
		// (or the column of the cluster boundary at/before col).
		wantWidth int
	}{
		{"ascii", "hello", 2, 2, 2},
		{"ascii end", "hi", 2, 2, 2},
		{"zwj family after", "👨‍👩‍👧", 2, len("👨‍👩‍👧"), 2},
		{"zwj family mid", "👨‍👩‍👧", 1, 0, 0},
		{"ascii then emoji", "ab👨‍👩‍👧", 2, 2, 2},
		{"ascii then emoji after", "ab👨‍👩‍👧", 4, len("ab👨‍👩‍👧"), 4},
		{"flag emoji", "🇯🇵", 2, len("🇯🇵"), 2},
		{"combining acute", "e\u0301", 1, len("e\u0301"), 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := bytePosForCol(c.line, c.col)
			if got != c.wantBytes {
				t.Errorf("bytePosForCol(%q,%d) = %d, want %d", c.line, c.col, got, c.wantBytes)
			}
			w := ansi.Width(c.line[:got])
			if w != c.wantWidth {
				t.Errorf("visibleWidth(line[:%d]) = %d, want %d", got, w, c.wantWidth)
			}
		})
	}
}

// TestAnsiWidth_GraphemeAware locks the grapheme-cluster-aware width that
// cursor placement, wrapping, and padding all rely on.
func TestAnsiWidth_GraphemeAware(t *testing.T) {
	cases := map[string]int{
		"abc":       3,
		"👨‍👩‍👧":     2, // ZWJ family = one 2-col cluster, not 6
		"🇯🇵":        2, // regional indicator flag
		"e\u0301":   1, // combining acute
		"ab👨‍👩‍👧cd": 6, // 2 + 2 + 2
		"你好":        4, // CJK wide
	}
	for s, want := range cases {
		if got := ansi.Width(s); got != want {
			t.Errorf("ansi.Width(%q) = %d, want %d", s, got, want)
		}
	}
}

// TestEditor_EmojiCursorRoundtrip drives the full editor render → cursor marker
// → extractCursorPosition → hardware column path with ZWJ emoji input and
// asserts the emitted hardware cursor column matches the rendered glyph
// column (2 for a single family emoji), which the old rune-based math got
// wrong (it emitted 6).
func TestEditor_EmojiCursorRoundtrip(t *testing.T) {
	term := &fakeTerminal{w: 80, h: 24}
	engine := NewTUI(term)
	inp := NewEditor()
	engine.AddChild(inp)
	engine.SetFocus(inp)
	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer engine.Stop()

	inp.HandleInput("👨‍👩‍👧")
	engine.RenderNow()

	// The hardware cursor column emitted must be 2 (one 2-col grapheme cluster),
	// proving the cursor sits immediately after the emoji glyph, not 4 columns
	// past it.
	_, _, _, col := (&renderScenario{t: t, term: term, emu: newScreenEmulator(24, 80)}).snapshot()
	_ = col
	// Replay and read the final cursor column from the emulator.
	emu := newScreenEmulator(24, 80)
	for _, w := range term.writes {
		emu.Process(w)
	}
	if emu.col != 2 {
		t.Errorf("after typing ZWJ family emoji, hardware cursor col = %d, want 2", emu.col)
	}
}
