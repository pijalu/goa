// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import "strings"

// is fully mutated before the host observes it.
func (e *Editor) HandleInput(data string) {
	cbs := e.handleInputLocked(data)
	for _, cb := range cbs {
		cb()
	}
}

// handleInputLocked performs input dispatch. It returns a slice of callbacks
// that must be executed after dispatch completes (state fully mutated).
func (e *Editor) handleInputLocked(data string) []func() {
	e.pendingCallbacks = nil
	if !e.focused {
		return nil
	}

	if e.handleCompletionInput(data) {
		return e.pendingCallbacks
	}

	if e.handleJumpMode(data) {
		return e.pendingCallbacks
	}

	// Paste detection: bracketed-paste content arrives as a single raw string.
	// Multi-line or large inputs are inserted as pasted text (or a marker).
	// We allow leading escape sequences (e.g. ANSI color from terminal output)
	// as long as the event is long enough or contains paste-like content.
	if e.looksLikePaste(data) || len(data) > 1000 || strings.Count(data, "\n") > 10 {
		e.handlePaste(data)
		return e.pendingCallbacks
	}

	if e.handleControlKeys(data) {
		return e.pendingCallbacks
	}
	if e.handleEditKeys(data) {
		return e.pendingCallbacks
	}
	if e.handleHistoryKeys(data) {
		return e.pendingCallbacks
	}
	if e.handleCursorKeys(data) {
		return e.pendingCallbacks
	}
	if isPrintable(data) {
		e.handlePrintable(data)
	}
	return e.pendingCallbacks
}

// handleControlKeys handles special control keys (Ctrl+D, Esc, PageUp/Down,
// and jump-mode triggers). Returns true if consumed.
func (e *Editor) handleControlKeys(data string) bool {
	switch {
	case matchesKey(data, KeyCtrlD):
		if len(e.buf) == 0 && e.tui != nil {
			t := e.tui
			e.queueCallback(func() { t.Stop() })
			return true
		}
		return false // Let handleEditKeys process it as delete-forward
	case matchesKey(data, KeyEscape):
		if e.OnEscape != nil {
			cb := e.OnEscape
			e.queueCallback(func() { cb() })
		}
		e.clearCompletion()
		return true
	case matchesKey(data, KeyPageUp):
		e.pageScroll(-1)
		return true
	case matchesKey(data, KeyPageDown):
		e.pageScroll(1)
		return true
	case matchesKey(data, "\x1d"):
		e.jumpMode = "forward"
		return true
	case matchesKey(data, "ctrl+alt+]"):
		e.jumpMode = "backward"
		return true
	}
	return false
}

// handleEditKeys handles editing keys (submit, newline, delete, kill).
// Returns true if consumed.
func (e *Editor) handleEditKeys(data string) bool {
	switch {
	case e.kb.Matches(data, KbSubmit):
		e.submit()
		return true
	case e.kb.Matches(data, KbNewLine):
		e.insertNewline()
		return true
	case e.kb.Matches(data, KbDeleteBackward):
		e.backspace()
		return true
	case e.kb.Matches(data, KbDeleteForward):
		e.deleteForward()
		return true
	case e.kb.Matches(data, KbDeleteWordBack):
		e.killWordBack()
		return true
	case e.kb.Matches(data, KbDeleteWordFwd):
		e.killWordForward()
		return true
	case e.kb.Matches(data, KbDeleteLineStart):
		e.killToStart()
		return true
	case e.kb.Matches(data, KbDeleteLineEnd):
		e.killToEnd()
		return true
	}
	return false
}

// handleHistoryKeys handles history/recall keys (yank, yank-pop, undo) and the
// Tab completion trigger. Returns true if consumed.
func (e *Editor) handleHistoryKeys(data string) bool {
	switch {
	case e.kb.Matches(data, KbYank):
		e.yank()
		return true
	case e.kb.Matches(data, KbYankPop):
		e.yankPop()
		return true
	case e.kb.Matches(data, KbUndo):
		e.doUndo()
		return true
	case e.kb.Matches(data, KbTab):
		e.triggerCompletion()
		return true
	}
	return false
}

// handleCursorKeys handles cursor movement keys. Returns true if consumed.
func (e *Editor) handleCursorKeys(data string) bool {
	switch {
	case e.kb.Matches(data, KbCursorLeft):
		e.moveLeft()
		return true
	case e.kb.Matches(data, KbCursorRight):
		e.moveRight()
		return true
	case e.kb.Matches(data, KbCursorUp) || matchesKey(data, KeyUp):
		e.handleCursorUp()
		return true
	case e.kb.Matches(data, KbCursorDown) || matchesKey(data, KeyDown):
		e.handleCursorDown()
		return true
	case e.kb.Matches(data, KbCursorWordLeft):
		e.wordLeft()
		return true
	case e.kb.Matches(data, KbCursorWordRight):
		e.wordRight()
		return true
	case e.kb.Matches(data, KbCursorLineStart):
		e.clearPreferredCol()
		e.pos = findLineStart(string(e.buf), e.pos)
		return true
	case e.kb.Matches(data, KbCursorLineEnd):
		e.clearPreferredCol()
		e.pos = findLineEnd(string(e.buf), e.pos)
		return true
	}
	return false
}

