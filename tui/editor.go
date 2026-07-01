// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"image"
	"strings"
	"time"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/ansi"
)

// saveClipboardImage saves a clipboard image to a temp file. It is swappable
// in tests.
var saveClipboardImage = internal.SaveClipboardImage

// Editor is a multi-line text editor component with readline behavior,
// undo/redo, kill-ring/yank, word navigation, and tab completion.
//
// The primary text input for the coding agent (heavily inspired by pi's Editor).
//
// Concurrency: the commandLoop is the sole owner of Editor state. HandleInput,
// Text/SetText/Clear/SetTitle/… and Render all run on the loop, serialized by
// single ownership (serialized by the commandLoop). User callbacks (onSubmit,
// OnEscape, OnImagePaste) are still batched and run after dispatch completes
// — not for lock safety (there is no lock) but to preserve the invariant that
// state is fully mutated before a host callback observes it. No mutex is
// required.
type Editor struct {
	buf     []rune
	pos     int // cursor position in grapheme clusters
	history []string
	histIdx int // -1 = editing new, >=0 = browsing history

	// pendingCallbacks collects callbacks queued during HandleInput dispatch
	// so they run after state mutation is complete.
	pendingCallbacks []func()

	// Visual
	prompt    string
	maxLines  int // max visible lines before internal scroll
	scroll    int // vertical scroll offset in visual lines
	lastWidth int // width used for last render (for visual line calc)

	// Message queue (pending messages merged by default)
	queue []string

	// compState unifies Tab-triggered and typing-triggered completion (A2 redesign).
	completer Completer
	compState CompState

	// Completion debounce: delays auto-completion while typing
	compTimer    *time.Timer
	compDebounce time.Duration

	// Abort channel for cancelling in-flight completion requests
	compAbort chan struct{}

	// TUI reference for showing overlays (completion popup, etc.) — deprecated, use inline list
	tui *TUI

	// Editing
	undo     *UndoStack
	killRing *KillRing

	// Callbacks
	onSubmit func(string)
	OnEscape func()

	focused bool
	kb      *KeybindingsManager

	// Jump mode (Ctrl+]/Ctrl+Alt+]) for character navigation
	jumpMode string // "forward", "backward", or ""

	// Paste tracking
	pasteCounter int
	pastes       map[int]string

	// thinkingLevel colors the editor border/separator lines.
	thinkingLevel string

	// title is an optional label rendered on the top border
	// (e.g. "───┨ title ┠───"). Used by RequestMainInput prompts.
	title string

	// OnImagePaste is called when an image is pasted from the clipboard.
	// If nil, the editor inserts a markdown image reference instead.
	OnImagePaste func(path string)

	// readClipboardImage reads an image from the clipboard. Swapped in tests.
	readClipboardImage func() (image.Image, error)

	// History draft: preserves current text when entering history browsing
	historyDraft *string

	// Sticky column for vertical cursor movement
	preferredVisualCol int  // -1 = unset
	preferredColSet    bool // explicit flag since 0 is a valid column

	// Undo coalescing: tracks the last action type for fish-style merging
	lastAction string // "type-word", "kill", "yank", or ""
}

// NewEditor creates a multi-line editor.
func NewEditor() *Editor {
	return &Editor{
		histIdx:            -1,
		prompt:             "",
		maxLines:           1,
		undo:               NewUndoStack(100),
		killRing:           NewKillRing(20),
		kb:                 DefaultKeybindingsManager(),
		compDebounce:       150 * time.Millisecond,
		compAbort:          make(chan struct{}),
		preferredVisualCol: -1,
		readClipboardImage: internal.ReadClipboardImage,
	}
}

// SetOnSubmit sets the submit callback. The callback is invoked AFTER mu is
// released, so it may safely re-enter the editor.
func (e *Editor) SetOnSubmit(fn func(string)) {
	e.onSubmit = fn
}

// SetCompleter sets the tab completion provider.
func (e *Editor) SetCompleter(c Completer) {
	e.completer = c
}

// UpdateCommandFreqs updates the completion provider's frequency order
// when the underlying command usage stats change. This makes recently-run
// commands appear in the "Most Used" completion tier immediately.
func (e *Editor) UpdateCommandFreqs(freqs map[string]int) {
	if cc, ok := e.completer.(*CommandCompleter); ok {
		cc.SetFreqOrder(freqs)
	}
}

// SetTUI sets the TUI reference for overlay support.
func (e *Editor) SetTUI(t *TUI) {
	e.tui = t
}

