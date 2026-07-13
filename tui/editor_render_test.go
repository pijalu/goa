// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"reflect"
	"testing"
)

// TestWrapText_PreservesTrailingSpace verifies that trailing spaces at the end
// of a wrapped paragraph are preserved and wrapped onto the final line(s).
func TestWrapText_PreservesTrailingSpace(t *testing.T) {
	// Width 10, "1234567890" fills the first visual line, "this" is on the
	// second line, and the trailing space should remain on that second line.
	got := wrapText("1234567890 this ", 10)
	want := []string{"1234567890", "this "}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("wrapText got %v, want %v", got, want)
	}

	got = wrapText("1234567890 this t", 10)
	want = []string{"1234567890", "this t"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("wrapText after 't' got %v, want %v", got, want)
	}
}

// TestEditor_VisualCursor_WrapTrailingSpace verifies that the cursor position
// accounts for trailing spaces that are preserved on the wrapped line.
func TestEditor_VisualCursor_WrapTrailingSpace(t *testing.T) {
	ed := NewEditor()
	ed.lastWidth = 10

	ed.SetText("1234567890 this ")
	line, col := ed.VisualCursor(10)
	if line != 1 {
		t.Errorf("VisualCursor line = %d, want 1 (space on second line)", line)
	}
	if col != 5 {
		t.Errorf("VisualCursor col = %d, want 5 (after the space)", col)
	}

	ed.SetText("1234567890 this t")
	line, col = ed.VisualCursor(10)
	if line != 1 {
		t.Errorf("VisualCursor line after 't' = %d, want 1", line)
	}
	if col != 6 {
		t.Errorf("VisualCursor col after 't' = %d, want 6", col)
	}
}

// TestWrapText_PreservesManyTrailingSpaces verifies that trailing spaces are
// wrapped onto additional lines when they overflow the current line.
func TestWrapText_PreservesManyTrailingSpaces(t *testing.T) {
	// Width 10, text is "hello" followed by 12 trailing spaces.
	// Line 1 fills to width with 5 spaces; line 2 holds the remaining 7.
	got := wrapText("hello            ", 10)
	want := []string{"hello     ", "       "}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("wrapText got %q, want %q", got, want)
	}
}

// TestWrapText_AllSpaces verifies that a paragraph consisting only of spaces
// is wrapped correctly.
func TestWrapText_AllSpaces(t *testing.T) {
	got := wrapText("     ", 2)
	want := []string{"  ", "  ", " "}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("wrapText all spaces got %v, want %v", got, want)
	}
}
