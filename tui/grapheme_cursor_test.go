// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"testing"

	"github.com/pijalu/goa/internal/ansi"
	"github.com/rivo/uniseg"
)

// markerOnGraphemeBoundary reports whether bytePos is a grapheme-cluster
// boundary in s. It walks all clusters and returns true if the boundary is at
// the start of a cluster or at the end of the string.
func markerOnGraphemeBoundary(s string, bytePos int) bool {
	gr := uniseg.NewGraphemes(s)
	boundary := 0
	for gr.Next() {
		if boundary == bytePos {
			return true
		}
		_, end := gr.Positions()
		boundary = end
	}
	return boundary == bytePos
}

// TestCursorPlacement_GraphemeAware verifies the new cursor-marker placement
// path (wrapChunks + cursorChunk + runeOffsetToByte) lands the marker on a
// grapheme-cluster boundary with the correct preceding visible width. This is
// the core of the "input line cursor" correctness: the marker is placed at the
// cursor's rune offset within its chunk, and chunks are faithful slices of the
// source, so the hardware cursor column always matches the rendered glyph —
// for ZWJ emoji and combining marks too.
func TestCursorPlacement_GraphemeAware(t *testing.T) {
	cases := []struct {
		name      string
		text      string
		pos       int
		wantWidth int
	}{
		{"ascii mid", "hello", 2, 2},
		{"ascii end", "hi", 2, 2},
		{"zwj family after", "👨‍👩‍👧", len([]rune("👨‍👩‍👧")), 2},
		{"zwj family mid", "👨‍👩‍👧", 0, 0},
		{"ascii then emoji after", "ab👨‍👩‍👧", 2, 2},
		{"flag emoji after", "🇯🇵", len([]rune("🇯🇵")), 2},
		{"combining acute", "e\u0301", len([]rune("e\u0301")), 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			chunks := wrapChunks(c.text, 80)
			idx, off := cursorChunk(chunks, c.text, c.pos)
			bytePos := runeOffsetToByte(chunks[idx].Text, off)
			w := ansi.Width(chunks[idx].Text[:bytePos])
			if w != c.wantWidth {
				t.Errorf("visibleWidth before marker = %d, want %d (bytePos=%d, chunk=%q)",
					w, c.wantWidth, bytePos, chunks[idx].Text)
			}
			if !markerOnGraphemeBoundary(chunks[idx].Text, bytePos) {
				t.Errorf("marker byte %d splits a grapheme cluster in %q", bytePos, chunks[idx].Text)
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