// SetMaxLines sets max visible lines before internal scrolling.
func (e *Editor) SetMaxLines(n int) {
	if n < 1 {
		n = 1
	}
	e.maxLines = n
}

// SetThinkingLevel sets the thinking level used to color the editor borders.
func (e *Editor) SetThinkingLevel(level string) {
	e.thinkingLevel = level
}

// SetTitle sets an optional label rendered on the top border.
// An empty string clears the title and restores a plain separator.
func (e *Editor) SetTitle(title string) {
	e.title = title
}

// Title returns the current top-border title.
func (e *Editor) Title() string {
	return e.title
}

// queueCallback appends a callback to be invoked after mu is released.
// Must be called with mu held (i.e. from within HandleInput's critical section).
func (e *Editor) queueCallback(fn func()) {
	e.pendingCallbacks = append(e.pendingCallbacks, fn)
}

// setPreferredCol sets the sticky column target for vertical cursor movement.
func (e *Editor) setPreferredCol(col int) {
	e.preferredVisualCol = col
	e.preferredColSet = true
}

// clearPreferredCol resets the sticky column tracking.
func (e *Editor) clearPreferredCol() {
	e.preferredVisualCol = -1
	e.preferredColSet = false
}

// Text returns the full buffer content.
func (e *Editor) Text() string {
	return string(e.buf)
}

// expandPasteMarkers replaces [paste #N ...] markers with the actual pasted
// content stored in e.pastes. This ensures the submitted text contains the
// real content, not the display marker.
func (e *Editor) expandPasteMarkers(text string) string {
	if len(e.pastes) == 0 {
		return text
	}
	var result strings.Builder
	for i := 0; i < len(text); {
		if text[i] != '[' {
			result.WriteByte(text[i])
			i++
			continue
		}
		endIdx := strings.IndexByte(text[i:], ']')
		if endIdx < 0 {
			result.WriteByte(text[i])
			i++
			continue
		}
		marker := text[i : i+endIdx+1]
		if id, ok := parsePasteMarkerID(marker); ok {
			if content, exists := e.pastes[id]; exists {
				result.WriteString(content)
				i += endIdx + 1
				continue
			}
		}
		result.WriteByte(text[i])
		i++
	}
	return result.String()
}

// parsePasteMarkerID extracts the numeric id from a marker like
// "[paste #1 +11 lines]" or "[paste #1 123 chars]".
func parsePasteMarkerID(marker string) (int, bool) {
	if !strings.HasPrefix(marker, "[paste #") {
		return 0, false
	}
	rest := marker[len("[paste #"):]
	var id int
	_, err := fmt.Sscanf(rest, "%d", &id)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// SetText replaces the buffer and resets position.
func (e *Editor) SetText(s string) {
	e.setTextLocked(s)
}

// setTextLocked replaces the buffer and resets position. Internal helper;
// runs on the commandLoop like every Editor method.
func (e *Editor) setTextLocked(s string) {
	e.buf = []rune(s)
	e.pos = len(e.buf)
	e.scroll = 0
}

// Clear empties the buffer.
func (e *Editor) Clear() {
	e.clearLocked()
}

// clearLocked empties the buffer. Internal helper; runs on the commandLoop.
func (e *Editor) clearLocked() {
	e.buf = nil
	e.pos = 0
	e.histIdx = -1
	e.scroll = 0
	e.clearCompletion()
	e.historyDraft = nil
	e.preferredVisualCol = -1
	e.preferredColSet = false
	e.lastAction = ""
}

// HandleInput processes keyboard input with readline-like behavior. Runs on
// the commandLoop; user callbacks are batched and run after dispatch so state
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
		if e.jumpMode == "forward" {
			targetStr := string(e.buf[e.pos:])
			if idx := strings.Index(targetStr, string(runes[0])); idx >= 0 {
				e.pos += idx + 1
			}
		} else {
			targetStr := string(e.buf[:e.pos])
			if idx := strings.LastIndex(targetStr, string(runes[0])); idx >= 0 {
				e.pos = idx + 1
			}
		}
		e.jumpMode = ""
		return true
	}
	e.jumpMode = ""
	return false
}

func (e *Editor) insertRune(r rune) {
	e.clearPreferredCol()
	e.buf = append(e.buf[:e.pos], append([]rune{r}, e.buf[e.pos:]...)...)
	e.pos++
}

func (e *Editor) insertString(s string) {
	for _, r := range s {
		e.insertRune(r)
	}
}

