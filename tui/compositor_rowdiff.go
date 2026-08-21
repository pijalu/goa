// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"fmt"
	"strings"
)

func (c *Compositor) unchangedRowTranscript(canvas []string, i, vt int) bool {
	if c.prevLines == nil {
		return false
	}
	prevIdx := i - vt + c.vt
	if prevIdx < 0 || prevIdx >= len(c.prevLines) {
		return false
	}
	cur := ""
	if i >= 0 && i < len(canvas) {
		cur = canvas[i]
	}
	return c.prevLines[prevIdx] == cur
}

// unchangedRowChrome reports whether chrome canvas row i (at screen row
// screenRow) has the same bytes as the row the terminal currently shows
// there. In the previous frame the chrome band was also pinned at the screen
// bottom, so the same screen row held prevLines at the chrome offset in the
// PREVIOUS canvas.
func (c *Compositor) unchangedRowChrome(canvas []string, i, screenRow, windowH int) bool {
	if c.prevLines == nil {
		return false
	}
	prevChromeH := c.prevChromeH
	prevWindowH := c.prevH - prevChromeH
	if prevWindowH < 1 {
		prevWindowH = 1
	}
	prevContentEnd := len(c.prevLines) - prevChromeH
	prevIdx := prevContentEnd + (screenRow - prevWindowH - 1)
	if prevIdx < 0 || prevIdx >= len(c.prevLines) {
		return false
	}
	cur := ""
	if i >= 0 && i < len(canvas) {
		cur = canvas[i]
	}
	return c.prevLines[prevIdx] == cur
}

// appendCursorSeq writes the hardware-cursor positioning into the SAME
// synced buffer as the frame content (absolute CUP, immune to auto-wrap drift),
// plus a show/hide transition only when the visibility actually changes.
//
// The screen row uses the SAME two-phase mapping as the paint
// (repaintWindow/drawWindow): transcript rows (below contentEnd) map linearly
// in the viewport top, while the pinned chrome band maps to the screen
// bottom regardless of where the window top lands. The two mappings coincide
// only when vt is the natural bottom anchor (canvasLen == vt+height); during
// a transient canvas shrink the watermark clamps vt above the anchor, and a
// linear map would place the cursor ABOVE the editor row — the reported
// "cursor one line too high / jumps out of the input box" glitches.
func (c *Compositor) appendCursorSeq(buf *strings.Builder, cp *CursorPos, totalLines, width, vtop, height int) {
	if cp == nil || totalLines <= 0 {
		if c.cursorVisible {
			buf.WriteString("\x1b[?25l")
			c.cursorVisible = false
		}
		return
	}
	targetRow := max(0, min(cp.Row, totalLines-1))
	targetCol := max(0, cp.Col)
	if width > 0 && targetCol >= width {
		targetCol = width - 1
	}
	screenRow := clampRow(c.cursorScreenRow(targetRow, totalLines, vtop, height), height)
	buf.WriteString(fmt.Sprintf("\x1b[%d;%dH", screenRow, targetCol+1))
	if !c.cursorVisible {
		buf.WriteString("\x1b[?25h")
		c.cursorVisible = true
	}
	c.hardwareCursorRow = targetRow
}

// cursorScreenRow maps a canvas row to its 1-indexed screen row under the
// two-phase layout: chrome-band rows (at/above the transcript end) are pinned
// to the screen bottom, transcript rows scroll linearly with the viewport top.
func (c *Compositor) cursorScreenRow(targetRow, totalLines, vtop, height int) int {
	contentEnd := max(0, totalLines-c.chromeH)
	if targetRow >= contentEnd && c.chromeH > 0 {
		windowH := max(1, height-c.chromeH)
		return windowH + (targetRow - contentEnd) + 1
	}
	return targetRow - vtop + 1
}

// clampRow clamps a 1-indexed screen row to [1, height].
func clampRow(row, height int) int {
	if row < 1 {
		return 1
	}
	if row > height {
		return height
	}
	return row
}

// applyLineResets appends a reset sequence to every non-image line in the
// given canvas subrange so SGR state cannot bleed across rows.
func applyLineResets(canvas []string, start, end int) []string {
	for i := start; i < end && i < len(canvas); i++ {
		if canvas[i] == "" {
			continue
		}
		canvas[i] = canvas[i] + "\x1b[0m"
	}
	return canvas
}

func copySlice(s []string) []string {
	if s == nil {
		return nil
	}
	out := make([]string, len(s))
	copy(out, s)
	return out
}

// FindNode returns the first node with the given name, or nil.
