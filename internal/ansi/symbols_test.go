// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package ansi

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestBoxSymbolGlyphs pins every box constant to its exact codepoint so an
// accidental glyph change in the single source of truth fails loudly instead
// of silently shifting every rendered border.
func TestBoxSymbolGlyphs(t *testing.T) {
	cases := []struct {
		name      string
		got       string
		codepoint rune
	}{
		{"BoxHorizontal", BoxHorizontal, '─'},
		{"BoxVertical", BoxVertical, '│'},
		{"BoxTopLeft", BoxTopLeft, '┌'},
		{"BoxTopRight", BoxTopRight, '┐'},
		{"BoxBottomLeft", BoxBottomLeft, '└'},
		{"BoxBottomRight", BoxBottomRight, '┘'},
		{"BoxRoundedTopLeft", BoxRoundedTopLeft, '╭'},
		{"BoxRoundedTopRight", BoxRoundedTopRight, '╮'},
		{"BoxRoundedBottomRight", BoxRoundedBottomRight, '╯'},
		{"BoxRoundedBottomLeft", BoxRoundedBottomLeft, '╰'},
		{"BoxJunctionRight", BoxJunctionRight, '├'},
		{"BoxJunctionLeft", BoxJunctionLeft, '┤'},
		{"BoxJunctionDown", BoxJunctionDown, '┬'},
		{"BoxJunctionUp", BoxJunctionUp, '┴'},
		{"BoxCross", BoxCross, '┼'},
		{"BoxTitleLeft", BoxTitleLeft, '┨'},
		{"BoxTitleRight", BoxTitleRight, '┠'},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runes := []rune(tc.got)
			if len(runes) != 1 || runes[0] != tc.codepoint {
				t.Fatalf("%s = %q (U+%04X…), want single rune U+%04X",
					tc.name, tc.got, runes[0], tc.codepoint)
			}
			if w := utf8.RuneCountInString(tc.got); w != 1 {
				t.Fatalf("%s spans %d cells, want exactly 1", tc.name, w)
			}
		})
	}
}

// TestRepeatHorizontal checks bar repetition including degenerate counts.
func TestRepeatHorizontal(t *testing.T) {
	cases := []struct {
		name string
		n    int
		want string
	}{
		{"negative", -1, ""},
		{"zero", 0, ""},
		{"one", 1, BoxHorizontal},
		{"three", 3, "───"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RepeatHorizontal(tc.n); got != tc.want {
				t.Fatalf("RepeatHorizontal(%d) = %q, want %q", tc.n, got, tc.want)
			}
			if got := RepeatHorizontal(tc.n); got != strings.Repeat(BoxHorizontal, max(0, tc.n)) {
				t.Fatalf("RepeatHorizontal(%d) diverges from strings.Repeat", tc.n)
			}
		})
	}
}