// InsertTextAtCursor inserts text at the current cursor position.
func (e *Editor) InsertTextAtCursor(text string) {
	if text == "" {
		return
	}
	e.pushUndo()
	e.lastAction = ""
	e.insertString(text)
	e.clearCompletion()
}

func (e *Editor) insertNewline() {
	e.pushUndo()
	e.insertRune('\n')
}

// looksLikePaste reports whether a single input event is probably pasted text.
func (e *Editor) looksLikePaste(data string) bool {
	if len(data) <= 1 {
		return false
	}
	// Multi-line or tab-containing input is almost certainly a paste.
	return strings.ContainsAny(data, "\n\t\r")
}

// handlePaste inserts pasted text at the cursor. If the clipboard contains
// an image, it is saved to a temp file and either handed to OnImagePaste or
// inserted as a markdown image reference. Large text pastes become a
// collapsible marker; smaller pastes are inserted inline.
func (e *Editor) handlePaste(text string) {
	if path, ok := e.tryPasteImage(); ok {
		if e.OnImagePaste != nil {
			cb := e.OnImagePaste
			p := path
			e.queueCallback(func() { cb(p) })
			return
		}
		e.insertImageReference(path)
		return
	}

	normalized := e.normalizePastedText(text)
	lines := strings.Count(normalized, "\n")
	if lines > 10 || len(normalized) > 1000 {
		e.insertPasteMarker(normalized)
		return
	}
	e.pushUndo()
	e.insertString(normalized)
	e.clearCompletion()
}

// tryPasteImage attempts to read and save an image from the clipboard.
// It returns the saved file path and true on success.
func (e *Editor) tryPasteImage() (string, bool) {
	if e.readClipboardImage == nil {
		return "", false
	}
	img, err := e.readClipboardImage()
	if err != nil || img == nil {
		return "", false
	}
	path, err := saveClipboardImage(img)
	if err != nil {
		return "", false
	}
	return path, true
}

// insertImageReference inserts the image file path at the cursor so the
// agent receives it as an attachment.
func (e *Editor) insertImageReference(path string) {
	e.pushUndo()
	e.insertString(path)
	e.clearCompletion()
}

// normalizePastedText cleans raw pasted bytes for editor storage.
func (e *Editor) normalizePastedText(text string) string {
	text = ansi.Strip(text)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.ReplaceAll(text, "\t", "  ")
	return e.filterPastedText(text)
}