// handleCursorUp handles Up arrow: history browsing or visual line up.
func (e *Editor) handleCursorUp() {
	if len(e.buf) == 0 {
		e.navigateHistory(-1)
	} else if e.histIdx > -1 && e.isOnFirstVisualLine() {
		e.navigateHistory(-1)
	} else if e.isOnFirstVisualLine() {
		e.clearPreferredCol()
		e.pos = findLineStart(string(e.buf), e.pos)
		e.adjustScrollToCursor()
	} else {
		e.lineUp()
	}
}

// handleCursorDown handles Down arrow: history browsing or visual line down.
func (e *Editor) handleCursorDown() {
	if e.histIdx > -1 && e.isOnLastVisualLine() {
		e.navigateHistory(1)
	} else if len(e.buf) == 0 && e.histIdx > -1 {
		// Empty buffer while still in history browsing mode: allow Down to
		// return to the newer entry / empty line even if visual-line checks
		// would not trigger.
		e.navigateHistory(1)
	} else if e.isOnLastVisualLine() {
		e.clearPreferredCol()
		e.pos = findLineEnd(string(e.buf), e.pos)
		e.adjustScrollToCursor()
	} else {
		e.lineDown()
	}
}

// handlePrintable processes printable character input with fish-style undo coalescing.
// Extracted from HandleInput for complexity.
func (e *Editor) handlePrintable(data string) {
	ch := data[0]
	isSpace := ch == ' '

	// Fish-style undo coalescing:
	// - Consecutive word characters coalesce into one undo unit
	// - Space captures state before itself (so undo removes space+word)
	// - Whitespace resets the coalescing
	if isSpace || e.lastAction != "type-word" {
		e.pushUndo()
	}
	e.lastAction = "type-word"

	for _, r := range data {
		if r == '\t' {
			e.insertString("  ")
		} else {
			e.insertRune(r)
		}
	}
	e.clearCompletion()
	e.scheduleAutoComp()
}

func isPrintable(data string) bool {
	if len(data) == 0 {
		return false
	}
	if strings.HasPrefix(data, "\x1b") {
		return false
	}
	for _, r := range data {
		if r < 32 && r != '\t' {
			return false
		}
	}
	return true
}

// ── Buffer operations ──

// handleCompletionInput returns true if the key was consumed by the completion popup.
// Extracted from HandleInput to reduce cognitive complexity.
func (e *Editor) handleCompletionInput(data string) bool {
	if !e.compState.Active() {
		return false
	}
	switch {
	case matchesKey(data, KeyDown):
		e.cycleCompletion(1)
		return true
	case matchesKey(data, KeyUp):
		e.cycleCompletion(-1)
		return true
	case matchesKey(data, KeyTab):
		e.acceptAndRecomplete()
		return true
	case matchesKey(data, KeyEnter):
		// Slash commands: Enter always accepts the currently selected
		// completion candidate before submitting, so a non-existent or
		// partial command expands to the highlighted item from the popup.
		// Non-slash completions: Enter accepts the selected item without submitting.
		if strings.HasPrefix(e.compState.Prefix, "/") {
			if sel := e.compState.Selected(); sel != nil {
				e.pushUndo()
				e.replacePrefix(e.compState.Prefix, sel.Value)
			}
			e.clearCompletion()
			e.submit()
		} else {
			e.acceptCompletion()
		}
		return true
	case matchesKey(data, KeyEscape):
		e.clearCompletion()
		return true
	}
	return false
}

// handleJumpMode returns true if the key was consumed by jump-mode navigation.
func (e *Editor) handleJumpMode(data string) bool {
	if e.jumpMode == "" {
		return false
	}
	if isPrintable(data) && !strings.HasPrefix(data, "\x1b") {
		runes := []rune(data)
		char := string(runes[0])
		full := string(e.buf)
		if e.jumpMode == "forward" {
			bytePos := RuneIndexToBytePos(full, e.pos)
			idx := strings.Index(full[bytePos:], char)
			if idx >= 0 {
				e.pos = BytePosToRuneIndex(full, bytePos+idx+len(char))
			}
		} else {
			bytePos := RuneIndexToBytePos(full, e.pos)
			idx := strings.LastIndex(full[:bytePos], char)
			if idx >= 0 {
				e.pos = BytePosToRuneIndex(full, idx+len(char))
			}
		}
		e.jumpMode = ""
		return true
	}
	e.jumpMode = ""
	return false
}
