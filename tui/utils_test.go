// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

func TestPadToWidthStyled_PreservesBackgroundAcrossInternalReset(t *testing.T) {
	bg := ansi.Bg("#ff0000")
	content := "before " + ansi.Bold + "bold" + ansi.Reset + " after"
	line := padToWidthStyled(content, 40, bg)

	if !strings.HasPrefix(line, bg) {
		t.Errorf("line should start with background code: %q", line)
	}
	if !strings.HasSuffix(line, ansi.Reset) {
		t.Errorf("line should end with reset: %q", line)
	}

	// Any internal reset must be followed by the background code.
	trimmed := strings.TrimSuffix(line, ansi.Reset)
	for {
		idx := strings.Index(trimmed, ansi.Reset)
		if idx < 0 {
			break
		}
		after := trimmed[idx+len(ansi.Reset):]
		if !strings.HasPrefix(after, bg) {
			t.Fatalf("internal reset not followed by background: %q", trimmed)
		}
		trimmed = after
	}

	if visibleWidth(line) != 40 {
		t.Errorf("visible width = %d, want 40", visibleWidth(line))
	}
}

func TestPadToWidthStyled_NoBackground_NoModification(t *testing.T) {
	content := "plain text"
	line := padToWidthStyled(content, 20, "")
	if strings.Contains(line, ansi.Bg("#ff0000")) {
		t.Errorf("unexpected background code in unstyled line: %q", line)
	}
	if visibleWidth(line) != 20 {
		t.Errorf("visible width = %d, want 20", visibleWidth(line))
	}
}

func TestPadToWidthStyled_EmptyContent_FillsBackground(t *testing.T) {
	bg := ansi.Bg("#00ff00")
	line := padToWidthStyled("", 10, bg)
	want := bg + strings.Repeat(" ", 10) + ansi.Reset
	if line != want {
		t.Errorf("line = %q, want %q", line, want)
	}
}

func TestPadToWidthStyled_ConvertsTabsToSpaces(t *testing.T) {
	bg := ansi.Bg("#00ff00")

	tests := []struct {
		name  string
		input string
	}{
		{"du-style output", "19M\t./goa"},
		{"single tab", "a\tb"},
		{"multiple tabs", "a\tb\tc"},
		{"tab at start", "\tfoo"},
		{"tab at end", "foo\t"},
		{"no tab", "hello world"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			styled := ansiToolOutput(tt.input)
			result := padToWidthStyled(" "+styled, 80, bg)
			plain := ansi.Strip(result)

			if strings.Contains(plain, "\t") {
				t.Errorf("tab survived in output: plain=%q", plain)
			}

			// Background must cover full width
			if !strings.HasSuffix(result, ansi.Reset) {
				t.Errorf("line should end with \\e[0m, got suffix %q", result[len(result)-10:])
			}
			if strings.Contains(result, ansi.BgReset) {
				t.Errorf("BgReset found: %q", result)
			}
		})
	}
}

func TestTruncateToWidth_FitsNoTruncation(t *testing.T) {
	// When text already fits within maxWidth, it must be returned unchanged.
	text := "hello world"
	result := truncateToWidth(text, 15, "...")
	if result != text {
		t.Errorf("truncateToWidth(%q, 15, %q) = %q, want %q", text, "...", result, text)
	}
}

func TestTruncateToWidth_NeedsTruncation(t *testing.T) {
	// When text exceeds maxWidth, ellipsis must replace truncated chars.
	// "hello world" is 11 chars, truncate to 8 → "hello wo" without the "rld".
	// With ellipsis: "hello wo" = 8 chars, then "..." = 3 chars = 11. But maxWidth is 8.
	// So: truncate to 5 + "..." = 8.
	text := "hello world"
	result := truncateToWidth(text, 8, "...")
	// Expected: first 5 chars + "..." = 8 chars
	want := "hello..."
	if result != want {
		t.Errorf("truncateToWidth(%q, 8, %q) = %q, want %q", text, "...", result, want)
	}
	if visibleWidth(result) != 8 {
		t.Errorf("visibleWidth = %d, want 8", visibleWidth(result))
	}
}

func TestTruncateToWidth_EmptyEllipsis(t *testing.T) {
	text := "hello world"
	result := truncateToWidth(text, 5, "")
	want := "hello"
	if result != want {
		t.Errorf("truncateToWidth(%q, 5, %q) = %q, want %q", text, "", result, want)
	}
}

func TestTruncateToWidth_PreservesANSI(t *testing.T) {
	// ANSI codes must be preserved in the truncated result.
	text := ansi.Bold + "hello world" + ansi.Reset
	result := truncateToWidth(text, 8, "...")
	if !strings.HasPrefix(result, ansi.Bold) {
		t.Errorf("result should start with bold: %q", result)
	}
	// Should have ANSI codes AND correct visible width
	if visibleWidth(result) != 8 {
		t.Errorf("visibleWidth = %d, want 8", visibleWidth(result))
	}
}

func TestTruncateToWidth_EmptyText(t *testing.T) {
	if result := truncateToWidth("", 10, "..."); result != "" {
		t.Errorf("empty text should return empty, got %q", result)
	}
	if result := truncateToWidth("hello", 0, "..."); result != "" {
		t.Errorf("maxWidth=0 should return empty, got %q", result)
	}
}

func TestPadToWidthStyled_TabWidthMatchesBothConversions(t *testing.T) {
	bg := ansi.Bg("#00ff00")

	// Tab and 4 spaces should result in the same visible width
	// because ansi.Width also uses 4-column tab stops internally.
	withTab := ansiToolOutput("19M\t./goa")
	result := padToWidthStyled(" "+withTab, 80, bg)
	plain := ansi.Strip(result)
	if len(plain) != 80 {
		t.Errorf("padded result should be exactly 80 columns, got %d: %q", len(plain), plain)
	}
}
