// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

// ── findStringEnd unit tests ───────────────────────────────────

func TestFindStringEnd(t *testing.T) {
	tests := []struct {
		name       string
		line       string
		start      int
		closeDelim byte
		escape     byte
		want       int
	}{
		{
			name:       "simple string",
			line:       `"hello"`,
			start:      1,
			closeDelim: '"',
			escape:     '\\',
			want:       7,
		},
		{
			name:       "string with escape sequence",
			line:       "\"hello\\nworld\"",
			start:      1,
			closeDelim: '"',
			escape:     '\\',
			want:       14,
		},
		{
			name:       "string with escaped quote",
			line:       "\"hello\\\"world\"",
			start:      1,
			closeDelim: '"',
			escape:     '\\',
			want:       14,
		},
		{
			name:       "unclosed string",
			line:       `"hello`,
			start:      1,
			closeDelim: '"',
			escape:     '\\',
			want:       6, // len(line)
		},
		{
			name:       "unclosed string ending with escape (the crash bug)",
			line:       "\"hello\\",
			start:      1,
			closeDelim: '"',
			escape:     '\\',
			want:       7, // len(line), not len(line)+1
		},
		{
			name:       "just escape, no closing quote",
			line:       "\"\\",
			start:      1,
			closeDelim: '"',
			escape:     '\\',
			want:       2, // len(line)
		},
		{
			name:       "empty string",
			line:       `""`,
			start:      1,
			closeDelim: '"',
			escape:     '\\',
			want:       2,
		},
		{
			name:       "single char string",
			line:       `"a"`,
			start:      1,
			closeDelim: '"',
			escape:     '\\',
			want:       3,
		},
		{
			name:       "multiple escapes before close",
			line:       "\"a\\\\n\"",
			start:      1,
			closeDelim: '"',
			escape:     '\\',
			want:       6,
		},
		{
			name:       "escape at last char then more",
			line:       "\"abc\\\\\"",
			start:      1,
			closeDelim: '"',
			escape:     '\\',
			want:       7,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findStringEnd(tt.line, tt.start, tt.closeDelim, tt.escape)
			if got != tt.want {
				t.Errorf("findStringEnd(%q, %d, %c, %c) = %d, want %d",
					tt.line, tt.start, tt.closeDelim, tt.escape, got, tt.want)
			}
			// Never return past len(line)
			if got > len(tt.line) {
				t.Errorf("findStringEnd returned %d which is > len(line)=%d", got, len(tt.line))
			}
		})
	}
}

// ── writeGoString integration tests ────────────────────────────

// lineFromWriter calls writeGoString and returns the written string and new position.
func lineFromWriter(line string, i int) (written string, newI int, ok bool) {
	var out strings.Builder
	c := &hlColors{
		str:   ansi.Fg("#a5d6ff"),
		reset: ansi.FgReset,
	}
	result := writeGoString(line, &i, &out, c)
	return out.String(), i, result
}

func TestWriteGoString_BacktickString(t *testing.T) {
	line := "`hello`"
	_, newI, ok := lineFromWriter(line, 0)
	if !ok {
		t.Fatal("writeGoString returned false for backtick string")
	}
	if newI != 7 {
		t.Errorf("newI = %d, want 7", newI)
	}
}

func TestWriteGoString_UnclosedBacktick(t *testing.T) {
	// Unclosed backtick should not panic
	line := "`hello"
	_, newI, ok := lineFromWriter(line, 0)
	if !ok {
		t.Fatal("writeGoString returned false for unclosed backtick string")
	}
	if newI != 6 { // len(line)
		t.Errorf("newI = %d, want %d", newI, len(line))
	}
}

func TestWriteGoString_StringWithTrailingEscape(t *testing.T) {
	// This is the crash: string ending with escape before closing quote
	line := "\"hello\\"
	_, newI, ok := lineFromWriter(line, 0)
	if !ok {
		t.Fatal("writeGoString returned false")
	}
	if newI > len(line) {
		t.Errorf("newI = %d, should not exceed len(line)=%d", newI, len(line))
	}
}

func TestWriteGoString_JustEscape(t *testing.T) {
	line := "\"\\"
	_, newI, ok := lineFromWriter(line, 0)
	if !ok {
		t.Fatal("writeGoString returned false")
	}
	if newI > len(line) {
		t.Errorf("newI = %d, should not exceed len(line)=%d", newI, len(line))
	}
}

// ── highlightGo crash regression tests ─────────────────────────

