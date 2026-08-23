// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strconv"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/tui"
)

// Streaming color-regression detection, driven through the production event
// path (uiScenario) and asserted on the TermEmulator's per-cell styles — the
// same "what is actually on screen" evidence class as ../term.log.
//
// Reported symptoms (bugs.md):
//  1. the same bubble changes color across streaming frames,
//  2. tool call blocks lose / only partially paint their background,
//  3. text color changes when a row scrolls off-screen (grey vs white).
//
// The chrome/muted grey #8b949e is the signature leak color: it belongs to
// footer/chrome styling; assistant content must never wear it.

const chromeMutedFgHex = "#8b949e"

func fgParams(hex string) string {
	r, g, b := ansi.HexToRGB(hex)
	return "38;2;" + strconv.Itoa(int(r)) + ";" + strconv.Itoa(int(g)) + ";" + strconv.Itoa(int(b))
}

// streamColorEmu replays every byte the engine wrote so far into one shared
// emulator, mirroring what a real terminal accumulates across differential
// frames. Returns the emulator for style queries.
type streamColorEmu struct {
	emu      *tui.TermEmulator
	consumed int
	term     *testTerminal
}

func newStreamColorEmu(term *testTerminal, w, h int) *streamColorEmu {
	return &streamColorEmu{emu: tui.NewTermEmulator(h, w), term: term}
}

// sync processes any writes emitted since the last call and returns the
// emulator reflecting the CURRENT screen state.
func (s *streamColorEmu) sync() *tui.TermEmulator {
	for ; s.consumed < len(s.term.writes); s.consumed++ {
		s.emu.Process(s.term.writes[s.consumed])
	}
	return s.emu
}

// rowWith returns the first screen row whose visible text contains substr.
func rowWith(emu *tui.TermEmulator, substr string, h int) int {
	for r := 0; r < h; r++ {
		if strings.Contains(emu.Visible(r), substr) {
			return r
		}
	}
	return -1
}

// assertNotChromeGrey fails the test if any non-blank cell of the given row
// wears the chrome/muted foreground.
func assertNotChromeGrey(t *testing.T, emu *tui.TermEmulator, row int, ctx string) {
	t.Helper()
	fg := fgParams(chromeMutedFgHex)
	for col, cellFg := range emu.VisibleFg(row) {
		if cellFg == fg && strings.TrimSpace(string(runeAt(emu.Visible(row), col))) != "" {
			t.Errorf("%s: row %d col %d carries chrome grey %q (text=%q)",
				ctx, row, col, chromeMutedFgHex, strings.TrimSpace(emu.Visible(row)))
			return
		}
	}
}

// runeAt returns the rune at byte index i (best effort for assertion only).
func runeAt(s string, i int) []rune {
	return []rune(s)[min(i, len([]rune(s))-1):]
}

// TestUI_StreamBubbleKeepsOneForeground streams an answer over several frames
// and asserts the first line keeps ONE foreground while later deltas arrive
// and push it upward (bubble stability + off-screen move stability).
func TestUI_StreamBubbleKeepsOneForeground(t *testing.T) {
	sc := newUIScenario(t, 80, 24)
	sec := newStreamColorEmu(sc.term, 80, 24)

	sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateContent})
	sc.apply(contentDelta("Line one of the answer.\n"))

	emu := sec.sync()
	row := rowWith(emu, "Line one of the answer", 24)
	if row < 0 {
		t.Fatal("first streamed line not found on screen")
	}
	firstFg := emu.RowFg(row)
	if firstFg == fgParams(chromeMutedFgHex) {
		t.Errorf("streamed answer line painted with chrome grey %q at first appearance", chromeMutedFgHex)
	}

	// More deltas arrive; "Line one" must keep its foreground as the bubble
	// grows and the line moves up the viewport (and eventually toward
	// scrollback).
	for i := 2; i <= 8; i++ {
		sc.apply(contentDelta("Line " + strconv.Itoa(i) + " adds more streamed content.\n"))
		emu := sec.sync()
		row := rowWith(emu, "Line one of the answer", 24)
		if row < 0 {
			t.Fatalf("line one scrolled away unexpectedly at delta %d", i)
		}
		nowFg := emu.RowFg(row)
		if nowFg != firstFg {
			t.Fatalf("delta %d: line-one foreground changed %q -> %q (bubble recolored mid-stream)",
				i, firstFg, nowFg)
		}
		assertNotChromeGrey(t, emu, row, "bubble stability")
	}
}

