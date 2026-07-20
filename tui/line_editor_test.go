// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"testing"
)

func TestLineEditor_SetText(t *testing.T) {
	e := NewLineEditor()
	e.SetText("hello")
	if e.Text() != "hello" {
		t.Errorf("Text() = %q, want %q", e.Text(), "hello")
	}
	if e.Cursor() != 5 {
		t.Errorf("Cursor() = %d, want 5", e.Cursor())
	}
}

func TestLineEditor_Clear(t *testing.T) {
	e := NewLineEditor()
	e.SetText("hello")
	e.Clear()
	if e.Text() != "" {
		t.Errorf("Text() = %q, want empty", e.Text())
	}
	if e.Cursor() != 0 {
		t.Errorf("Cursor() = %d, want 0", e.Cursor())
	}
}

func TestLineEditor_insert(t *testing.T) {
	e := NewLineEditor()
	e.SetText("heo")
	e.SetCursor(2)
	e.insert('l')
	e.insert('l')
	if e.Text() != "hello" {
		t.Errorf("Text() = %q, want %q", e.Text(), "hello")
	}
	if e.Cursor() != 4 {
		t.Errorf("Cursor() = %d, want 4", e.Cursor())
	}
}

func TestLineEditor_backspace(t *testing.T) {
	e := NewLineEditor()
	e.SetText("hello")
	e.backspace()
	if e.Text() != "hell" {
		t.Errorf("Text() = %q, want %q", e.Text(), "hell")
	}
	if e.Cursor() != 4 {
		t.Errorf("Cursor() = %d, want 4", e.Cursor())
	}
}

func TestLineEditor_backspaceEmpty(t *testing.T) {
	e := NewLineEditor()
	e.backspace()
	if e.Text() != "" {
		t.Errorf("Text() = %q, want empty", e.Text())
	}
}

func TestLineEditor_backspaceMultiByte(t *testing.T) {
	e := NewLineEditor()
	e.SetText("h\u00e9")
	e.backspace()
	if e.Text() != "h" {
		t.Errorf("Text() = %q, want %q", e.Text(), "h")
	}
	if e.Cursor() != 1 {
		t.Errorf("Cursor() = %d, want 1", e.Cursor())
	}
}

func TestLineEditor_deleteForward(t *testing.T) {
	e := NewLineEditor()
	e.SetText("hello")
	e.SetCursor(1)
	e.deleteForward()
	if e.Text() != "hllo" {
		t.Errorf("Text() = %q, want %q", e.Text(), "hllo")
	}
	if e.Cursor() != 1 {
		t.Errorf("Cursor() = %d, want 1", e.Cursor())
	}
}

func TestLineEditor_deleteForwardAtEnd(t *testing.T) {
	e := NewLineEditor()
	e.SetText("hello")
	e.SetCursor(5)
	e.deleteForward()
	if e.Text() != "hello" {
		t.Errorf("Text() = %q, want %q", e.Text(), "hello")
	}
}

func TestLineEditor_deleteToStart(t *testing.T) {
	e := NewLineEditor()
	e.SetText("hello")
	e.SetCursor(3)
	e.deleteToStart()
	if e.Text() != "lo" {
		t.Errorf("Text() = %q, want %q", e.Text(), "lo")
	}
	if e.Cursor() != 0 {
		t.Errorf("Cursor() = %d, want 0", e.Cursor())
	}
}

func TestLineEditor_deleteToEnd(t *testing.T) {
	e := NewLineEditor()
	e.SetText("hello")
	e.SetCursor(2)
	e.deleteToEnd()
	if e.Text() != "he" {
		t.Errorf("Text() = %q, want %q", e.Text(), "he")
	}
	if e.Cursor() != 2 {
		t.Errorf("Cursor() = %d, want 2", e.Cursor())
	}
}

func TestLineEditor_deleteWordBack(t *testing.T) {
	e := NewLineEditor()
	e.SetText("hello world")
	e.SetCursor(11)
	e.deleteWordBack()
	if e.Text() != "hello " {
		t.Errorf("Text() = %q, want %q", e.Text(), "hello ")
	}
	if e.Cursor() != 6 {
		t.Errorf("Cursor() = %d, want 6", e.Cursor())
	}
}

func TestLineEditor_deleteWordBackNoWord(t *testing.T) {
	e := NewLineEditor()
	e.SetText("hello ")
	e.SetCursor(6)
	e.deleteWordBack()
	if e.Text() != "hello" {
		t.Errorf("Text() = %q, want %q", e.Text(), "hello")
	}
}

func TestLineEditor_HandleKey_left(t *testing.T) {
	e := NewLineEditor()
	e.SetText("hello")
	e.HandleKey(KeyLeft)
	if e.Cursor() != 4 {
		t.Errorf("Cursor() = %d, want 4", e.Cursor())
	}
	e.SetCursor(0)
	e.HandleKey(KeyLeft)
	if e.Cursor() != 0 {
		t.Errorf("Cursor() = %d, want 0", e.Cursor())
	}
}

