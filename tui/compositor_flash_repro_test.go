// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"strings"
	"testing"
)

// TestFlashRepro_FirstScrollDoesNotPaintMascotAtBottom is the regression test
// for the "logo/mascot flashes across the screen during the first scroll"
// report.
//
// On the first viewport advance the compositor must populate scrollback. The
// old bottom-anchored first-scroll write painted the whole canvas starting at
// the BOTTOM row, so the off-screen-top header/mascot was written at the
// bottom and rolled UP through the entire visible screen — a very visible
// flash of the mascot.
//
// The fix re-writes the canvas TOP-DOWN from row 1: the header is rewritten in
// place (identical content) and scrolls off the top naturally. This test
// asserts the first-scroll write positions the header at the TOP (a row-1 CUP
// precedes the mascot content), never at the bottom row.
func TestFlashRepro_FirstScrollDoesNotPaintMascotAtBottom(t *testing.T) {
	const w, h = 100, 24
	term := &fakeTerminal{w: w, h: h}
	engine := NewTUI(term)
	header := NewHeader("goa", "test")
	chat := NewChatViewport()
	status := NewStatusMsg()
	inp := NewEditor()
	footer := NewFooter()

	engine.AddChild(header)
	engine.AddChild(chat)
	engine.AddChild(status)
	engine.AddChild(inp)
	engine.AddChild(footer)
	engine.SetFocus(inp)
	status.SetTUI(engine)
	inp.SetTUI(engine)

	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer engine.Stop()

	const marker = "coding agent"
	engine.RenderNow()
	writesAtBaseline := len(term.writes)

	// Stream past the screen so the first viewport scroll fires.
	for i := 0; i < h+5; i++ {
		chat.AddSystemMessage("stream line " + itoaStr(i))
		engine.RenderNow()
	}

	// Locate every post-baseline write that contains the header marker. These
	// are the first-scroll writes (later scrolls never re-emit the header).
	for i := writesAtBaseline; i < len(term.writes); i++ {
		wr := term.writes[i]
		idx := strings.Index(wr, marker)
		if idx < 0 {
			continue
		}
		preceding := wr[:idx]
		lastCUP := lastCursorRow(preceding, h)
		if lastCUP == h {
			t.Errorf("write[%d] painted the header/mascot at the BOTTOM row %d (the flash); "+
				"first-scroll must write top-down from row 1:\n%s",
				i, h, truncEscape(wr))
		}
	}

	// Sanity: the header must indeed have scrolled off the visible screen.
	emu := newScreenEmulator(h, w)
	for _, wr := range term.writes {
		emu.Process(wr)
	}
	if visibleContains(emu, h, marker) {
		t.Errorf("header marker should be scrolled off screen\n%s", dumpEmu(emu, h))
	}
}

// lastCursorRow returns the terminal row targeted by the most recent CUP
// sequence in s, defaulting to 1 when none is found. It understands the
// "ESC[<row>;<col>H" form used by the compositor.
func lastCursorRow(s string, height int) int {
	row := 1
	for i := 0; i < len(s); i++ {
		if s[i] != '\x1b' || i+1 >= len(s) || s[i+1] != '[' {
			continue
		}
		j := i + 2
		numStart := j
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j > numStart && j < len(s) && s[j] == ';' {
			if n, ok := atoiStrSafe(s[numStart:j]); ok {
				row = n
			}
		}
		i = j
	}
	return row
}

func atoiStrSafe(s string) (int, bool) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}