// contentDelta builds one assistant content delta like the real stream does.
func contentDelta(text string) *agentic.OutputEvent {
	return &agentic.OutputEvent{
		Type:  agentic.EventContent,
		Role:  agentic.Assistant,
		State: agentic.StateContent,
		Text:  text,
	}
}

// TestUI_MarkdownTableKeepsStableStyle streams a markdown table (the exact
// repro shape from the field report) across two frames and asserts the table
// rows keep ONE foreground and never wear the chrome grey — the grey/white
// split between on-screen and scrolled rows.
func TestUI_MarkdownTableKeepsStableStyle(t *testing.T) {
	sc := newUIScenario(t, 80, 24)
	sec := newStreamColorEmu(sc.term, 80, 24)

	sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateContent})
	// Frame 1: intro + header row only.
	sc.apply(contentDelta("Intro line.\n"))
	sc.apply(contentDelta("| A | B |\n"))

	emu := sec.sync()
	headerRow := rowWith(emu, "│ A", 24)
	if headerRow < 0 {
		t.Fatalf("table header not rendered; screen=\n%s", dumpScreen(emu, 24))
	}
	headerFg := emu.RowFg(headerRow)

	// Frame 2+: complete the table with separator and data rows. The already-
	// rendered header must keep its exact style when the table re-renders as a
	// whole (same bubble = same color).
	for i, chunk := range []string{"|---|---|\n", "| one | two |\n", "After table text.\n"} {
		sc.apply(contentDelta(chunk))
		emu := sec.sync()
		row := rowWith(emu, "│ A", 24)
		if row < 0 {
			t.Fatalf("chunk %d: table header vanished from screen", i)
		}
		if nowFg := emu.RowFg(row); nowFg != headerFg {
			t.Fatalf("chunk %d: table header foreground changed %q -> %q (bubble recolored)", i, headerFg, nowFg)
		}
		assertNotChromeGrey(t, emu, row, "table stability")
	}
}

// dumpScreen renders the emulator's visible rows for failure output.
func dumpScreen(emu *tui.TermEmulator, h int) string {
	var b strings.Builder
	for r := 0; r < h; r++ {
		b.WriteString(emu.Visible(r))
		b.WriteByte('\n')
	}
	return b.String()
}

// TestUI_ToolCallBlockKeepsBackgroundWhileStreaming asserts the tool widget's
// background survives subsequent streamed frames (symptom 2).
func TestUI_ToolCallBlockKeepsBackgroundWhileStreaming(t *testing.T) {
	sc := newUIScenario(t, 80, 24)
	sec := newStreamColorEmu(sc.term, 80, 24)

	sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateToolCall})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolCall, State: agentic.StateToolCall,
		ToolCallID: "c1", ToolName: "read", ToolInput: `{"path":"x"}`})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolResult, State: agentic.StateToolResult,
		ToolCallID: "c1", ToolName: "read", Text: "ok"})

	emu := sec.sync()
	widgetRow := -1
	for r := 0; r < 24; r++ {
		if strings.Contains(emu.Visible(r), "read") {
			widgetRow = r
			break
		}
	}
	if widgetRow < 0 {
		t.Fatal("tool widget row not found after result")
	}

	// Stream an answer below the widget; the widget rows must not lose their
	// background during those repaints. A row that keeps its block background
	// carries bg params on its padded cells; a row that lost it falls back to
	// the default (empty) bg everywhere.
	for i := 1; i <= 5; i++ {
		sc.apply(contentDelta("Answer part " + strconv.Itoa(i) + ".\n"))
		emu := sec.sync()
		row := rowWith(emu, "read", 24)
		if row < 0 {
			t.Fatalf("tool block vanished at delta %d", i)
		}
		bgged := 0
		for _, bg := range emu.VisibleBg(row) {
			if bg != "" {
				bgged++
			}
		}
		if bgged == 0 && i > 1 {
			t.Fatalf("delta %d: tool widget row %d lost its background entirely", i, row)
		}
	}
}
