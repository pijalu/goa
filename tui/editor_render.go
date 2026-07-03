// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/internal/ansi"
	"github.com/rivo/uniseg"
)

// ── Rendering ──

// maxEditorLines computes the maximum visible lines for the editor.
// At least 5 lines, at most 12, scaled to 30%% of terminal height.
func maxEditorLines(terminalRows int) int {
	// Math.max(5, Math.floor(terminalRows * 0.3))
	fromHeight := terminalRows * 30 / 100
	if fromHeight < 5 {
		fromHeight = 5
	}
	maxDesired := fromHeight
	if maxDesired > 12 {
		maxDesired = 12
	}
	return maxDesired
}

// editorBorderColor returns the styled border string for the editor.
func editorBorderColor(level string) string {
	color := ThinkingLevelSeparatorColor(level)
	if color == "" {
		color = TheTheme.ColorHex("border")
	}
	if color == "" {
		color = "#444444"
	}
	return ansi.Fg(color)
}

// renderScrollIndicator builds a scroll indicator line ("─── ↑ N more" or "─── ↓ N more").
func renderScrollIndicator(direction rune, count, width int) string {
	indicator := fmt.Sprintf("─── %c %d more ", direction, count)
	indent := width - visibleWidth(indicator)
	if indent < 0 {
		indent = 0
	}
	return ansi.Fg(TheTheme.ColorHex("system_msg")) + indicator +
		strings.Repeat("─", indent) + ansi.Reset
}

// renderTitledBorder renders an editor border line, optionally embedding a
// title label as "───┨ title ┠───". When title is empty, a plain ruled line is
// returned. The brackets and title use the system_msg color while the rule
// uses the thinking-level border color.
func renderTitledBorder(title, thinkingLevel string, width int) string {
	border := editorBorderColor(thinkingLevel)
	reset := ansi.Reset
	if strings.TrimSpace(title) == "" {
		return border + strings.Repeat("─", width) + reset
	}
	labelColor := ansi.Fg(TheTheme.ColorHex("system_msg"))
	center := labelColor + "┨ " + strings.TrimSpace(title) + " ┠" + reset
	centerWidth := visibleWidth(center)
	if centerWidth+6 >= width {
		// Too narrow for decoration: just show the title.
		return border + center + reset
	}
	left := (width - centerWidth) / 2
	right := width - centerWidth - left
	if left < 3 {
		left = 3
	}
	if right < 3 {
		right = 3
	}
	return border + strings.Repeat("─", left) + reset + center + border + strings.Repeat("─", right) + reset
}

// renderContentLine renders a single content line with optional cursor marker.
func (e *Editor) renderContentLine(line string, hasCursor bool, cursorCol int) string {
	if !hasCursor {
		return padToWidth(line, e.lastWidth)
	}
	bytePos := bytePosForCol(line, cursorCol)
	return padToWidth(line[:bytePos]+CURSOR_MARKER+line[bytePos:], e.lastWidth)
}

// editorLayout holds the computed rendering geometry for the editor.
type editorLayout struct {
	wrapped          []string
	totalVisualLines int
	cursorVisLine    int
	cursorCol        int
	dispStart        int
	dispEnd          int
	translatedLine   int
}

func (e *Editor) Render(width int) []string {
	if width <= 0 {
		return nil
	}
	e.lastWidth = width

	layout := e.computeLayout(width)
	result := e.renderEditorFrame(layout, width)

	if e.focused && e.compState.Active() {
		result = e.appendCompletionLines(result, width)
	}
	return result
}

