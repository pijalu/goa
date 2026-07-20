// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import "strings"

// LineEditor is a reusable single-line text editor with cursor movement,
// deletion, insertion, and optional tab completion. It is the editing core
// shared by Input, Editor, and any component that needs text entry.
type LineEditor struct {
	buf       []rune
	pos       int // rune index into buf
	completer Completer
	compState CompState
	onChange  func(string)
}

// NewLineEditor creates a new LineEditor.
func NewLineEditor() *LineEditor {
	return &LineEditor{}
}

// Text returns the current buffer content.
func (e *LineEditor) Text() string {
	return string(e.buf)
}

// SetText replaces the buffer content and moves the cursor to the end.
func (e *LineEditor) SetText(s string) {
	e.buf = []rune(s)
	e.pos = len(e.buf)
}

// Clear empties the buffer and resets the cursor.
func (e *LineEditor) Clear() {
	e.buf = nil
	e.pos = 0
}

// Cursor returns the current cursor position in runes.
func (e *LineEditor) Cursor() int {
	return e.pos
}

// SetCursor sets the cursor position in runes (clamped to buffer bounds).
func (e *LineEditor) SetCursor(p int) {
	if p < 0 {
		p = 0
	}
	if p > len(e.buf) {
		p = len(e.buf)
	}
	e.pos = p
}

// TextBeforeCursor returns the text before the cursor position.
func (e *LineEditor) TextBeforeCursor() string {
	return string(e.buf[:e.pos])
}

// SetCompleter sets the tab completion provider. Pass nil to disable.
func (e *LineEditor) SetCompleter(c Completer) {
	e.completer = c
}

// Completer returns the current completer, or nil.
func (e *LineEditor) Completer() Completer {
	return e.completer
}

// SetOnChange sets a callback invoked after each text-changing operation.
func (e *LineEditor) SetOnChange(fn func(string)) {
	e.onChange = fn
}

// CompState returns a pointer to the completion state (for rendering popups).
func (e *LineEditor) CompState() *CompState {
	return &e.compState
}

// HandleKey processes a single key event. It returns true if the key was
// consumed (handled), false otherwise. Callers should delegate Enter, Escape,
// Up, Down, and other navigation keys before calling HandleKey.
func (e *LineEditor) HandleKey(key string) bool {
	if e.handleMovementKey(key) {
		return true
	}
	if e.handleDeletionKey(key) {
		return true
	}
	if e.handleTabKey(key) {
		return true
	}
	return e.handleTextKey(key)
}

func (e *LineEditor) handleMovementKey(key string) bool {
	switch {
	case matchesKey(key, KeyLeft):
		if e.pos > 0 {
			e.pos--
		}
	case matchesKey(key, KeyRight):
		if e.pos < len(e.buf) {
			e.pos++
		}
	case matchesKey(key, KeyHome):
		e.pos = 0
	case matchesKey(key, KeyEnd):
		e.pos = len(e.buf)
	default:
		return false
	}
	return true
}

func (e *LineEditor) handleDeletionKey(key string) bool {
	switch {
	case matchesKey(key, KeyBackspace):
		e.backspace()
		e.updateAutoComp()
	case matchesKey(key, KeyDelete):
		e.deleteForward()
	case matchesKey(key, KeyCtrlD):
		e.deleteForward()
	case matchesKey(key, KeyCtrlU):
		e.deleteToStart()
	case matchesKey(key, KeyCtrlK):
		e.deleteToEnd()
	case matchesKey(key, KeyCtrlW):
		e.deleteWordBack()
		e.updateAutoComp()
	default:
		return false
	}
	return true
}

func (e *LineEditor) handleTabKey(key string) bool {
	if !matchesKey(key, KeyTab) {
		return false
	}
	e.handleTab()
	return true
}

func (e *LineEditor) handleTextKey(key string) bool {
	if len(key) == 1 && key[0] >= 32 && key[0] < 127 {
		e.insert(rune(key[0]))
		e.onCharInserted(rune(key[0]))
		return true
	}
	if len(key) > 1 && !strings.HasPrefix(key, "\x1b[") {
		for _, r := range key {
			if r >= 32 && r < 127 {
				e.insert(r)
				e.onCharInserted(r)
			}
		}
		return true
	}
	return false
}

