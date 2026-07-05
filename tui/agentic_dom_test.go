// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"
)

// TestAgenticDOM_CursorUpFromEmptySecondLine drives the TUI through the
// full input/render pipeline and validates the cursor position using the
// agentic screen model. This is the integration-level regression test for
// the B2 navigation bug where Up failed to move from an empty second line.
func TestAgenticDOM_CursorUpFromEmptySecondLine(t *testing.T) {
	term := &fakeTerminal{w: 80, h: 24}
	engine := NewTUI(term)

	ed := NewEditor()
	ed.SetTUI(engine)
	ed.SetMaxLines(3)
	ed.SetFocused(true)

	engine.AddChild(ed)
	engine.SetFocus(ed)
	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	engine.SendKey("abc")
	engine.SendKey("alt+enter")
	engine.SendKey("up")

	frame := engine.AgentFrame()
	if frame.Cursor == nil {
		t.Fatal("expected cursor in frame")
	}
	if frame.Cursor.Row != 1 {
		t.Errorf("cursor row = %d, want 1 (first content line after border)", frame.Cursor.Row)
	}
	if frame.Cursor.Col != 0 {
		t.Errorf("cursor col = %d, want 0 (start of 'abc')", frame.Cursor.Col)
	}

	inputNode := frame.FindNode("Editor")
	if inputNode == nil {
		t.Fatal("expected Editor node in agentic DOM")
	}
	if !strings.Contains(inputNode.Text, "abc") {
		t.Errorf("Editor node text should contain 'abc', got %q", inputNode.Text)
	}
	if !inputNode.Focused {
		t.Error("Editor node should be focused")
	}

	cursorNode := frame.CursorNode()
	if cursorNode == nil {
		t.Fatal("expected CursorNode to identify the editor")
	}
	if cursorNode.Name != "Editor" {
		t.Errorf("CursorNode name = %q, want Editor", cursorNode.Name)
	}
}

// TestAgenticDOM_CursorUpFromEmptySecondLine_RawCSIu uses the actual CSI-u
// byte sequences emitted by a modern terminal for Ctrl+Enter and Up, ensuring
// the B2 fix works through the full decode/render pipeline.
func TestAgenticDOM_CursorUpFromEmptySecondLine_RawCSIu(t *testing.T) {
	term := &fakeTerminal{w: 80, h: 24}
	engine := NewTUI(term)

	ed := NewEditor()
	ed.SetTUI(engine)
	ed.SetMaxLines(3)
	ed.SetFocused(true)

	engine.AddChild(ed)
	engine.SetFocus(ed)
	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	engine.SendKey("abc")
	engine.SendKey("\x1b[13;5u") // Ctrl+Enter (CSI-u)
	engine.SendKey("\x1b[A")     // Up arrow

	frame := engine.AgentFrame()
	if frame.Cursor == nil {
		t.Fatal("expected cursor in frame")
	}
	if frame.Cursor.Row != 1 {
		t.Errorf("cursor row = %d, want 1", frame.Cursor.Row)
	}
	if frame.Cursor.Col != 0 {
		t.Errorf("cursor col = %d, want 0", frame.Cursor.Col)
	}
}

// TestAgenticDOM_Dump_CoversNodes verifies Dump() includes key nodes and cursor.
func TestAgenticDOM_Dump_CoversNodes(t *testing.T) {
	term := &fakeTerminal{w: 80, h: 24}
	engine := NewTUI(term)

	ed := NewEditor()
	ed.SetTUI(engine)
	ed.SetFocused(true)

	engine.AddChild(ed)
	engine.SetFocus(ed)
	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	engine.SendKey("hi")
	dump := engine.AgentFrame().Dump()
	if !strings.Contains(dump, "AgentFrame 80x24") {
		t.Errorf("Dump missing frame size: %s", dump)
	}
	if !strings.Contains(dump, "node Editor") {
		t.Errorf("Dump missing Editor node: %s", dump)
	}
	if !strings.Contains(dump, "cursor:") {
		t.Errorf("Dump missing cursor: %s", dump)
	}
}