func (e *Editor) computeLayout(width int) editorLayout {
	fullText := e.prompt + string(e.buf)
	wrapped := wrapText(fullText, width)
	totalVisualLines := len(wrapped)

	cursorFullPos := len(e.prompt) + e.pos
	cursorVisLine, cursorCol := visualCursorPos(fullText, cursorFullPos, width)

	e.maxLines = clampEditorMaxLines(totalVisualLines, e.terminalRows())
	e.scroll = clampScroll(cursorVisLine, e.scroll, e.maxLines, totalVisualLines)

	dispStart := e.scroll
	dispEnd := min(e.scroll+e.maxLines, totalVisualLines)

	return editorLayout{
		wrapped:          wrapped,
		totalVisualLines: totalVisualLines,
		cursorVisLine:    cursorVisLine,
		cursorCol:        cursorCol,
		dispStart:        dispStart,
		dispEnd:          dispEnd,
		translatedLine:   cursorVisLine - e.scroll,
	}
}

func (e *Editor) terminalRows() int {
	if e.tui != nil {
		if tr := e.tui.TerminalRows(); tr > 5 {
			return tr
		}
	}
	return 24
}

func clampEditorMaxLines(totalVisualLines, termRows int) int {
	capLines := maxEditorLines(termRows)
	neededLines := totalVisualLines
	if neededLines > capLines {
		neededLines = capLines
	}
	if neededLines < 1 {
		neededLines = 1
	}
	return neededLines
}

func clampScroll(cursorVisLine, scroll, maxLines, totalVisualLines int) int {
	if cursorVisLine < scroll {
		scroll = cursorVisLine
	} else if cursorVisLine >= scroll+maxLines {
		scroll = cursorVisLine - maxLines + 1
	}
	maxScroll := totalVisualLines - maxLines
	if scroll > maxScroll {
		scroll = maxScroll
	}
	if scroll < 0 {
		scroll = 0
	}
	return scroll
}

func (e *Editor) renderEditorFrame(layout editorLayout, width int) []string {
	result := []string{e.renderTopBorder(width)}

	for i := layout.dispStart; i < layout.dispEnd; i++ {
		hasCursor := e.focused && (i-layout.dispStart) == layout.translatedLine
		result = append(result, e.renderContentLine(layout.wrapped[i], hasCursor, layout.cursorCol))
	}

	for len(result)-1 < e.maxLines {
		result = append(result, strings.Repeat(" ", width))
	}

	result = append(result, e.renderBottomBorder(layout, width))
	return result
}

func (e *Editor) renderTopBorder(width int) string {
	if e.scroll > 0 {
		return renderScrollIndicator('↑', e.scroll, width)
	}
	return renderTitledBorder(e.title, e.thinkingLevel, width)
}

func (e *Editor) renderBottomBorder(layout editorLayout, width int) string {
	if linesBelow := layout.totalVisualLines - layout.dispEnd; linesBelow > 0 {
		return renderScrollIndicator('↓', linesBelow, width)
	}
	return editorBorderColor(e.thinkingLevel) + strings.Repeat("─", width) + ansi.Reset
}

// appendCompletionLines appends completion list lines after the editor content.
func (e *Editor) appendCompletionLines(result []string, width int) []string {
	compLines := e.renderAutoComp(width)
	if len(compLines) == 0 {
		return result
	}
	return append(result, compLines...)
}

// compLineInfo holds a rendered completion line and its source item index.
type compLineInfo struct {
	text      string
	itemIndex int // -1 for category headers
}

// renderAutoComp renders the completion popup with category headers.
func (e *Editor) renderAutoComp(width int) []string {
	items := e.compState.Items
	idx := e.compState.Idx
	if len(items) == 0 {
		return nil
	}

	allLines := e.buildCompletionLines(items, idx, width)
	selLine := findSelectedLine(allLines, idx)
	start := clampWindowStart(selLine, len(allLines), 8)

	var lines []string
	end := min(start+8, len(allLines))
	for i := start; i < end; i++ {
		line := allLines[i].text
		if visibleWidth(line) > width {
			line = truncateToWidth(line, width, "")
		}
		lines = append(lines, padToWidth(line, width))
	}
	if len(allLines) > 8 {
		hint := ansi.Fg(TheTheme.ColorHex("system_msg")) + fmt.Sprintf("(%d more)", len(allLines)-8) + ansi.Reset
		if visibleWidth(hint) > width {
			hint = truncateToWidth(hint, width, "")
		}
		lines = append(lines, padToWidth(hint, width))
	}
	return lines
}

