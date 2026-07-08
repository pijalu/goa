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

	// stableMaxLines tracks the largest maxLines value seen since the last
	// terminal resize. The editor never renders below this height so that the
	// input line and the footer below it do not jump up when the buffer shrinks.
	stableMaxLines int

	// lastTerminalRows is used to detect terminal resizes and reset
	// stableMaxLines when the screen dimensions change.
	lastTerminalRows int

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
//
// A single trailing colon (optionally preceded by whitespace) is stripped so
// prompts such as "Describe the issue:" do not render as "┨ Describe the
// issue: ┠" — the colon collides visually with the closing "┠" bracket drawn
// by renderTitledBorder. Only the visual title is normalized; callers that
// need the original prompt text should keep their own copy (e.g. the chat
// system message).
func (e *Editor) SetTitle(title string) {
	e.title = normalizeEditorTitle(title)
}

// normalizeEditorTitle trims surrounding whitespace and removes a single
// trailing colon (with any preceding space) so the bordered title does not end
// with ":" adjacent to the closing "┠" bracket. Only one trailing colon is
// stripped ("a::" → "a:"); leading/internal colons are preserved.
func normalizeEditorTitle(title string) string {
	t := strings.TrimSpace(title)
	if strings.HasSuffix(t, ":") {
		t = strings.TrimSpace(t[:len(t)-1])
	}
	return t
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

// VisualCursor returns the cursor's current visual line and column based on
// the editor's prompt and buffer content at the given width. This is useful
// for tests and agentic debugging to verify cursor placement without parsing
// terminal output.
func (e *Editor) VisualCursor(width int) (line, col int) {
	if width <= 0 {
		width = e.lastWidth
	}
	if width <= 0 {
		width = 80
	}
	fullText := e.prompt + string(e.buf)
	chunks := wrapChunks(fullText, width)
	idx, off := cursorChunk(chunks, fullText, len(e.prompt)+e.pos)
	c := chunks[idx]
	return idx, visibleWidth(c.Text[:runeOffsetToByte(c.Text, off)])
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
	text := string(e.buf)
	bytePos := RuneIndexToBytePos(text, e.pos)
	startByte := PrevGraphemeStart(text, bytePos)
	startRune := BytePosToRuneIndex(text, startByte)
	e.buf = append(e.buf[:startRune], e.buf[e.pos:]...)
	e.pos = startRune
	e.clearPreferredCol()
	e.updateAutoComp()
}

func (e *Editor) deleteForward() {
	if e.pos >= len(e.buf) {
		return
	}
	e.pushUndo()
	e.lastAction = ""
	text := string(e.buf)
	bytePos := RuneIndexToBytePos(text, e.pos)
	endByte := NextGraphemeEnd(text, bytePos)
	endRune := BytePosToRuneIndex(text, endByte)
	e.buf = append(e.buf[:e.pos], e.buf[endRune:]...)
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
	bytePos := RuneIndexToBytePos(s, pos)
	prev := PrevGraphemeStart(s, bytePos)
	if markerStart, ok := findPasteMarkerStartBefore(s, prev); ok {
		return BytePosToRuneIndex(s, markerStart)
	}
	return BytePosToRuneIndex(s, prev)
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
	bytePos := RuneIndexToBytePos(s, pos)
	// If the text starting at bytePos is a paste marker, jump to after it
	if pos < len(e.buf) && strings.HasPrefix(s[bytePos:], "[paste #") {
		endIdx := strings.IndexByte(s[bytePos:], ']')
		if endIdx >= 0 {
			return BytePosToRuneIndex(s, bytePos+endIdx+1)
		}
	}
	next := NextGraphemeEnd(s, bytePos)
	return BytePosToRuneIndex(s, next)
}

// editorChunks builds the visual-line layout (chunks) for the current buffer
// at the editor's render width and returns it with the cursor's full-text rune
// position. Shared by cursor navigation so movement, scrolling, and rendering
// all agree on a single layout — the same wrapChunks used to display the text.
func (e *Editor) editorChunks() (chunks []wrapChunk, fullText string, pos int) {
	width := e.lastWidth
	if width <= 0 {
		width = 80
	}
	fullText = e.prompt + string(e.buf)
	chunks = wrapChunks(fullText, width)
	pos = len(e.prompt) + e.pos
	return chunks, fullText, pos
}

// verticalMoveColumn resolves the target column for an up/down move using the
// sticky (preferred) column rules. Shared by lineUp and lineDown.
func (e *Editor) verticalMoveColumn(currentVisCol, sourceMaxVis, targetMaxVis int) int {
	if !e.preferredColSet || currentVisCol < sourceMaxVis-1 {
		if targetMaxVis < currentVisCol {
			e.setPreferredCol(currentVisCol)
			return targetMaxVis - 1
		}
		e.clearPreferredCol()
		return currentVisCol
	}
	if targetMaxVis < currentVisCol || targetMaxVis < e.preferredVisualCol {
		return targetMaxVis - 1
	}
	col := e.preferredVisualCol
	e.clearPreferredCol()
	return col
}

func (e *Editor) lineUp() {
	chunks, fullText, pos := e.editorChunks()
	currentVL, _ := cursorChunk(chunks, fullText, pos)
	if currentVL <= 0 {
		e.pos = 0
		return
	}
	cur := chunks[currentVL]
	target := chunks[currentVL-1]
	moveToCol := e.verticalMoveColumn(pos-cur.Start, cur.End-cur.Start, target.End-target.Start)
	if moveToCol < 0 {
		moveToCol = 0
	}
	e.pos = target.Start + moveToCol - len(e.prompt)
	if e.pos < 0 {
		e.pos = 0
	}
	e.adjustScrollToCursor()
}

func (e *Editor) lineDown() {
	chunks, fullText, pos := e.editorChunks()
	currentVL, _ := cursorChunk(chunks, fullText, pos)
	if currentVL >= len(chunks)-1 {
		e.pos = len(e.buf)
		return
	}
	cur := chunks[currentVL]
	target := chunks[currentVL+1]
	moveToCol := e.verticalMoveColumn(pos-cur.Start, cur.End-cur.Start, target.End-target.Start)
	if moveToCol < 0 {
		moveToCol = 0
	}
	e.pos = target.Start + moveToCol - len(e.prompt)
	if e.pos < 0 {
		e.pos = 0
	}
	e.adjustScrollToCursor()
}

// adjustScrollToCursor adjusts e.scroll so the cursor is visible.
// Called after cursor movement operations.
func (e *Editor) adjustScrollToCursor() {
	chunks, fullText, pos := e.editorChunks()
	cursorVisLine, _ := cursorChunk(chunks, fullText, pos)

	if cursorVisLine < e.scroll {
		e.scroll = cursorVisLine
	} else if cursorVisLine >= e.scroll+e.maxLines {
		e.scroll = cursorVisLine - e.maxLines + 1
	}

	maxScroll := len(chunks) - e.maxLines
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

	if newIdx >= len(e.history) {
		// Past newest: return to a truly empty editing line.
		e.histIdx = -1
		if e.historyDraft != nil {
			e.setTextLocked(*e.historyDraft)
			e.historyDraft = nil
		} else {
			e.clearLocked()
		}
		e.clearPreferredCol()
		e.adjustScrollToCursor()
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

// nextHistoryIndex computes the target history index for a direction.
// direction: -1 = older, 1 = newer.
// Returns -1 to mean "return to editing state" (past newest), -2 to mean
// no-op (already in editing state and pressing Down).
func (e *Editor) nextHistoryIndex(direction int) int {
	switch {
	case e.histIdx == -1 && direction == -1:
		return len(e.history) - 1
	case e.histIdx == -1 && direction == 1:
		return -2 // sentinel: no-op
	default:
		newIdx := e.histIdx + direction
		if newIdx < 0 {
			return 0 // clamp to oldest entry
		}
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
func (e *Editor) isOnFirstVisualLine() bool {
	chunks, fullText, pos := e.editorChunks()
	if len(chunks) == 0 {
		return true
	}
	idx, _ := cursorChunk(chunks, fullText, pos)
	return idx <= 0
}

// isOnLastVisualLine returns true if the cursor is on the last visual line.
func (e *Editor) isOnLastVisualLine() bool {
	chunks, fullText, pos := e.editorChunks()
	if len(chunks) == 0 {
		return true
	}
	idx, _ := cursorChunk(chunks, fullText, pos)
	return idx >= len(chunks)-1
}

func (e *Editor) wordLeft() {
	e.clearPreferredCol()
	e.pos = findWordBackward(string(e.buf), e.pos)
}

func (e *Editor) wordRight() {
	e.clearPreferredCol()
	e.pos = findWordForward(string(e.buf), e.pos)
}
