// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"strings"
	"testing"
)

// chromeScene builds a transcript+chrome scene: a base transcript layer of
// transcriptRows rows and a chrome band of chromeRows rows pinned below it
// (Scene.ChromeHeight), mimicking chat + status/input/footer.
func chromeScene(transcriptRows, chromeRows, w, h int, chromeTag string) *Scene {
	transcript := make([]string, transcriptRows)
	for i := range transcript {
		transcript[i] = fmt.Sprintf("row %04d", i)
	}
	chrome := make([]string, chromeRows)
	for i := range chrome {
		chrome[i] = fmt.Sprintf("chrome-%s-%d", chromeTag, i)
	}
	return &Scene{
		TerminalW: w, TerminalH: h,
		ChromeHeight: chromeRows,
		Layers: []Layer{
			{Name: "chat", Kind: LayerBase, Rect: Rect{X: 0, Y: 0, W: w, H: transcriptRows}, Content: transcript},
			{Name: "chrome", Kind: LayerBase, Rect: Rect{X: 0, Y: transcriptRows, W: w, H: chromeRows}, Content: chrome},
		},
	}
}

// TestCompositor_ChromeGrowKeepsScrollback pins "Slow performance on
// very large conversations": a bottom-chrome height GROWTH (the editor
// gaining a row on newline, a goal/steering bubble appearing) must be an
// incremental frame — exactly one transcript row scrolls off and the window
// repaints in place. Before the fix this was a geometry reset: scrollback
// wipe (\x1b[3J) + re-emit of the ENTIRE transcript — O(history) terminal I/O
// per keystroke, the 100%-CPU-for-seconds on each newline.
func TestCompositor_ChromeGrowKeepsScrollback(t *testing.T) {
	term := &fakeTerminal{w: 40, h: 12}
	comp := NewCompositor(term)

	comp.Render(chromeScene(200, 2, 40, 12, "a")) // first frame populates scrollback
	before := len(term.Writes())

	// Editor newline: chrome band grows 2 -> 3 rows.
	comp.Render(chromeScene(200, 3, 40, 12, "a"))
	writes := term.Writes()
	if len(writes) <= before {
		t.Fatal("no frame emitted for chrome growth")
	}
	frame := strings.Join(writes[before:], "")

	if strings.Contains(frame, "\x1b[3J") {
		t.Errorf("chrome growth wiped scrollback (\\x1b[3J) — the O(history) reset: %q", frame[:200])
	}
	if strings.Contains(frame, "\x1b[2J") {
		t.Errorf("chrome growth blanked the screen (\\x1b[2J)")
	}
	// Row writes must be O(viewport), not O(history): a full re-emit writes
	// all 200 transcript rows; an incremental frame writes the window plus
	// the one scrolled-off row.
	if n := strings.Count(frame, "\r\x1b[2K"); n > 30 {
		t.Errorf("chrome growth wrote %d rows — expected an incremental frame (<= ~14), not a 200-row re-emit", n)
	}
}

// TestCompositor_ChromeShrinkKeepsScrollback covers the symmetric case: the
// bubble clearing (chrome -1) must also stay incremental.
func TestCompositor_ChromeShrinkKeepsScrollback(t *testing.T) {
	term := &fakeTerminal{w: 40, h: 12}
	comp := NewCompositor(term)

	comp.Render(chromeScene(200, 3, 40, 12, "a"))
	before := len(term.Writes())
	comp.Render(chromeScene(200, 2, 40, 12, "a"))
	writes := term.Writes()
	if len(writes) <= before {
		t.Fatal("no frame emitted for chrome shrink")
	}
	frame := strings.Join(writes[before:], "")
	if strings.Contains(frame, "\x1b[3J") || strings.Contains(frame, "\x1b[2J") {
		t.Errorf("chrome shrink took the reset path: %q", frame[:200])
	}
	if n := strings.Count(frame, "\r\x1b[2K"); n > 30 {
		t.Errorf("chrome shrink wrote %d rows — expected an incremental frame", n)
	}
}

// TestCompositor_ChromeResizeIntegrity is the correctness gate for the
// incremental chrome-change path: replay every emitted byte through the
// faithful terminal emulator across chrome grow/shrink cycles (with and
// without concurrent transcript growth — the /quota-mid-stream case) and
// assert no transcript row is lost or duplicated and the chrome band ends
// correct on screen.
func chromeIntegrityFrame(t *testing.T, steps [][2]int) {
	t.Helper()
	term := &fakeTerminal{w: 40, h: 12}
	comp := NewCompositor(term)
	for _, step := range steps {
		comp.Render(chromeScene(step[0], step[1], 40, 12, "a"))
	}
	emu := NewTermEmulator(term.h, term.w)
	for _, write := range term.Writes() {
		emu.Process(write)
	}
	visible := make([]string, term.h)
	for row := range visible {
		visible[row] = emu.Visible(row)
	}
	scrollback := emu.Scrollback()
	joined := "\n" + strings.Join(append(append([]string{}, scrollback...), visible...), "\n") + "\n"
	sbJoined := "\n" + strings.Join(scrollback, "\n") + "\n"
	last := steps[len(steps)-1]
	for i := 0; i < last[0]; i++ {
		marker := fmt.Sprintf("row %04d", i)
		if !strings.Contains(joined, marker) {
			t.Fatalf("row %q LOST\n--- screen ---\n%s\n--- scrollback tail ---\n%s", marker, strings.Join(visible, "\n"), tailLines(scrollback, 12))
		}
		if n := strings.Count(sbJoined, marker); n > 1 {
			t.Fatalf("row %q duplicated WITHIN scrollback (%d times)", marker, n)
		}
	}
	visibleText := strings.Join(visible, "\n")
	for i := 0; i < last[1]; i++ {
		marker := fmt.Sprintf("chrome-a-%d", i)
		if !strings.Contains(visibleText, marker) {
			t.Errorf("chrome row %q missing from screen:\n%s", marker, visibleText)
		}
	}
}

func TestCompositor_ChromeResizeIntegrity(t *testing.T) {
	for _, tc := range []struct {
		name  string
		steps [][2]int // {transcriptRows, chromeRows} per frame
	}{
		{"grow only", [][2]int{{60, 2}, {60, 3}, {60, 4}}},
		{"shrink only", [][2]int{{60, 4}, {60, 3}, {60, 2}}},
		{"grow+stream", [][2]int{{60, 2}, {65, 3}, {72, 4}}}, // /quota mid-stream
		{"shrink+stream", [][2]int{{60, 4}, {66, 3}, {70, 2}}},
		{"oscillate", [][2]int{{60, 2}, {61, 3}, {62, 2}, {63, 3}, {64, 2}}},
	} {
		t.Run(tc.name, func(t *testing.T) { chromeIntegrityFrame(t, tc.steps) })
	}

}
