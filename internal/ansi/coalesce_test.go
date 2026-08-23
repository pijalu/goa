// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package ansi

import "testing"

// Styled.Render's emission shape: prefix + text + "\x1b[0m" per piece.
func piece(prefix, text string) string {
	return prefix + text + Reset
}

const (
	fgRed     = "\x1b[38;2;255;80;80m"
	fgBlue    = "\x1b[38;2;80;120;255m"
	fgGray    = "\x1b[38;2;139;148;158m"
	boldOn    = "\x1b[1m"
	italOn    = "\x1b[3m"
	linkOpen  = "\x1b]8;;https://goa.test\x07"
	linkClose = "\x1b]8;;\x07"
)

func TestSGRCoalescer_Filter(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "plain text is untouched",
			in:   "hello world",
			want: "hello world",
		},
		{
			name: "already minimal piece is byte-identical",
			in:   piece(fgGray, "text"),
			want: fgGray + "text" + Reset,
		},
		{
			name: "adjacent duplicate SGR runs collapse to one",
			in:   boldOn + boldOn + "A",
			want: boldOn + "A",
		},
		{
			name: "duplicate run after text elides via state tracking",
			in:   boldOn + "ab" + boldOn + "cd",
			want: boldOn + "abcd",
		},
		{
			name: "reset immediately re-opening same style collapses to nothing",
			// Two consecutive pieces of the SAME style: the classic
			// Styled.Render seam.
			in:   piece(fgGray, "foo") + piece(fgGray, "bar"),
			want: fgGray + "foobar" + Reset,
		},
		{
			name: "reset then different style keeps exactly one open",
			in:   piece(fgRed, "err") + piece(fgBlue, "ok"),
			want: fgRed + "err" + fgBlue + "ok" + Reset,
		},
		{
			name: "redundant trailing reset on default state elides",
			in:   "plain" + Reset,
			want: "plain",
		},
		{
			name: "multi-run opening merges into canonical single run",
			// Styled.Prefix emits fg, then bold, then italic as three runs.
			in:   fgRed + boldOn + italOn + "X" + Reset,
			want: "\x1b[1;3;38;2;255;80;80mX" + Reset,
		},
		{
			name: "truecolor RGB components are not mistaken for resets",
			// The params of 38;2;R;G;B contain literal zero tokens.
			in:   "\x1b[38;2;0;0;0mA" + Reset,
			want: "\x1b[38;2;0;0;0mA" + Reset,
		},
		{
			name: "non-SGR barriers pass through verbatim between kept runs",
			in:   boldOn + "A" + Reset + "\x1b[10;1H\x1b[2K" + boldOn + "B" + Reset,
			want: "\x1b[1mA" + Reset + "\x1b[10;1H\x1b[2K\x1b[1mB" + Reset,
		},
		{
			name: "cursor show/hide and sync markers are never touched",
			in:   "\x1b[?2026h\x1b[?25l\x1b[?25h\x1b[?2026l",
			want: "\x1b[?2026h\x1b[?25l\x1b[?25h\x1b[?2026l",
		},
		{
			name: "private-parameter CSI is not SGR",
			in:   "\x1b[>4m",
			want: "\x1b[>4m",
		},
		{
			name: "OSC 8 hyperlinks pass through intact",
			in:   linkOpen + fgBlue + "link" + Reset + linkClose,
			want: linkOpen + fgBlue + "link" + Reset + linkClose,
		},
		{
			name: "basic palette code poisons elisions until hard reset",
			// 31 is not modeled: nothing after it may be elided on state
			// grounds until a reset re-synchronizes the tracker.
			in:   "\x1b[31m" + boldOn + "A" + Reset + boldOn + "B",
			want: "\x1b[31m" + boldOn + "A" + Reset + boldOn + "B",
		},
		{
			name: "hard reset re-establishes trust after poisoning",
			// [31m][0m] form ONE adjacent run: the trailing reset makes its
			// net effect on a default-state terminal zero, so eliding both
			// is state-equivalent. [1m] after the (elided) reset is emitted.
			in:   "\x1b[31m" + Reset + boldOn + "A",
			want: boldOn + "A",
		},
		{
			name: "poisoning suppresses later duplicate elision until reset",
			// The second [1m] looks like a duplicate but a palette color is
			// really active: it must be kept even though the tracker cannot
			// see it.
			in:   "\x1b[31mX" + boldOn + boldOn + "Y",
			want: "\x1b[31mX" + boldOn + boldOn + "Y",
		},
		{
			name: "empty SGR parameter is a hard reset",
			// "\x1b[;m" on a default-state terminal is a no-op, so eliding
			// it is state-equivalent.
			in:   "\x1b[;mA",
			want: "A",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc := NewSGRCoalescer()
			if got := sc.Filter(tt.in); got != tt.want {
				t.Errorf("Filter mismatch\n in:  %q\n got: %q\nwant: %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestSGRCoalescer_Idempotent asserts filtering an already-filtered frame is
// the identity: coalesced output must itself be already-minimal input.
func TestSGRCoalescer_Idempotent(t *testing.T) {
	inputs := []string{
		piece(fgGray, "foo") + piece(fgGray, "bar") + piece(fgRed, "baz"),
		boldOn + "x" + Reset + "\x1b[5;1H\x1b[2K" + italOn + "y" + Reset,
		linkOpen + "q" + linkClose + Reset,
		"\x1b[31mpoisoned\x1b[0m tail",
	}
	for _, in := range inputs {
		sc := NewSGRCoalescer()
		once := sc.Filter(in)
		twice := sc.Filter(once)
		if once != twice {
			t.Errorf("not idempotent\n once: %q\n twice: %q", once, twice)
		}
	}
}

// TestSGRCoalescer_StatePersistsAcrossCalls mirrors the wire stream: frames
// are filtered one at a time but the terminal state carries over, so an
// unterminated style at frame N's tail can suppress frame N+1's identical
// opening prefix.
func TestSGRCoalescer_StatePersistsAcrossCalls(t *testing.T) {
	sc := NewSGRCoalescer()

	// Frame 1: row truncated mid-styled-text (no trailing reset).
	frame1 := "head " + fgGray + "truncated-tail"
	got1 := sc.Filter(frame1)
	if want := frame1; got1 != want {
		t.Fatalf("frame1 = %q, want %q", got1, want)
	}

	// Frame 2: next row opens with the SAME color the terminal still holds —
	// the prefix is redundant and must vanish.
	got2 := sc.Filter("\x1b[10;1H\x1b[2K" + fgGray + "more text")
	if want := "\x1b[10;1H\x1b[2Kmore text"; got2 != want {
		t.Errorf("frame2 = %q, want %q", got2, want)
	}

	// A DIFFERENT color must still be emitted.
	got3 := sc.Filter(fgRed + "switch")
	if want := fgRed + "switch"; got3 != want {
		t.Errorf("frame3 = %q, want %q", got3, want)
	}

	// After Restore()'s out-of-band reset the tracker assumes default again.
	sc.Reset()
	got4 := sc.Filter("plain")
	if want := "plain"; got4 != want {
		t.Errorf("frame4 = %q, want %q", got4, want)
	}
}

// TestSGRCoalescer_EquivalentToRaw pins the core safety property at the unit
// level: for styled compositions, filtering must produce output whose
// stripped visible text matches the raw stream exactly.
func TestSGRCoalescer_EquivalentToRaw(t *testing.T) {
	raw := piece(fgGray, "user") + " " +
		piece(fgRed, "error!") + "\n" +
		piece(boldOn+fgBlue, "title") + piece(boldOn+fgBlue, ": subtitle") + "\n" +
		"\x1b[10;1H\x1b[2K" + piece(fgGray, "next row")
	sc := NewSGRCoalescer()
	got := sc.Filter(raw)
	if Strip(got) != Strip(raw) {
		t.Errorf("visible text changed\nraw:  %q\ngot:  %q", Strip(raw), Strip(got))
	}
	if len(got) >= len(raw) {
		t.Errorf("expected reduction: raw=%d got=%d (%q)", len(raw), len(got), got)
	}
}