func TestHighlightGo_UnclosedStringWithTrailingEscape(t *testing.T) {
	fg := ansi.Fg("#c9d1d9")
	lines := []string{
		"x := \"abc\\",       // unclosed string, escape at end
		"s := \"test\\\"",    // unclosed string, escaped quote at end
		"a := \"\\",          // just escape
		"b := \"\\\\",        // escaped backslash at end (unclosed)
		"c := \"x\\\"y\\\"",  // multiple escapes
		"d := \"`raw\\\\`\"", // string containing backtick-raw-like content
	}

	for _, line := range lines {
		t.Run("", func(t *testing.T) {
			result := highlightGo(line, fg)
			if result == "" {
				t.Log("highlightGo returned empty string (no highlight applied)")
			}
		})
	}
}

func TestHighlightGo_VariousStrings(t *testing.T) {
	fg := ansi.Fg("#c9d1d9")
	inputs := []string{
		`func main() { fmt.Println("hello") }`,
		`str := "hello\nworld"`,
		`str := "hello\"world"`,
		`r := 'a'`,
		`package main`,
		`type Foo struct { Name string }`,
		`numbers := []int{1, 2, 3}`,
		`if x > 0 { return x }`,
		`// just a comment`,
	}
	for _, line := range inputs {
		t.Run("", func(t *testing.T) {
			result := highlightGo(line, fg)
			if result == "" {
				t.Errorf("highlightGo returned empty for %q", line)
			}
		})
	}
}

func TestHighlightGo_SingleCharacterString(t *testing.T) {
	fg := ansi.Fg("#c9d1d9")
	lines := []string{
		`a := "x"`,
		`b := 'x'`,
	}
	for _, line := range lines {
		t.Run("", func(t *testing.T) {
			result := highlightGo(line, fg)
			if result == "" {
				t.Errorf("highlightGo returned empty for %q", line)
			}
		})
	}
}

func TestHighlightGo_MaxLengthString(t *testing.T) {
	// Long unclosed string — should not panic
	fg := ansi.Fg("#c9d1d9")
	line := `s := "` + strings.Repeat("a", 50)
	result := highlightGo(line, fg)
	if result == "" {
		t.Log("highlightGo returned empty for long unclosed string (no crash)")
	}
}

func TestHighlightGo_BacktickInContent(t *testing.T) {
	// Lines containing backtick characters as content (not Go raw string delimiters)
	fg := ansi.Fg("#c9d1d9")
	lines := []string{
		"a := `hello`",   // backtick-delimited content (Go raw string)
		"b := `hello",    // unclosed backtick
		"c := `hello\\`", // backtick content with backslash
	}
	for _, line := range lines {
		t.Run("", func(t *testing.T) {
			result := highlightGo(line, fg)
			if result == "" && line != "" {
				t.Logf("highlightGo(%q) returned empty", line)
			}
		})
	}
}

// ── highlightTokenizer unit tests ──────────────────────────────

func TestHighlightTokenizer_NeverPanics(t *testing.T) {
	c := &hlColors{
		kw:    ansi.Fg("#d29922"),
		typ:   ansi.Fg("#58a6ff"),
		fn:    ansi.Fg("#3fb950"),
		str:   ansi.Fg("#a5d6ff"),
		num:   ansi.Fg("#79c0ff"),
		comm:  ansi.Faint,
		reset: ansi.FgReset,
	}
	keywords := map[string]bool{"func": true, "return": true}
	types := map[string]bool{"string": true}

	// Lines that could trigger out-of-bounds
	lines := []string{
		`"`,
		"\"\\",
		`"\"`,
		"\"\\\\\"",
		"\"\\\\\\",
		"a := \"hello\\",
		"'",
		"'\\",
		"\\",
		"\\`",
		"",
		"x",
	}

	for _, line := range lines {
		t.Run("", func(t *testing.T) {
			result := highlightTokenizer(line, keywords, types, c)
			if result == "" && line != "" {
				t.Logf("highlightTokenizer(%q) returned empty", line)
			}
		})
	}
}

// ── findStringEnd edge case ────────────────────────────────────

func TestFindStringEnd_NeverExceedsLength(t *testing.T) {
	lines := []string{
		"\"\\",      // open quote + escape at end (2 chars)
		"\"hello\\", // open quote + content + escape at end
		"\"\\\\",    // open quote + escaped backslash at end (unclosed)
		"\"a\\",     // single char + escape at end
		"\"\\\"",    // open + escaped quote... wait, that closes
		"\"\\\\\"",  // open + escaped backslash + closing quote
	}
	for _, line := range lines {
		t.Run("", func(t *testing.T) {
			if len(line) < 2 {
				return
			}
			end := findStringEnd(line, 1, '"', '\\')
			if end > len(line) {
				t.Errorf("findStringEnd(%q, 1, '\"', '\\\\') = %d > len(line)=%d", line, end, len(line))
			}
		})
	}
}
