// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import "testing"

// TestEditor_DeleteShrinksHeightViaTUI drives the editor through the TUI
// engine and verifies backspacing a multi-line input shrinks the editor node
// (bugs.md: footer must not float up leaving a gap).
func TestEditor_DeleteShrinksHeightViaTUI(t *testing.T) {
	term := &fakeTerminal{w: 40, h: 24}
	engine := NewTUI(term)

	ed := NewEditor()
	ed.SetTUI(engine)
	ed.SetMaxLines(10)
	ed.SetFocused(true)

	engine.AddChild(ed)
	engine.SetFocus(ed)
	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Type a wrapped multi-line input.
	engine.SendKey("aaaa bbbb cccc dddd eeee ffff gggg hhhh iiii jjjj kkkk llll mmmm nnnn oooo pppp")
	frame := engine.AgentFrame()
	node := frame.FindNode("Editor")
	if node == nil {
		t.Fatal("expected Editor node")
	}
	grown := node.Rect.H
	if grown <= 3 {
		t.Fatalf("expected editor to grow beyond single-line, got %d", grown)
	}

	// Backspace everything away.
	for range "aaaa bbbb cccc dddd eeee ffff gggg hhhh iiii jjjj kkkk llll mmmm nnnn oooo pppp" {
		engine.SendKey(KeyBackspace)
	}
	frame = engine.AgentFrame()
	node = frame.FindNode("Editor")
	if node == nil {
		t.Fatal("expected Editor node after delete")
	}
	if node.Rect.H != 3 {
		t.Errorf("after deleting the input, editor height = %d, want 3 (grown was %d)", node.Rect.H, grown)
	}
}