// buildCompletionLines builds rendered lines with category headers.
func (e *Editor) buildCompletionLines(items []Completion, selectedIdx, width int) []compLineInfo {
	selFg := TheTheme.ColorHex("selection_fg")
	dim := TheTheme.ColorHex("system_msg")
	sepColor := TheTheme.ColorHex("border")

	var lines []compLineInfo
	var currentCat CompCategory = -1

	for i, item := range items {
		if item.Category != currentCat {
			currentCat = item.Category
			lines = append(lines, compLineInfo{
				text:      ansi.Fg(sepColor) + categoryHeader(currentCat) + ansi.Reset,
				itemIndex: -1,
			})
		}
		lines = append(lines, compLineInfo{
			text:      e.renderCompletionItem(item, i == selectedIdx, width, selFg, dim),
			itemIndex: i,
		})
	}
	return lines
}

// categoryHeader returns the header label for a completion category.
func categoryHeader(cat CompCategory) string {
	switch cat {
	case CatMostUsed:
		return "── Most Used ──"
	case CatCommand:
		return "── Commands ──"
	case CatModifier:
		return "── Modifiers ──"
	default:
		return "──"
	}
}

// renderCompletionItem renders a single completion item with optional score padding.
func (e *Editor) renderCompletionItem(item Completion, selected bool, width int, selFg, dim string) string {
	var text string
	if selected {
		text = ansi.Fg(selFg) + "\u203a " + ansi.Bold + item.Display + ansi.Reset
	} else {
		text = "  " + item.Display
	}
	if item.Description != "" {
		text += "  " + ansi.Fg(dim) + item.Description + ansi.Reset
	}
	if item.Category == CatMostUsed && item.Score > 0 {
		scoreText := " " + fmt.Sprintf("%d", item.Score)
		pad := width - visibleWidth(text) - visibleWidth(scoreText)
		if pad < 2 {
			// Text is already too wide or barely fits — skip score to avoid overflow
			return text
		}
		text += strings.Repeat(" ", pad) + ansi.Fg(dim) + scoreText + ansi.Reset
	}
	return text
}

// findSelectedLine returns the line index containing the selected completion item.
func findSelectedLine(lines []compLineInfo, idx int) int {
	for i, li := range lines {
		if li.itemIndex == idx {
			return i
		}
	}
	return 0
}

// clampWindowStart computes the top of the visible window around selLine.
func clampWindowStart(selLine, total, maxShow int) int {
	start := selLine - maxShow/2
	if start < 0 {
		start = 0
	}
	if start+maxShow > total {
		start = total - maxShow
		if start < 0 {
			start = 0
		}
	}
	return start
}

// wordInfo tracks a word's rune range within a paragraph for visual cursor computation.
type wordInfo struct {
	start int // rune start (inclusive)
	end   int // rune end (exclusive)
}

// buildWordPositions splits a paragraph into words with their rune positions.
func buildWordPositions(runes []rune) []wordInfo {
	var words []wordInfo
	i := 0
	for i < len(runes) {
		if runes[i] == ' ' {
			i++
			continue
		}
		start := i
		for i < len(runes) && runes[i] != ' ' {
			i++
		}
		words = append(words, wordInfo{start: start, end: i})
	}
	return words
}

// cursorInCurrentParagraph checks if the cursor position falls within the
// current paragraph content (as opposed to at the trailing newline or after).
func cursorInCurrentParagraph(runePos, paraStart, paraLen int, hasNewline bool) bool {
	// Within content OR at the trailing newline position (end of line).
	// The newline character logically belongs to the current line for
	// visual cursor positioning, so <= is correct here.
	if runePos <= paraStart+paraLen {
		return true
	}
	return false
}

