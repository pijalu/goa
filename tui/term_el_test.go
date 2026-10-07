// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import "testing"

// EL (erase in line) must follow real-terminal semantics: EL 0 clears from
// the cursor rightward only, EL 1 from the start of the line through the
// cursor, EL 2 the whole line. Treating EL 0 as a whole-line wipe made the
// emulator disagree with every real terminal about the byte streams a client
// that paints a row and then clears its tail produces — the row arrived
// blank here and intact on a real terminal.
func TestEraseLineForms(t *testing.T) {
	// "abcdef" on row 0, cursor parked at col 3, then each EL form.
	const setup = "\x1b[1;1Habcdef\x1b[1;4H"
	cases := []struct {
		name string
		el   string
		// want maps cell index to expected text ("" = blank cell).
		want map[int]string
	}{
		{"EL0 clears from cursor", "\x1b[K", map[int]string{0: "a", 1: "b", 2: "c", 3: "", 5: ""}},
		{"bare EL is EL0", "\x1b[0K", map[int]string{0: "a", 2: "c", 3: "", 4: ""}},
		{"EL1 clears through cursor", "\x1b[1K", map[int]string{0: "", 1: "", 2: "", 3: "", 4: "e", 5: "f"}},
		{"EL2 clears the line", "\x1b[2K", map[int]string{0: "", 2: "", 4: "", 5: ""}},
	}
	for _, c := range cases {
		e := NewTermEmulator(2, 10)
		e.Process(setup + c.el)
		for col, want := range c.want {
			if got := e.Cells(0)[col].Text; got != want {
				t.Errorf("%s: cell %d = %q, want %q", c.name, col, got, want)
			}
		}
	}
}

// The regressed behaviour, pinned from the renderer's exact stream: paint a
// full-width row, reset + EL — the text must survive, as it does on a real
// terminal.
func TestEraseLineAfterFullRowPaint(t *testing.T) {
	e := NewTermEmulator(2, 40)
	e.Process("\x1b[1;1Hrowzero" + "                " + "\x1b[0m\x1b[K")
	if got := e.Cells(0)[0].Text; got != "r" {
		t.Fatalf("full-row paint + EL lost the text: cell 0 = %q", got)
	}
	if got := e.Cells(0)[6].Text; got != "o" {
		t.Fatalf("full-row paint + EL corrupted the tail: cell 6 = %q", got)
	}
}