// -- internal editing operations ----------------------------------

func (e *LineEditor) insert(r rune) {
	e.buf = append(e.buf[:e.pos], append([]rune{r}, e.buf[e.pos:]...)...)
	e.pos++
}

func (e *LineEditor) backspace() {
	if e.pos <= 0 {
		return
	}
	e.buf = append(e.buf[:e.pos-1], e.buf[e.pos:]...)
	e.pos--
}

func (e *LineEditor) deleteForward() {
	if e.pos >= len(e.buf) {
		return
	}
	e.buf = append(e.buf[:e.pos], e.buf[e.pos+1:]...)
}

func (e *LineEditor) deleteToStart() {
	if e.pos <= 0 {
		return
	}
	// Kill to the start of the current line only (readline unix-line-discard),
	// preserving any preceding lines.
	start := findLineStart(string(e.buf), e.pos)
	e.buf = append(e.buf[:start], e.buf[e.pos:]...)
	e.pos = start
}

func (e *LineEditor) deleteToEnd() {
	if e.pos >= len(e.buf) {
		return
	}
	// Kill to the end of the current line only (readline kill-line),
	// preserving any following lines.
	end := findLineEnd(string(e.buf), e.pos)
	e.buf = append(e.buf[:e.pos], e.buf[end:]...)
}

func (e *LineEditor) deleteWordBack() {
	if e.pos <= 0 {
		return
	}
	pos := e.pos
	for pos > 0 && isWordRune(e.buf[pos-1]) {
		pos--
	}
	if pos == e.pos {
		e.backspace()
		return
	}
	e.buf = append(e.buf[:pos], e.buf[e.pos:]...)
	e.pos = pos
}

func isWordRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') || r == '_'
}

// -- completion ---------------------------------------------------

func (e *LineEditor) handleTab() {
	if e.completer == nil {
		e.insert(' ')
		e.insert(' ')
		return
	}
	if e.compState.Active() {
		e.acceptAndRecomplete()
		return
	}
	prefix := e.currentPrefix()
	items := e.completer.Complete(prefix)
	if len(items) == 0 {
		return
	}
	e.compState.Phase = PhaseCommand
	e.compState.Items = items
	e.compState.Idx = 0
	e.compState.Prefix = prefix
}

func (e *LineEditor) acceptAndRecomplete() {
	if !e.compState.Active() {
		return
	}
	sel := e.compState.Selected()
	if sel == nil {
		return
	}
	e.applyCompletion(e.compState.Prefix, sel.Value)
	e.updateAutoComp()
}

func (e *LineEditor) applyCompletion(oldPrefix, newValue string) {
	text := string(e.buf)
	prefixLen := len([]rune(oldPrefix))
	before := e.pos - prefixLen
	if before < 0 {
		before = 0
	}
	newText := string([]rune(text)[:before]) + newValue + string([]rune(text)[e.pos:])
	e.buf = []rune(newText)
	e.pos = before + len([]rune(newValue))
}

func (e *LineEditor) currentPrefix() string {
	text := string(e.buf[:e.pos])
	if text == "" {
		return ""
	}
	if quoteStart := findUnclosedQuote(text); quoteStart >= 0 {
		if isTokenStart(text, quoteStart) {
			return text[quoteStart:]
		}
	}
	if atIdx := strings.LastIndex(text, "@"); atIdx >= 0 && isTokenStart(text, atIdx) {
		return text[atIdx:]
	}
	last := strings.LastIndexAny(text, " \t\n'=")
	if last >= 0 {
		return text[last+1:]
	}
	return text
}

func (e *LineEditor) clearCompletion() {
	e.compState.Clear()
}

func (e *LineEditor) onCharInserted(r rune) {
	if r == ':' && e.compState.Phase == PhaseAccepted {
		e.compState.Phase = PhaseArg
	}
	e.updateAutoComp()
}

func (e *LineEditor) updateAutoComp() {
	if e.completer == nil {
		e.clearCompletion()
		return
	}
	prefix := e.currentPrefix()
	if prefix == "" || len(prefix) < 1 {
		e.clearCompletion()
		return
	}
	e.scheduleAutoComp()
}

func (e *LineEditor) scheduleAutoComp() {
	e.compState.Trigger = "regular"
}