// filterPastedText removes control characters except newlines.
func (e *Editor) filterPastedText(text string) string {
	var b strings.Builder
	for _, r := range text {
		if r == '\n' || r >= 32 {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// insertPasteMarker creates a condensed marker for large pasted content.
func (e *Editor) insertPasteMarker(text string) {
	e.pushUndo()
	e.pasteCounter++
	id := e.pasteCounter
	if e.pastes == nil {
		e.pastes = make(map[int]string)
	}
	e.pastes[id] = text
	lines := strings.Count(text, "\n")
	var marker string
	if lines > 10 {
		marker = fmt.Sprintf("[paste #%d +%d lines]", id, lines)
	} else {
		marker = fmt.Sprintf("[paste #%d %d chars]", id, len(text))
	}
	for _, r := range marker {
		e.buf = append(e.buf[:e.pos], append([]rune{r}, e.buf[e.pos:]...)...)
		e.pos++
	}
}

func (e *Editor) backspace() {
	if e.pos <= 0 {
		return
	}
	e.pushUndo()
	e.lastAction = ""
	start := PrevGraphemeStart(string(e.buf), e.pos)
	e.buf = append(e.buf[:start], e.buf[e.pos:]...)
	e.pos = start
	e.clearPreferredCol()
	e.updateAutoComp()
}

func (e *Editor) deleteForward() {
	if e.pos >= len(e.buf) {
		return
	}
	e.pushUndo()
	e.lastAction = ""
	end := NextGraphemeEnd(string(e.buf), e.pos)
	e.buf = append(e.buf[:e.pos], e.buf[end:]...)
}

func (e *Editor) moveLeft() {
	if e.pos <= 0 {
		return
	}
	e.clearPreferredCol()
	e.pos = e.prevGraphemeOrMarker(string(e.buf), e.pos)
}

func (e *Editor) moveRight() {
	if e.pos >= len(e.buf) {
		return
	}
	e.clearPreferredCol()
	e.pos = e.nextGraphemeOrMarker(string(e.buf), e.pos)
}

// prevGraphemeOrMarker moves left by one grapheme cluster, but skips paste
// markers entirely in one jump (they are treated as atomic single units).
func (e *Editor) prevGraphemeOrMarker(s string, pos int) int {
	prev := PrevGraphemeStart(s, pos)
	if markerStart, ok := findPasteMarkerStartBefore(s, prev); ok {
		return markerStart
	}
	return prev
}

// findPasteMarkerStartBefore searches for a "[paste #...]" marker ending at prev.
func findPasteMarkerStartBefore(s string, prev int) (int, bool) {
	if prev <= 0 || s[prev-1] != ']' {
		return 0, false
	}
	for j := prev - 2; j >= 7; j-- {
		if s[j] != '[' {
			continue
		}
		if !strings.HasPrefix(s[j:], "[paste #") {
			continue
		}
		endIdx := strings.IndexByte(s[j:], ']')
		if endIdx >= 0 && j+endIdx+1 == prev {
			return j, true
		}
	}
	return 0, false
}

// nextGraphemeOrMarker moves right by one grapheme cluster, but skips paste
// markers entirely in one jump.
func (e *Editor) nextGraphemeOrMarker(s string, pos int) int {
	next := NextGraphemeEnd(s, pos)
	// If the text starting at pos is a paste marker, jump to after it
	if pos < len(s) && strings.HasPrefix(s[pos:], "[paste #") {
		endIdx := strings.IndexByte(s[pos:], ']')
		if endIdx >= 0 {
			return pos + endIdx + 1
		}
	}
	return next
}

func (e *Editor) lineUp() {
	width := e.lastWidth
	if width <= 0 {
		width = 80
	}
	vlm := buildVisualLineMap(string(e.buf), width)
	currentVL := findVisualLine(vlm, e.pos)
	if currentVL <= 0 {
		e.pos = 0
		return
	}
	currentVisCol := e.pos - vlm[currentVL].bufStart
	sourceMaxVis := vlm[currentVL].runeCount
	targetVL := vlm[currentVL-1]
	targetMaxVis := targetVL.runeCount

	// Sticky column logic
	var moveToCol int
	if !e.preferredColSet || currentVisCol < sourceMaxVis-1 {
		// P=0 or S=1 (cursor in middle of source line)
		if targetMaxVis < currentVisCol {
			// T=1: target shorter — set preferred, go to end
			e.setPreferredCol(currentVisCol)
			moveToCol = targetMaxVis - 1
		} else {
			// T=0: target fits
			e.clearPreferredCol()
			moveToCol = currentVisCol
		}
	} else {
		// P=1, S=0 (cursor was clamped to end of source line)
		if targetMaxVis < currentVisCol || targetMaxVis < e.preferredVisualCol {
			// T=1 or U=1: target can't fit preferred
			moveToCol = targetMaxVis - 1
		} else {
			// T=0, U=0: target fits preferred
			moveToCol = e.preferredVisualCol
			e.clearPreferredCol()
		}
	}
	if moveToCol < 0 {
		moveToCol = 0
	}
	e.pos = targetVL.bufStart + moveToCol
	if e.pos < 0 {
		e.pos = 0
	}

	// Adjust scroll so the new cursor position is visible
	e.adjustScrollToCursor()
}

func (e *Editor) lineDown() {
	width := e.lastWidth
	if width <= 0 {
		width = 80
	}
	vlm := buildVisualLineMap(string(e.buf), width)
	currentVL := findVisualLine(vlm, e.pos)
	if currentVL >= len(vlm)-1 {
		e.pos = len(e.buf)
		return
	}
	currentVisCol := e.pos - vlm[currentVL].bufStart
	sourceMaxVis := vlm[currentVL].runeCount
	targetVL := vlm[currentVL+1]
	targetMaxVis := targetVL.runeCount

	// Sticky column logic
	var moveToCol int
	if !e.preferredColSet || currentVisCol < sourceMaxVis-1 {
		if targetMaxVis < currentVisCol {
			e.setPreferredCol(currentVisCol)
			moveToCol = targetMaxVis - 1
		} else {
			e.clearPreferredCol()
			moveToCol = currentVisCol
		}
	} else {
		if targetMaxVis < currentVisCol || targetMaxVis < e.preferredVisualCol {
			moveToCol = targetMaxVis - 1
		} else {
			moveToCol = e.preferredVisualCol
			e.clearPreferredCol()
		}
	}
	if moveToCol < 0 {
		moveToCol = 0
	}
	e.pos = targetVL.bufStart + moveToCol
	if e.pos < 0 {
		e.pos = 0
	}

	// Adjust scroll so the new cursor position is visible
	e.adjustScrollToCursor()
}

// adjustScrollToCursor adjusts e.scroll so the cursor is visible.
// Called after cursor movement operations.
func (e *Editor) adjustScrollToCursor() {
	width := e.lastWidth
	if width <= 0 {
		width = 80
	}
	fullText := e.prompt + string(e.buf)
	wrapped := wrapText(fullText, width)
	totalVisLines := len(wrapped)
	cursorFullPos := len(e.prompt) + e.pos
	cursorVisLine, _ := visualCursorPos(fullText, cursorFullPos, width)

	if cursorVisLine < e.scroll {
		e.scroll = cursorVisLine
	} else if cursorVisLine >= e.scroll+e.maxLines {
		e.scroll = cursorVisLine - e.maxLines + 1
	}

	maxScroll := totalVisLines - e.maxLines
	if e.scroll > maxScroll {
		e.scroll = maxScroll
	}
	if e.scroll < 0 {
		e.scroll = 0
	}
}

func (e *Editor) pageScroll(direction int) {
	// Page scroll moves cursor by approximately maxVisibleLines in the direction.
	termRows := 24
	if e.tui != nil {
		tr := e.tui.TerminalRows()
		if tr > 5 {
			termRows = tr
		}
	}
	pageSize := maxEditorLines(termRows)
	if pageSize < 1 {
		pageSize = 5
	}
	targetLine := cursorLogicalLine(string(e.buf), e.pos) + direction*pageSize
	if targetLine < 0 {
		targetLine = 0
	}
	lineCount := 0
	for i, ch := range e.buf {
		if lineCount == targetLine {
			e.pos = i
			return
		}
		if ch == '\n' {
			lineCount++
		}
	}
	e.pos = len(e.buf)
}

// navigateHistory browses the editor history.
// direction: -1 = older (↑), 1 = newer (↓).
// Preserves the current editing text as a draft so it can be restored
// when the user returns.
// History is stored oldest-first; the most recent entry is at len-1, so Up
// recalls the newest entry first.
func (e *Editor) navigateHistory(direction int) {
	if len(e.history) == 0 {
		return
	}

	newIdx := e.nextHistoryIndex(direction)
	if newIdx < -1 {
		return
	}

	if e.histIdx == -1 && newIdx >= 0 {
		draft := string(e.buf)
		e.historyDraft = &draft
		e.pushUndo()
	}

	e.histIdx = newIdx
	e.applyHistoryIndex()
}

func (e *Editor) nextHistoryIndex(direction int) int {
	switch {
	case e.histIdx == -1 && direction == -1:
		return len(e.history) - 1
	case e.histIdx == -1 && direction == 1:
		return -2 // sentinel: no-op
	default:
		newIdx := e.histIdx + direction
		if newIdx >= len(e.history) {
			return -1
		}
		return newIdx
	}
}

func (e *Editor) applyHistoryIndex() {
	if e.histIdx == -1 {
		if e.historyDraft != nil {
			e.setTextLocked(*e.historyDraft)
			e.historyDraft = nil
		} else {
			e.clearLocked()
		}
		return
	}
	e.setTextLocked(e.history[e.histIdx])
}

// isOnFirstVisualLine returns true if the cursor is on the first visual line.
// Uses buildVisualLineMap for correct visual line detection with wrapped lines.
func (e *Editor) isOnFirstVisualLine() bool {
	width := e.lastWidth
	if width <= 0 {
		width = 80
	}
	vlm := buildVisualLineMap(string(e.buf), width)
	if len(vlm) == 0 {
		return true
	}
	currentVL := findVisualLine(vlm, e.pos)
	return currentVL <= 0
}

// isOnLastVisualLine returns true if the cursor is on the last visual line.
// Uses buildVisualLineMap for correct visual line detection with wrapped lines.
func (e *Editor) isOnLastVisualLine() bool {
	width := e.lastWidth
	if width <= 0 {
		width = 80
	}
	vlm := buildVisualLineMap(string(e.buf), width)
	if len(vlm) == 0 {
		return true
	}
	currentVL := findVisualLine(vlm, e.pos)
	return currentVL >= len(vlm)-1
}

func (e *Editor) wordLeft() {
	e.clearPreferredCol()
	e.pos = findWordBackward(string(e.buf), e.pos)
}

func (e *Editor) wordRight() {
	e.clearPreferredCol()
	e.pos = findWordForward(string(e.buf), e.pos)
}
