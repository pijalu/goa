// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/internal/ansi"
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
	if maxDesired < 1 {
		maxDesired = 1
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

// renderContentLine renders a single content line with an optional cursor
// marker. cursorOffset is the rune offset of the cursor within line; because
// line is a faithful slice of the source (a wrapChunk.Text), the marker is
// inserted exactly at the cursor's rune position — display and cursor can
// never disagree.
func (e *Editor) renderContentLine(line string, hasCursor bool, cursorOffset int) string {
	if !hasCursor {
		return padToWidth(line, e.lastWidth)
	}
	bytePos := runeOffsetToByte(line, cursorOffset)
	return padToWidth(line[:bytePos]+CURSOR_MARKER+line[bytePos:], e.lastWidth)
}

// editorLayout holds the computed rendering geometry for the editor.
type editorLayout struct {
	chunks           []wrapChunk
	totalVisualLines int
	cursorVisLine    int
	cursorOffset     int // rune offset of the cursor within its chunk
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
	chunks := wrapChunks(fullText, width)
	totalVisualLines := len(chunks)

	cursorFullPos := len(e.prompt) + e.pos
	cursorVisLine, cursorOffset := cursorChunk(chunks, fullText, cursorFullPos)

	e.maxLines = clampEditorMaxLines(totalVisualLines, e.terminalRows())
	// Reset the stable height tracker when the terminal is resized; position
	// changes are expected in that case.
	if tr := e.terminalRows(); tr != e.lastTerminalRows {
		e.lastTerminalRows = tr
		e.stableMaxLines = 0
	}
	if e.maxLines > e.stableMaxLines {
		e.stableMaxLines = e.maxLines
	}
	if e.maxLines < e.stableMaxLines {
		e.maxLines = e.stableMaxLines
	}
	e.scroll = clampScroll(cursorVisLine, e.scroll, e.maxLines, totalVisualLines)

	dispStart := e.scroll
	dispEnd := min(e.scroll+e.maxLines, totalVisualLines)

	return editorLayout{
		chunks:           chunks,
		totalVisualLines: totalVisualLines,
		cursorVisLine:    cursorVisLine,
		cursorOffset:     cursorOffset,
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
		result = append(result, e.renderContentLine(layout.chunks[i].Text, hasCursor, layout.cursorOffset))
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
