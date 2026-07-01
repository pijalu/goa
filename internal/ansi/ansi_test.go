// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package ansi

import (
	"strings"
	"testing"
)

func TestHexToRGB(t *testing.T) {
	tests := []struct {
		hex   string
		wantR uint8
		wantG uint8
		wantB uint8
	}{
		{"#ffffff", 255, 255, 255},
		{"#000000", 0, 0, 0},
		{"#ff0000", 255, 0, 0},
		{"#00ff00", 0, 255, 0},
		{"#0000ff", 0, 0, 255},
		{"#abc", 170, 187, 204},
		{"#aabbcc", 170, 187, 204},
		{"invalid", 128, 128, 128},
		{"", 128, 128, 128},
	}
	for _, tt := range tests {
		r, g, b := HexToRGB(tt.hex)
		if r != tt.wantR || g != tt.wantG || b != tt.wantB {
			t.Errorf("HexToRGB(%q) = (%d,%d,%d), want (%d,%d,%d)", tt.hex, r, g, b, tt.wantR, tt.wantG, tt.wantB)
		}
	}
}

func TestStrip(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "hello"},
		{Fg("#ff0000") + "hello" + Reset, "hello"},
		{Bold + "bold" + Reset + " normal", "bold normal"},
		{"", ""},
		{"no ansi here", "no ansi here"},
		// OSC title set terminated by BEL must be fully stripped.
		{"\x1b]0;hello\x07visible\x1b[31mred\x1b[0m", "visiblered"},
		// OSC terminated by ST (ESC \).
		{"\x1b]2;title\x1b\\body", "body"},
		// Non-SGR CSI (clear screen / cursor moves) are zero-width, must strip.
		{"\x1b[2J\x1b[Hclean", "clean"},
	}
	for _, tt := range tests {
		got := Strip(tt.input)
		if got != tt.want {
			t.Errorf("Strip(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestWidth(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"hello", 5},
		{Fg("#ff0000") + "hi" + Reset, 2},
		// Tabs expand to the terminal's default 8-column tab stops.
		{"tab\there", 12},
		{"a\tb", 9},
		{"\thello", 13},
		{"", 0},
		{"a\nb", 2}, // newlines are zero-width (go-runewidth)
	}
	for _, tt := range tests {
		got := Width(tt.input)
		if got != tt.want {
			t.Errorf("Width(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		text  string
		width int
		want  string
	}{
		{"hello world", 5, "hello"},
		{"hello world", 100, "hello world"},
		{Fg("#ff") + "hello" + Reset, 3, Fg("#ff") + "hel"},
		{"", 5, ""},
		{"hello", 0, ""},
	}
	for _, tt := range tests {
		got := Truncate(tt.text, tt.width)
		if got != tt.want {
			t.Errorf("Truncate(%q, %d) = %q, want %q", tt.text, tt.width, got, tt.want)
		}
	}
}

func TestWrap(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		width int
		want  []string
	}{
		{
			name:  "simple",
			text:  "hello world foo bar",
			width: 10,
			want:  []string{"hello", "world foo", "bar"},
		},
		{
			name:  "exact fit",
			text:  "hello world",
			width: 11,
			want:  []string{"hello world"},
		},
		{
			name:  "long word",
			text:  "supercalifragilistic",
			width: 8,
			want:  []string{"supercal" + Reset, "ifragili" + Reset, "stic"},
		},
		{
			name:  "empty",
			text:  "",
			width: 10,
			want:  []string{""},
		},
		{
			name:  "with ansi",
			text:  Bold + "hello world" + Reset + " foo",
			width: 8,
			want:  []string{Bold + "hello", Bold + "world" + Reset, "foo"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Wrap(tt.text, tt.width)
			if len(got) != len(tt.want) {
				t.Fatalf("Wrap(%q, %d) = %v, want %v", tt.text, tt.width, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("line %d: got %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestExpandTabs(t *testing.T) {
	tests := []struct {
		input    string
		tabWidth int
		want     string
	}{
		{"hello", 8, "hello"},
		{"a\tb", 8, "a       b"},
		{"tab\there", 8, "tab     here"},
		{"\thello", 8, "        hello"},
		{"no\ttab", 4, "no  tab"},
		{Fg("#ff") + "a\tb" + Reset, 8, Fg("#ff") + "a       b" + Reset},
	}
	for _, tt := range tests {
		got := ExpandTabs(tt.input, tt.tabWidth)
		if got != tt.want {
			t.Errorf("ExpandTabs(%q, %d) = %q, want %q", tt.input, tt.tabWidth, got, tt.want)
		}
	}
}

func TestEscapeTracker(t *testing.T) {
	esc := &escapeTracker{}
	tests := []struct {
		ch   rune
		want bool
	}{
		{'\x1b', true},
		{'[', true},
		{'3', true},
		{'8', true},
		{';', true},
		{'2', true},
		{'m', true},
		{'h', false},
		{'\x1b', true},
		{'[', true},
		{'0', true},
		{'m', true},
	}
	for i, tt := range tests {
		got := esc.update(tt.ch)
		if got != tt.want {
			t.Errorf("step %d: update(%q) = %v, want %v", i, tt.ch, got, tt.want)
		}
	}
}

func TestHelpers(t *testing.T) {
	if !strings.HasPrefix(MoveUp(3), "\x1b[") {
		t.Error("MoveUp missing ESC prefix")
	}
	if !strings.HasPrefix(Fg("#ff0000"), "\x1b[") {
		t.Error("Fg missing ESC prefix")
	}
	if !strings.HasPrefix(Bg("#00ff00"), "\x1b[") {
		t.Error("Bg missing ESC prefix")
	}
}

func TestSplitWords(t *testing.T) {
	text := Bold + "hello" + Reset + " " + Italic + "world" + Reset
	words := splitWords(text)
	if len(words) != 2 {
		t.Fatalf("expected 2 words, got %d: %v", len(words), words)
	}
	if words[0] != Bold+"hello"+Reset {
		t.Errorf("word 0 = %q, want bold hello", words[0])
	}
	if words[1] != Italic+"world"+Reset {
		t.Errorf("word 1 = %q, want italic world", words[1])
	}
}

func TestRenderWithCursor(t *testing.T) {
	tests := []struct {
		name          string
		text          string
		cursorRunePos int
		want          string
	}{
		{"empty_at_end", "", 0, Reverse + " " + Reset},
		{"at_end", "hello", 5, "hello" + Reverse + " " + Reset},
		{"at_start", "hello", 0, Reverse + "h" + Reset + "ello"},
		{"middle", "hello", 2, "he" + Reverse + "l" + Reset + "lo"},
		{"negative", "hello", -1, "hello"},
		{"utf8", "\u65e5\u672c", 1, "\u65e5" + Reverse + "\u672c" + Reset},
		{"beyond_end", "hello", 100, "hello" + Reverse + " " + Reset},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RenderWithCursor(tt.text, tt.cursorRunePos)
			if got != tt.want {
				t.Errorf("RenderWithCursor(%q, %d) = %q, want %q", tt.text, tt.cursorRunePos, got, tt.want)
			}
		})
	}
}

func TestExtractTrailingSGR(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", ""},
		{Bold + "hello", Bold},
		{Bold + "hello" + Reset + Italic + "x", Italic},
		{"plain" + Reset + Bold + Italic, Bold + Italic},
	}
	for _, tt := range tests {
		got := extractTrailingSGR(tt.input)
		if got != tt.want {
			t.Errorf("extractTrailingSGR(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
