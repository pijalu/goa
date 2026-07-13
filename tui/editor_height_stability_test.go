// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import "testing"

// TestEditor_RenderHeightResetsOnClear verifies that the editor returns to a
// single-line height after an explicit Clear(). The stable height is still
// maintained while typing so the layout does not jitter, but a clear action
// intentionally resets the reserved space.
func TestEditor_RenderHeightResetsOnClear(t *testing.T) {
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

	// After Clear() the height must fall back to the empty single-line value.
	editor.Clear()
	cleared := len(editor.Render(40))
	if cleared != empty {
		t.Fatalf("cleared editor height = %d, want empty height %d", cleared, empty)
	}

	// New short text also keeps the single-line height.
	editor.SetText("hi")
	short := len(editor.Render(40))
	if short != empty {
		t.Fatalf("short editor height = %d, want empty height %d", short, empty)
	}
}

// TestEditor_RenderHeightIsStableWhileTyping verifies that the editor reserves
// the height it once needed while the user is typing/editing, so the input line
// and components below it do not jump up when the buffer shrinks mid-edit.
func TestEditor_RenderHeightIsStableWhileTyping(t *testing.T) {
	editor := NewEditor()
	editor.SetMaxLines(5)

	editor.SetText("line1\nline2\nline3\nline4\nline5")
	grown := len(editor.Render(40))

	// Delete content without clearing: height should stay stable.
	editor.SetText("line1")
	shrunk := len(editor.Render(40))
	if shrunk != grown {
		t.Fatalf("shrunk editor height = %d, want stable %d", shrunk, grown)
	}
}

// TestEditor_ClearResetsHeightViaTUI drives the editor through the TUI engine
// and verifies that pressing Ctrl+C on a multi-line input shrinks the editor
// back to a single-line height.
func TestEditor_ClearResetsHeightViaTUI(t *testing.T) {
	term := &fakeTerminal{w: 80, h: 24}
	engine := NewTUI(term)

	ed := NewEditor()
	ed.SetTUI(engine)
	ed.SetMaxLines(5)
	ed.SetFocused(true)

	engine.AddChild(ed)
	engine.SetFocus(ed)
	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Build a multi-line input so the editor grows.
	engine.SendKey("line1")
	engine.SendKey("alt+enter")
	engine.SendKey("line2")
	engine.SendKey("alt+enter")
	engine.SendKey("line3")

	frame := engine.AgentFrame()
	inputNode := frame.FindNode("Editor")
	if inputNode == nil {
		t.Fatal("expected Editor node in agentic DOM")
	}
	grownHeight := inputNode.Rect.H
	if grownHeight <= 3 {
		t.Fatalf("expected editor to grow beyond single-line, got height %d", grownHeight)
	}

	// Press Ctrl+C to clear the input.
	engine.SendKey(KeyCtrlC)

	frame = engine.AgentFrame()
	inputNode = frame.FindNode("Editor")
	if inputNode == nil {
		t.Fatal("expected Editor node after clear")
	}
	clearedHeight := inputNode.Rect.H

	// The editor must collapse back to a single-line editor
	// (3 rows: top border + content + bottom border).
	if clearedHeight > 3 {
		t.Errorf("cleared editor height = %d, want 3 (single-line collapsed)", clearedHeight)
	}
}