func TestLineEditor_HandleKey_right(t *testing.T) {
	e := NewLineEditor()
	e.SetText("hello")
	e.SetCursor(0)
	e.HandleKey(KeyRight)
	if e.Cursor() != 1 {
		t.Errorf("Cursor() = %d, want 1", e.Cursor())
	}
	e.SetCursor(5)
	e.HandleKey(KeyRight)
	if e.Cursor() != 5 {
		t.Errorf("Cursor() = %d, want 5", e.Cursor())
	}
}

func TestLineEditor_HandleKey_home(t *testing.T) {
	e := NewLineEditor()
	e.SetText("hello")
	e.HandleKey(KeyHome)
	if e.Cursor() != 0 {
		t.Errorf("Cursor() = %d, want 0", e.Cursor())
	}
}

func TestLineEditor_HandleKey_end(t *testing.T) {
	e := NewLineEditor()
	e.SetText("hello")
	e.SetCursor(0)
	e.HandleKey(KeyEnd)
	if e.Cursor() != 5 {
		t.Errorf("Cursor() = %d, want 5", e.Cursor())
	}
}

func TestLineEditor_HandleKey_backspace(t *testing.T) {
	e := NewLineEditor()
	e.SetText("hello")
	e.HandleKey(KeyBackspace)
	if e.Text() != "hell" {
		t.Errorf("Text() = %q, want %q", e.Text(), "hell")
	}
}

func TestLineEditor_HandleKey_delete(t *testing.T) {
	e := NewLineEditor()
	e.SetText("hello")
	e.SetCursor(1)
	e.HandleKey(KeyDelete)
	if e.Text() != "hllo" {
		t.Errorf("Text() = %q, want %q", e.Text(), "hllo")
	}
}

func TestLineEditor_HandleKey_ctrlD(t *testing.T) {
	e := NewLineEditor()
	e.SetText("hello")
	e.SetCursor(1)
	e.HandleKey(KeyCtrlD)
	if e.Text() != "hllo" {
		t.Errorf("Text() = %q, want %q", e.Text(), "hllo")
	}
	if e.Cursor() != 1 {
		t.Errorf("Cursor() = %d, want 1", e.Cursor())
	}
}

func TestLineEditor_HandleKey_ctrlDAtEnd(t *testing.T) {
	e := NewLineEditor()
	e.SetText("hello")
	e.SetCursor(5)
	e.HandleKey(KeyCtrlD)
	if e.Text() != "hello" {
		t.Errorf("Text() = %q, want %q", e.Text(), "hello")
	}
}
func TestLineEditor_HandleKey_ctrlU(t *testing.T) {
	e := NewLineEditor()
	e.SetText("hello")
	e.SetCursor(3)
	e.HandleKey(KeyCtrlU)
	if e.Text() != "lo" {
		t.Errorf("Text() = %q, want %q", e.Text(), "lo")
	}
}

func TestLineEditor_HandleKey_ctrlK(t *testing.T) {
	e := NewLineEditor()
	e.SetText("hello")
	e.SetCursor(2)
	e.HandleKey(KeyCtrlK)
	if e.Text() != "he" {
		t.Errorf("Text() = %q, want %q", e.Text(), "he")
	}
}

func TestLineEditor_HandleKey_ctrlW(t *testing.T) {
	e := NewLineEditor()
	e.SetText("hello world")
	e.SetCursor(11)
	e.HandleKey(KeyCtrlW)
	if e.Text() != "hello " {
		t.Errorf("Text() = %q, want %q", e.Text(), "hello ")
	}
}

func TestLineEditor_HandleKey_type(t *testing.T) {
	e := NewLineEditor()
	e.HandleKey("h")
	e.HandleKey("i")
	if e.Text() != "hi" {
		t.Errorf("Text() = %q, want %q", e.Text(), "hi")
	}
	if e.Cursor() != 2 {
		t.Errorf("Cursor() = %d, want 2", e.Cursor())
	}
}

func TestLineEditor_HandleKey_unknown(t *testing.T) {
	e := NewLineEditor()
	e.SetText("hello")
	if e.HandleKey("\x1b[unknown") {
		t.Error("HandleKey should return false for unknown key")
	}
	if e.Text() != "hello" {
		t.Errorf("Text() = %q, want %q", e.Text(), "hello")
	}
}

func TestLineEditor_UTF8(t *testing.T) {
	e := NewLineEditor()
	e.SetText("\u65e5\u672c\u8a9e")
	if e.Cursor() != 3 {
		t.Errorf("Cursor() = %d, want 3", e.Cursor())
	}
	e.HandleKey(KeyBackspace)
	if e.Text() != "\u65e5\u672c" {
		t.Errorf("Text() = %q, want %q", e.Text(), "\u65e5\u672c")
	}
	if e.Cursor() != 2 {
		t.Errorf("Cursor() = %d, want 2", e.Cursor())
	}
}

