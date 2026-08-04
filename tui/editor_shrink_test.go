// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import "testing"

// TestEditor_BackspaceShrinksHeight reproduces bugs.md "Input line is not
// resized when content is deleted": typing a wrapped multi-line input grows
// the editor, and backspacing those lines away must shrink it back — the
// reserved height tracks the content, it is not ratcheted forever.
func TestEditor_BackspaceShrinksHeight(t *testing.T) {
	editor := NewEditor()
	editor.SetMaxLines(10)

	long := "aaaa bbbb cccc dddd eeee ffff gggg hhhh iiii jjjj kkkk llll"
	editor.SetText(long)
	grown := len(editor.Render(20))
	single := len(NewEditor().Render(20))
	if grown <= single {
		t.Fatalf("wrapped input should grow beyond one line: grown=%d single=%d", grown, single)
	}

	// Backspace everything away (user delete, not SetText).
	for range long {
		editor.backspace()
	}
	shrunk := len(editor.Render(20))
	if shrunk != single {
		t.Fatalf("after deleting the whole wrapped input, height = %d, want single-line %d", shrunk, single)
	}
}

// TestEditor_DeleteForwardShrinksHeight covers the Delete key path: removing
// visual lines with delete-forward must shrink the reserved height.
func TestEditor_DeleteForwardShrinksHeight(t *testing.T) {
	editor := NewEditor()
	editor.SetMaxLines(10)

	long := "aaaa bbbb cccc dddd eeee ffff gggg hhhh iiii jjjj kkkk llll"
	editor.SetText(long)
	grown := len(editor.Render(20))
	single := len(NewEditor().Render(20))
	if grown <= single {
		t.Fatalf("wrapped input should grow: grown=%d single=%d", grown, single)
	}

	// Move to start and delete the whole buffer forward.
	editor.pos = 0
	for range long {
		editor.deleteForward()
	}
	shrunk := len(editor.Render(20))
	if shrunk != single {
		t.Fatalf("after delete-forward of the whole input, height = %d, want %d", shrunk, single)
	}
}

// TestEditor_KillToEndShrinksHeight covers kill-to-end (Ctrl+K): a kill that
// removes visual lines must shrink the reserved height.
func TestEditor_KillToEndShrinksHeight(t *testing.T) {
	editor := NewEditor()
	editor.SetMaxLines(10)

	long := "aaaa bbbb cccc dddd eeee ffff gggg hhhh iiii jjjj kkkk llll"
	editor.SetText(long)
	grown := len(editor.Render(20))
	single := len(NewEditor().Render(20))
	if grown <= single {
		t.Fatalf("wrapped input should grow: grown=%d single=%d", grown, single)
	}

	editor.pos = 0
	editor.killToEnd()
	shrunk := len(editor.Render(20))
	if shrunk != single {
		t.Fatalf("after kill-to-end of the whole input, height = %d, want %d", shrunk, single)
	}
}

// TestEditor_PartialDeleteShrinksProportionally verifies deleting part of a
// wrapped input shrinks the height to the remaining visual-line count (not
// all the way, not not at all).
func TestEditor_PartialDeleteShrinksProportionally(t *testing.T) {
	editor := NewEditor()
	editor.SetMaxLines(10)

	// Two visual lines at width 20.
	editor.SetText("aaaa bbbb cccc\ndddd eeee ffff")
	grown := len(editor.Render(20))

	// Backspace over "dddd eeee ffff" + the newline.
	for range "dddd eeee ffff\n" {
		editor.backspace()
	}
	oneLine := NewEditor()
	oneLine.SetText("aaaa bbbb cccc")
	want := len(oneLine.Render(20))
	shrunk := len(editor.Render(20))
	if shrunk != want {
		t.Fatalf("after deleting the second line, height = %d, want %d (grown was %d)", shrunk, want, grown)
	}
	if shrunk >= grown {
		t.Fatalf("height should have shrunk below grown %d, got %d", grown, shrunk)
	}
}

// TestEditor_SetTextKeepsStability pins the history-recall behavior: a
// wholesale SetText (history up/down) keeps the reserved height stable so
// the layout does not jitter while browsing history.
func TestEditor_SetTextKeepsStability(t *testing.T) {
	editor := NewEditor()
	editor.SetMaxLines(10)

	editor.SetText("aaaa bbbb cccc dddd eeee ffff gggg hhhh iiii jjjj kkkk llll")
	grown := len(editor.Render(20))

	editor.SetText("short")
	shrunk := len(editor.Render(20))
	if shrunk != grown {
		t.Fatalf("SetText replacement should keep stable height: got %d, want %d", shrunk, grown)
	}
}

// TestEditor_ShrinkThenRegrow verifies the decay does not break growth: after
// shrinking, typing a wrapped line grows the editor again.
func TestEditor_ShrinkThenRegrow(t *testing.T) {
	editor := NewEditor()
	editor.SetMaxLines(10)

	long := "aaaa bbbb cccc dddd eeee ffff gggg hhhh iiii jjjj kkkk llll"
	editor.SetText(long)
	grown := len(editor.Render(20))

	for range long {
		editor.backspace()
	}
	single := len(NewEditor().Render(20))
	if got := len(editor.Render(20)); got != single {
		t.Fatalf("post-delete height = %d, want %d", got, single)
	}

	editor.SetText(long)
	regrown := len(editor.Render(20))
	if regrown != grown {
		t.Fatalf("regrown height = %d, want original grown %d", regrown, grown)
	}
}