// firstWordEnd returns the end rune position of the word at the given index.
func firstWordEnd(runes []rune, words []wordInfo, idx int) int {
	if idx < 0 || idx >= len(words) {
		return 0
	}
	return words[idx].end
}

// cursorColInWord computes the visual column of an offset within a word.
// Width is grapheme-cluster-aware (via ansi.Width) so multi-rune clusters such
// as ZWJ emoji contribute their true rendered width, keeping the column
// consistent with the marker placement in bytePosForCol.
func cursorColInWord(runes []rune, w wordInfo, offset, baseCol int) int {
	within := offset - w.start
	if within < 0 {
		within = 0
	}
	return baseCol + ansi.Width(string(runes[w.start:w.start+within]))
}

// processLongWord handles the cursor position within a long word that exceeds
// the line width and must be broken character-by-character.
// Returns (found, visLine, visCol) where found is true if the cursor was located.
func processLongWord(runes []rune, w wordInfo, offset, visLine, visCol, width int) (bool, int, int) {
	// Flush current line first
	if visCol > 0 {
		visLine++
		visCol = 0
	}
	// Break the word into chunks, tracking each grapheme cluster's position.
	// Cluster-aware so ZWJ emoji/combining marks break at true rendered widths.
	wordStr := string(runes[w.start:w.end])
	gr := uniseg.NewGraphemes(wordStr)
	runeOff := 0 // rune offset within the word
	for gr.Next() {
		cluster := gr.Str()
		cw := ansi.ClusterWidth(cluster)
		if visCol+cw > width {
			visLine++
			visCol = 0
		}
		if w.start+runeOff == offset {
			return true, visLine, visCol
		}
		visCol += cw
		runeOff += len([]rune(cluster))
	}
	return false, visLine, visCol
}

// processNormalWord handles the cursor position within a normal (non-long) word
// that fits within the line width. Returns (found, visLine, visCol).
func processNormalWord(runes []rune, words []wordInfo, wi int, w wordInfo, offset, visLine, visCol, width int) (bool, int, int) {
	ww := visibleWidth(string(runes[w.start:w.end]))
	spaceWidth := 0
	if wi > 0 {
		spaceWidth = 1
	}

	// Check if cursor falls in the space before this word
	if wi > 0 && offset >= firstWordEnd(runes, words, wi-1) && offset < w.start {
		return true, visLine, visCol
	}

	// Normal word: try to add to current line
	if visCol+spaceWidth+ww > width && visCol > 0 {
		visLine++
		visCol = 0
		spaceWidth = 0
	}

	// Check if cursor falls in the space we're about to add
	if spaceWidth > 0 && offset == w.start-1 {
		return true, visLine, visCol
	}

	// Add space
	if spaceWidth > 0 {
		visCol++
	}

	// Check if cursor falls within this word
	if offset >= w.start && offset <= w.end {
		visCol = cursorColInWord(runes, w, offset, visCol)
		return true, visLine, visCol
	}

	visCol += ww
	return false, visLine, visCol
}

// processParagraph handles one paragraph in visualCursorPos.
// Returns (done, visLine, visCol) where done=true means the cursor was found.
func processParagraph(runes []rune, paraStart, paraEnd, runePos, visLine, visCol, width int) (bool, int, int) {
	hasNewline := paraEnd < len(runes)
	paraRunes := runes[paraStart:paraEnd]
	paraLen := len(paraRunes)

	// Check if cursor falls within this paragraph
	if cursorInCurrentParagraph(runePos, paraStart, paraLen, hasNewline) {
		offsetInPara := runePos - paraStart
		l, c := visualCursorInParagraph(string(paraRunes), offsetInPara, width)
		return true, visLine + l, c
	}

	// Cursor is after this paragraph — count its visual lines and advance
	if paraLen > 0 {
		wrapped := ansi.Wrap(string(paraRunes), width)
		visLine += len(wrapped)
		visCol = 0

		// If a newline follows, cursor may be at the newline position
		if hasNewline && paraStart+paraLen == runePos {
			return true, visLine, 0
		}
	} else if hasNewline {
		// Empty paragraph (consecutive newlines) — one empty visual line
		if paraStart == runePos {
			return true, visLine, 0
		}
		visLine++
		visCol = 0
	}

	return false, visLine, visCol
}