func TestLineEditor_TextBeforeCursor(t *testing.T) {
	e := NewLineEditor()
	e.SetText("hello world")
	e.SetCursor(5)
	if e.TextBeforeCursor() != "hello" {
		t.Errorf("TextBeforeCursor() = %q, want %q", e.TextBeforeCursor(), "hello")
	}
}

func TestLineEditor_SetCursor_clamp(t *testing.T) {
	e := NewLineEditor()
	e.SetText("hi")
	e.SetCursor(-1)
	if e.Cursor() != 0 {
		t.Errorf("Cursor() = %d, want 0", e.Cursor())
	}
	e.SetCursor(100)
	if e.Cursor() != 2 {
		t.Errorf("Cursor() = %d, want 2", e.Cursor())
	}
}

func TestLineEditor_currentPrefix(t *testing.T) {
	e := NewLineEditor()
	e.SetText("/mode y")
	e.SetCursor(7)
	prefix := e.currentPrefix()
	if prefix != "y" {
		t.Errorf("currentPrefix() = %q, want %q", prefix, "y")
	}
}

func TestLineEditor_completionTab(t *testing.T) {
	e := NewLineEditor()
	e.SetCompleter(&mockCompleter{results: []Completion{{Value: "hello", Display: "hello"}}})
	e.SetText("h")
	// First Tab triggers completion popup (does not auto-insert)
	e.HandleKey(KeyTab)
	if e.Text() != "h" {
		t.Errorf("Text() after first Tab = %q, want %q", e.Text(), "h")
	}
	if !e.compState.Active() {
		t.Error("compState should be active after first Tab")
	}
	// Second Tab accepts the completion
	e.HandleKey(KeyTab)
	if e.Text() != "hello" {
		t.Errorf("Text() after second Tab = %q, want %q", e.Text(), "hello")
	}
}

func TestLineEditor_completionTabNoCompleter(t *testing.T) {
	e := NewLineEditor()
	e.SetText("")
	e.HandleKey(KeyTab)
	if e.Text() != "  " {
		t.Errorf("Text() = %q, want %q", e.Text(), "  ")
	}
}

type mockCompleter struct {
	results []Completion
}

func (m *mockCompleter) Complete(prefix string) []Completion {
	return m.results
}

// TestLineEditor_deleteToEnd_MultiLine verifies ctrl-k kills only to the end
// of the current line, not the whole buffer (readline semantics).
func TestLineEditor_deleteToEnd_MultiLine(t *testing.T) {
	e := NewLineEditor()
	e.SetText("hello\nworld\nfoo")
	e.SetCursor(2) // mid first line
	e.deleteToEnd()
	if e.Text() != "he\nworld\nfoo" {
		t.Errorf("Text() = %q, want %q", e.Text(), "he\nworld\nfoo")
	}
	if e.Cursor() != 2 {
		t.Errorf("Cursor() = %d, want 2", e.Cursor())
	}

	// Cursor mid second line: only that line's tail is killed.
	e.SetText("hello\nworld\nfoo")
	e.SetCursor(8) // "hello\nwo|rld"
	e.deleteToEnd()
	if e.Text() != "hello\nwo\nfoo" {
		t.Errorf("Text() = %q, want %q", e.Text(), "hello\nwo\nfoo")
	}

	// Cursor on last line, no trailing newline: kills to buffer end.
	e.SetText("hello\nworld")
	e.SetCursor(7)
	e.deleteToEnd()
	if e.Text() != "hello\nw" {
		t.Errorf("Text() = %q, want %q", e.Text(), "hello\nw")
	}
}

// TestLineEditor_deleteToStart_MultiLine verifies ctrl-u kills only to the
// start of the current line, not the whole buffer (readline unix-line-discard).
func TestLineEditor_deleteToStart_MultiLine(t *testing.T) {
	e := NewLineEditor()
	e.SetText("hello\nworld\nfoo")
	e.SetCursor(9) // "hello\nwor|ld"
	e.deleteToStart()
	if e.Text() != "hello\nld\nfoo" {
		t.Errorf("Text() = %q, want %q", e.Text(), "hello\nld\nfoo")
	}
	if e.Cursor() != 6 { // start of second line
		t.Errorf("Cursor() = %d, want 6", e.Cursor())
	}

	// Cursor on first line: kills to buffer start (unchanged).
	e.SetText("hello\nworld")
	e.SetCursor(3)
	e.deleteToStart()
	if e.Text() != "lo\nworld" {
		t.Errorf("Text() = %q, want %q", e.Text(), "lo\nworld")
	}
	if e.Cursor() != 0 {
		t.Errorf("Cursor() = %d, want 0", e.Cursor())
	}
}
