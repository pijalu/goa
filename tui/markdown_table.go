// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"

	"github.com/pijalu/goa/internal/ansi"
)

// ── Table detection ────────────────────────────────────────────

// isTableRow checks if a line starts a table row (starts with |).
func isTableRow(s string) bool {
	return strings.HasPrefix(strings.TrimSpace(s), "|")
}

// isTableSeparator checks if a line is a table separator (like |---|---|).
func isTableSeparator(s string) bool {
	line := strings.TrimSpace(s)
	if len(line) < 3 {
		return false
	}
	if !strings.HasPrefix(line, "|") || !strings.HasSuffix(line, "|") {
		return false
	}
	// Check that the content between pipes is only dashes, colons, and spaces
	inner := line[1 : len(line)-1]
	if len(inner) == 0 {
		return false
	}
	for _, ch := range inner {
		if ch != '-' && ch != ':' && ch != ' ' && ch != '|' {
			return false
		}
	}
	return strings.Contains(inner, "-")
}

// parseTableRow splits a table row line into cell contents.
func parseTableRow(line string) []string {
	line = strings.TrimSpace(line)
	// Strip leading and trailing pipe.
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	// Split by | and trim each cell
	parts := strings.Split(line, "|")
	cells := make([]string, len(parts))
	for i, p := range parts {
		cells[i] = strings.TrimSpace(p)
	}
	return cells
}

// collectTable collects all rows of a table starting at line start.
// Returns (headerRow, separatorRow, dataRows, consumed).
func (r *MDStreamRenderer) collectTable(lines []string, start int) ([]string, []string, [][]string, int) {
	if start >= len(lines) {
		return nil, nil, nil, 0
	}

	header := parseTableRow(lines[start])
	consumed := 1

	// Look for separator row
	var separatorRow []string
	if start+1 < len(lines) && isTableSeparator(lines[start+1]) {
		separatorRow = parseTableRow(lines[start+1])
		consumed = 2
	}

	// Collect data rows
	var dataRows [][]string
	for i := start + consumed; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if !isTableRow(line) {
			break
		}
		dataRows = append(dataRows, parseTableRow(line))
		consumed++
	}

	return header, separatorRow, dataRows, consumed
}

// renderTable renders a markdown table with box-drawing characters,
// cell-aware wrapping, natural width calculation, and proportional
// shrinking.
func (r *MDStreamRenderer) renderTable(header []string, separator []string, rows [][]string) []string {
	if len(header) == 0 {
		return nil
	}

	numCols := tableColCount(header, rows)
	borderOverhead := 3*numCols + 1
	availableForCells := r.width - borderOverhead
	if availableForCells < numCols {
		return r.renderTableFallback(header, rows)
	}

	naturalWidths, minWordWidths := tableNaturalWidths(header, rows, numCols)
	minColumnWidths := tableMinWidths(minWordWidths, numCols, availableForCells)
	columnWidths := tableFinalWidths(naturalWidths, minColumnWidths, numCols, borderOverhead, availableForCells, r.width)

	dim := ansi.Faint
	bold := ansi.Bold
	reset := ansi.Reset

	result := []string{
		tableBorder("┌", "┬", "┐", columnWidths, dim, reset),
	}

	headerLines := r.renderTableWrappedCells(header, columnWidths)
	for _, line := range headerLines {
		result = append(result, renderTableDataLine(line, columnWidths, bold, dim, reset))
	}

	result = append(result, tableBorder("├", "┼", "┤", columnWidths, dim, reset))

	for rowIdx, row := range rows {
		rowLines := r.renderTableWrappedCells(row, columnWidths)
		for _, line := range rowLines {
			result = append(result, renderTableDataLine(line, columnWidths, "", dim, reset))
		}
		if rowIdx < len(rows)-1 {
			result = append(result, tableBorder("├", "┼", "┤", columnWidths, dim, reset))
		}
	}

	result = append(result, tableBorder("└", "┴", "┘", columnWidths, dim, reset))
	result = append(result, "")
	return result
}

// tableBorder draws a horizontal table border using the given corner/join characters.
func tableBorder(left, mid, right string, widths []int, dim, reset string) string {
	parts := make([]string, len(widths))
	for i, w := range widths {
		parts[i] = "─" + strings.Repeat("─", w) + "─"
	}
	return dim + left + strings.Join(parts, mid) + right + reset
}

// renderTableDataLine formats one line of table cells with optional bold styling.
func renderTableDataLine(cells []string, widths []int, bold, dim, reset string) string {
	parts := make([]string, len(widths))
	for i, text := range cells {
		pw := padToWidth(text, widths[i])
		if bold != "" {
			parts[i] = bold + pw + reset + dim
		} else {
			parts[i] = pw
		}
	}
	return dim + "│ " + strings.Join(parts, " │ ") + " │" + reset
}

// tableColCount returns the max column count across header and rows.
func tableColCount(header []string, rows [][]string) int {
	n := len(header)
	for _, row := range rows {
		if len(row) > n {
			n = len(row)
		}
	}
	return n
}

// tableNaturalWidths calculates natural and min-word widths for each column.
func tableNaturalWidths(header []string, rows [][]string, numCols int) (natural, minWord []int) {
	natural = make([]int, numCols)
	minWord = make([]int, numCols)
	maxWord := 30
	for i, cell := range header {
		natural[i] = ansi.Width(cell)
		minWord[i] = max(1, min(longestWordWidth(cell, maxWord), maxWord))
	}
	for _, row := range rows {
		for i, cell := range row {
			if i >= numCols {
				continue
			}
			if cw := ansi.Width(cell); cw > natural[i] {
				natural[i] = cw
			}
			if lw := longestWordWidth(cell, maxWord); lw > minWord[i] {
				minWord[i] = min(lw, maxWord)
			}
		}
	}
	return
}

