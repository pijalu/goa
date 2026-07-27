// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

// TestCompositor_ToolWidgetLastRowKeepsStatusBg reproduces bugs.md Issue 20:
// a tool widget rendered as the LAST transcript content (directly above the
// spinner/chrome) whose final line loses its status background. The widget's
// own renderer paints every row (including the blank bottom pad) with the
// status background, so the loss must be introduced by the compositor's
// scroll/repaint path. This test drives a real ToolExecutionComponent through
// the real Compositor — pending frame, then success+duration frame (which
// grows the canvas by one row and scrolls) — and asserts every cell of the
// widget's last row carries the success background.
func TestCompositor_ToolWidgetLastRowKeepsStatusBg(t *testing.T) {
	const w, termH = 40, 10
	term := &fakeTerminal{w: w, h: termH}
	comp := NewCompositor(term)

	tc := NewToolExecution("read", `{"path":"x.go"}`)
	tc.SetArgsJSON(`{"path":"x.go"}`)
	tc.SetOutput("line1\nline2")
	pendingRows := tc.Render(w)

	tc.SetStatus(ToolSuccess)
	tc.SetDuration("0.08s")
	successRows := tc.Render(w)

	// The collapsed read widget is small; the realistic completion frame is:
	// bg flip (pending -> success) PLUS follow-up content arriving in the same
	// frame (next streamed row), which grows the canvas and scrolls.
	mkCanvas := func(widgetRows []string, tail int) []string {
		var c []string
		for i := 0; i < 6; i++ {
			c = append(c, "history-"+itoaStr(i))
		}
		c = append(c, widgetRows...)
		for i := 0; i < tail; i++ {
			c = append(c, "tail-"+itoaStr(i))
		}
		c = append(c, "SPINNER") // chrome row pinned below the transcript
		return c
	}
	mkScene := func(canvas []string) *Scene {
		return &Scene{TerminalW: w, TerminalH: termH, ChromeHeight: 1,
			Layers: []Layer{{Name: "c", Kind: LayerBase, Rect: Rect{X: 0, Y: 0, W: w, H: len(canvas)}, Content: canvas}}}
	}

	comp.Render(mkScene(mkCanvas(pendingRows, 0)))
	comp.Render(mkScene(mkCanvas(successRows, 1))) // bg flip + growth -> scroll

	emu := NewTermEmulator(termH, w)
	for _, wr := range term.Writes() {
		emu.Process(wr)
	}

	canvas := mkCanvas(successRows, 1)
	widgetFirstIdx := 6
	widgetLastIdx := 6 + len(successRows) - 1 // bottom-pad row
	vt := len(canvas) - termH
	if vt < 0 {
		vt = 0
	}

	successBg := sgrBgParams(TheTheme.ColorHex("tool_success_bg"))
	if successBg == "" {
		t.Fatal("theme has no tool_success_bg")
	}

	for idx := widgetFirstIdx; idx <= widgetLastIdx; idx++ {
		screenRow := idx - vt
		if screenRow < 0 || screenRow >= termH {
			continue // scrolled off screen
		}
		bg := emu.VisibleBg(screenRow)
		for col, b := range bg {
			if b != successBg {
				t.Errorf("widget row %d (screen %d) col %d bg = %q, want %q (text %q)",
					idx, screenRow, col, b, successBg, emu.Visible(screenRow))
			}
		}
	}
}

// sgrBgParams converts a hex color to the SGR params the emulator tracks
// (e.g. "#2a3229" -> "48;2;42;50;41"), matching ansi.Bg output.
func sgrBgParams(hex string) string {
	r, g, b := ansi.HexToRGB(hex)
	return "48;2;" + itoaStr(int(r)) + ";" + itoaStr(int(g)) + ";" + itoaStr(int(b))
}