// visualCursorPos returns the visual line and column for a rune position in wrapped text.
// Matches the word-wrapping algorithm used by ansi.Wrap: text is split into words,
// and each word is placed on the current line if it fits, otherwise it starts a new line.
// Long words that exceed the width are broken character-by-character.
// Newlines (\n) force a line break and reset the paragraph.
func visualCursorPos(text string, runePos, width int) (line, col int) {
	if width <= 0 {
		return 0, 0
	}
	runes := []rune(text)
	if runePos > len(runes) {
		runePos = len(runes)
	}

	visLine := 0
	visCol := 0

	// Process text paragraph by paragraph (split by newlines)
	paraStart := 0
	for paraStart <= len(runes) {
		// Find end of this paragraph (next newline or end of text)
		paraEnd := paraStart
		for paraEnd < len(runes) && runes[paraEnd] != '\n' {
			paraEnd++
		}

		done, l, c := processParagraph(runes, paraStart, paraEnd, runePos, visLine, visCol, width)
		if done {
			return l, c
		}
		visLine, visCol = l, c
		paraStart = paraEnd + 1
	}

	return visLine, visCol
}

// visualCursorInParagraph computes the visual position within a single paragraph
// (no newlines) for a given rune offset, using word-based wrapping.
func visualCursorInParagraph(para string, offset, width int) (line, col int) {
	if width <= 0 || para == "" {
		return 0, 0
	}
	runes := []rune(para)
	if offset > len(runes) {
		offset = len(runes)
	}

	words := buildWordPositions(runes)
	visLine, visCol := simulateWordWrap(runes, words, offset, width)

	// Account for trailing spaces after the last word.
	if len(words) > 0 {
		lastWordEnd := words[len(words)-1].end
		for j := lastWordEnd; j < len(runes); j++ {
			if runes[j] == ' ' {
				visCol++
			}
		}
	}
	return visLine, visCol
}

// simulateWordWrap walks through words to find the visual position of offset.
func simulateWordWrap(runes []rune, words []wordInfo, offset, width int) (visLine, visCol int) {
	for wi, w := range words {
		ww := visibleWidth(string(runes[w.start:w.end]))

		if ww > width {
			found, l, c := processLongWord(runes, w, offset, visLine, visCol, width)
			if found {
				return l, c
			}
			visLine, visCol = l, c
			continue
		}

		found, l, c := processNormalWord(runes, words, wi, w, offset, visLine, visCol, width)
		if found {
			return l, c
		}
		visLine, visCol = l, c
	}
	return visLine, visCol
}

// bytePosForCol finds the byte position in line that corresponds to the given
// visual column. It is grapheme-cluster-aware so the returned byte offset
// always lands on a cluster boundary and the accumulated width matches
// visibleWidth (and thus the terminal's actual rendering). This is what keeps
// the hardware cursor column aligned with the glyph under multi-rune clusters
// such as ZWJ emoji, combining marks, and regional-indicator flags.
func bytePosForCol(line string, col int) int {
	if col <= 0 {
		return 0
	}
	current := 0
	gr := uniseg.NewGraphemes(line)
	for gr.Next() {
		cluster := gr.Str()
		w := ansi.ClusterWidth(cluster)
		if current+w > col {
			// Cursor falls inside this cluster — place the marker at the
			// cluster boundary (a cursor never splits a grapheme cluster).
			start, _ := gr.Positions()
			return start
		}
		current += w
	}
	return len(line)
}

// Focused returns focus state.
func (e *Editor) Focused() bool {
	return e.focused
}

// SetFocused sets focus state.
func (e *Editor) SetFocused(f bool) {
	e.focused = f
}

// Invalidate is a no-op.
func (e *Editor) Invalidate() {}
