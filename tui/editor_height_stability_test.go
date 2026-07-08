// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import "testing"

// TestEditor_RenderHeightIsStable verifies that the editor reserves the height
// it once needed so the input line and components below it do not jump up when
// the buffer shrinks (after submit/clear). Height is allowed to grow, and is
// reset only on terminal resize.
func TestEditor_RenderHeightIsStable(t *testing.T) {
	editor := NewEditor()
	editor.SetMaxLines(5)

	// Empty editor: natural height is 1 content line + 2 borders.
	empty := len(editor.Render(40))

	// Grow to a multi-line message.
	editor.SetText("line1\nline2\nline3\nline4\nline5")
	grown := len(editor.Render(40))
	if grown <= empty {
		t.Fatalf("grown height %d should be > empty height %d", grown, empty)
	}

	// After clear the height must stay at the grown value.
	editor.Clear()
	cleared := len(editor.Render(40))
	if cleared != grown {
		t.Fatalf("cleared editor height = %d, want stable %d", cleared, grown)
	}

	// New short text also keeps the stable height.
	editor.SetText("hi")
	short := len(editor.Render(40))
	if short != grown {
		t.Fatalf("short editor height = %d, want stable %d", short, grown)
	}
}