// tableMinWidths computes minimum column widths from word widths.
func tableMinWidths(minWord []int, numCols, available int) []int {
	widths := make([]int, numCols)
	copy(widths, minWord)
	total := 0
	for _, w := range widths {
		total += w
	}
	if total <= available || numCols == 0 {
		return widths
	}
	for i := range widths {
		widths[i] = 1
	}
	remaining := available - numCols
	if remaining <= 0 {
		return widths
	}
	return allocateProportionalWidths(widths, minWord, remaining, available)
}

func allocateProportionalWidths(widths, minWord []int, remaining, available int) []int {
	totalWeight := 0
	for _, w := range minWord {
		if w > 1 {
			totalWeight += w - 1
		}
	}
	allocated := len(widths)
	for i, w := range minWord {
		if totalWeight > 0 && w > 1 {
			growth := (w - 1) * remaining / totalWeight
			widths[i] += growth
			allocated += growth
		}
	}
	for i := 0; allocated < available && i < len(widths); i++ {
		widths[i]++
		allocated++
	}
	return widths
}

// tableFinalWidths computes final column widths, shrinking proportionally if needed.
func tableFinalWidths(natural, minWidths []int, numCols, borderOverhead, availableForCells, termWidth int) []int {
	totalNat := borderOverhead
	for _, w := range natural {
		totalNat += w
	}
	if totalNat <= termWidth {
		result := make([]int, numCols)
		for i := 0; i < numCols; i++ {
			result[i] = max(natural[i], minWidths[i])
		}
		return result
	}

	totalGrow, extra := tableFinalWidthGrowth(natural, minWidths, numCols, availableForCells)

	result := make([]int, numCols)
	allocated := 0
	for i := 0; i < numCols; i++ {
		grow := 0
		if totalGrow > 0 {
			grow = max(0, natural[i]-minWidths[i]) * extra / totalGrow
		}
		result[i] = minWidths[i] + grow
		allocated += result[i]
	}
	return distributeRemainingWidth(result, natural, allocated, availableForCells)
}

func tableFinalWidthGrowth(natural, minWidths []int, numCols, availableForCells int) (totalGrow, extra int) {
	for i := 0; i < numCols; i++ {
		if d := natural[i] - minWidths[i]; d > 0 {
			totalGrow += d
		}
	}
	extra = availableForCells
	for _, w := range minWidths {
		extra -= w
	}
	if extra < 0 {
		extra = 0
	}
	return
}

func distributeRemainingWidth(result, natural []int, allocated, available int) []int {
	for remaining := available - allocated; remaining > 0; {
		grew := false
		for i := 0; i < len(result) && remaining > 0; i++ {
			if result[i] < natural[i] {
				result[i]++
				remaining--
				grew = true
			}
		}
		if !grew {
			result[0] += remaining
			break
		}
	}
	return result
}

// renderTableFallback renders a plain pipe-delimited table when too narrow.
func (r *MDStreamRenderer) renderTableFallback(header []string, rows [][]string) []string {
	var result []string
	result = append(result, "| "+strings.Join(header, " | ")+" |")
	for _, row := range rows {
		result = append(result, "| "+strings.Join(row, " | ")+" |")
	}
	result = append(result, "")
	return result
}

// renderTableWrappedCells wraps each cell text to fit column width.
// Returns a list of rows, where each row is a list of cell strings.
// All rows have the same number of lines (shorter cells are padded).
func (r *MDStreamRenderer) renderTableWrappedCells(cells []string, colWidths []int) [][]string {
	numCols := len(colWidths)
	if numCols == 0 {
		return nil
	}

	wrapped, maxLines := wrapTableCells(cells, colWidths)
	return padWrappedCells(wrapped, maxLines, numCols)
}

func wrapTableCells(cells []string, colWidths []int) ([][]string, int) {
	wrapped := make([][]string, len(colWidths))
	maxLines := 0
	for i, cw := range colWidths {
		text := ""
		if i < len(cells) {
			text = cells[i]
		}
		wrapped[i] = wrapTableCell(text, cw)
		if len(wrapped[i]) > maxLines {
			maxLines = len(wrapped[i])
		}
	}
	return wrapped, maxLines
}

func wrapTableCell(text string, width int) []string {
	if text == "" {
		return []string{""}
	}
	lines := ansi.Wrap(text, width)
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func padWrappedCells(wrapped [][]string, maxLines, numCols int) [][]string {
	result := make([][]string, maxLines)
	for line := 0; line < maxLines; line++ {
		row := make([]string, numCols)
		for col := 0; col < numCols; col++ {
			if line < len(wrapped[col]) {
				row[col] = wrapped[col][line]
			}
		}
		result[line] = row
	}
	return result
}

// longestWordWidth returns the visible width of the longest word in s.
func longestWordWidth(s string, maxWidth int) int {
	maxW := 0
	for _, word := range strings.Fields(s) {
		w := ansi.Width(word)
		if w > maxW {
			maxW = w
		}
		if maxW >= maxWidth {
			return maxWidth
		}
	}
	return maxW
}
