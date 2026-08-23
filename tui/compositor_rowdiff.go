// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/internal/ansi"
	"github.com/rivo/uniseg"
)

// prevRowTranscript returns the previous-frame canvas row the terminal
// currently shows at transcript canvas index i (viewport top vt), plus whether
// such a baseline exists. The mapping is screen-relative: i - vt is the
// on-screen row, which held c.prevLines[i - vt + c.vt] in the previous frame.
func (c *Compositor) prevRowTranscript(i, vt int) (string, bool) {
	if c.prevLines == nil {
		return "", false
	}
	prevIdx := i - vt + c.vt
	if prevIdx < 0 || prevIdx >= len(c.prevLines) {
		return "", false
	}
	return c.prevLines[prevIdx], true
}

// unchangedRowTranscript reports whether transcript canvas row i (at viewport
// top vt) has the same bytes as the row the terminal currently shows there.
func (c *Compositor) unchangedRowTranscript(canvas []string, i, vt int) bool {
	prev, ok := c.prevRowTranscript(i, vt)
	if !ok {
		return false
	}
	cur := ""
	if i >= 0 && i < len(canvas) {
		cur = canvas[i]
	}
	return prev == cur
}

// prevRowChrome returns the previous-frame row shown at chrome canvas row i
// (screen row screenRow). In the previous frame the chrome band was also
// pinned at the screen bottom, so the same screen row held prevLines at the
// chrome offset in the PREVIOUS canvas.
func (c *Compositor) prevRowChrome(i, screenRow, windowH int) (string, bool) {
	if c.prevLines == nil {
		return "", false
	}
	prevChromeH := c.prevChromeH
	prevWindowH := c.prevH - prevChromeH
	if prevWindowH < 1 {
		prevWindowH = 1
	}
	prevContentEnd := len(c.prevLines) - prevChromeH
	prevIdx := prevContentEnd + (screenRow - prevWindowH - 1)
	if prevIdx < 0 || prevIdx >= len(c.prevLines) {
		return "", false
	}
	return c.prevLines[prevIdx], true
}

// unchangedRowChrome reports whether chrome canvas row i (at screen row
// screenRow) has the same bytes as the row the terminal currently shows there.
func (c *Compositor) unchangedRowChrome(canvas []string, i, screenRow, windowH int) bool {
	prev, ok := c.prevRowChrome(i, screenRow, windowH)
	if !ok {
		return false
	}
	cur := ""
	if i >= 0 && i < len(canvas) {
		cur = canvas[i]
	}
	return prev == cur
}

// rowUpdate describes how to repaint one changed canvas row.
//
// partial=false → legacy full-row emit: CUP(row,1) + \x1b[2K + whole line.
// partial=true → column-range emit: CUP(row,col+1) + seg. The segment either
// overwrites every visible cell from col through the end of the row (tail
// mode: a stable prefix exists) or exactly the changed leading cells (head
// mode: a stable suffix keeps its columns); untouched cells retain their
// previous frame's content.
type rowUpdate struct {
	partial bool
	col     int    // 0-indexed first rewritten visible column
	seg     string // exact bytes to write at col
}

// Approximate CUP+clear overhead per mode, used only to rank two valid plans
// by emitted-byte cost; exact widths differ by at most a digit or two.
const (
	costCupFull = 12 // "\x1b[<r>;1H" + "\x1b[2K"
	costCupCol  = 11 // "\x1b[<r>;<cc>H"
)

// planPartialRow computes the cheapest SAFE emit plan for one changed row,
// comparing prev (what the terminal currently shows there) with cur (the new
// canvas row), bounded by the terminal width.
//
// A partial plan requires ALL of:
//   - a stable byte prefix or suffix exists (the dirty range excludes an edge)
//   - every cut point lies outside escape sequences and grapheme clusters
//     (ansi.SafeCut): slicing never splits an escape sequence or wide grapheme
//   - both rows are emittable (no tabs/C0 controls, no OSC, style-closed) so
//     SGR/hyperlink state cannot leak across frames via untouched cells
//   - tail mode: the new tail is not narrower than the old one, so no stale
//     trailing cells survive; the segment never starts with a bare combining
//     mark (it would compose with stale cell content instead of repainting)
//   - head mode: both heads have equal visible width and contain no escapes,
//     so the untouched suffix keeps its exact columns and styling
//
// Anything else falls back to the full-row emit — visually identical to the
// pre-optimization behavior.
func planPartialRow(prev, cur string, width int) rowUpdate {
	if prev == cur || width <= 0 || !rowEmittable(prev) || !rowEmittable(cur) {
		return rowUpdate{}
	}
	p := alignPrefixToBoundary(prev, cur)
	sp := alignSuffixToBoundary(prev, cur, p)
	tail, tailOK := planTailRow(prev, cur, width, p)
	head, headOK := planHeadRow(prev, cur, width, sp)
	switch {
	case tailOK && (!headOK || len(tail.seg)+costCupCol <= len(head.seg)+costCupFull):
		return tail
	case headOK:
		return head
	default:
		return rowUpdate{}
	}
}

// planTailRow builds the stable-prefix plan: rewrite from the first differing
// column THROUGH the end of the row, overwriting every stale cell to the
// right of it. p is the aligned shared byte-prefix length.
func planTailRow(prev, cur string, width, p int) (rowUpdate, bool) {
	if p <= 0 || !ansi.SafeCut(cur, p) {
		return rowUpdate{}, false
	}
	col := ansi.Width(cur[:p])
	avail := width - col
	if avail <= 0 {
		return rowUpdate{}, false // change starts past the right margin
	}
	// Width of the previously EMITTED tail beyond col (the full row was
	// truncated to width). The new tail must cover at least as many columns.
	oldTailW := min(ansi.Width(prev), width) - col
	seg := ansi.Truncate(cur[p:], avail)
	if max(0, oldTailW) > ansi.Width(seg) {
		return rowUpdate{}, false // narrower tail would leave stale cells
	}
	if !startsPrintable(seg) {
		return rowUpdate{}, false // bare combining mark cannot repaint a cell
	}
	return rowUpdate{partial: true, col: col, seg: seg}, true
}

// planHeadRow builds the stable-suffix plan: rewrite only the changed leading
// cells and leave the identical suffix untouched on screen.
func planHeadRow(prev, cur string, width, sp int) (rowUpdate, bool) {
	if sp <= 0 {
		return rowUpdate{}, false
	}
	qc, qp := len(cur)-sp, len(prev)-sp
	if !ansi.SafeCut(cur, qc) || !ansi.SafeCut(prev, qp) {
		return rowUpdate{}, false
	}
	// The differing middle may carry styling: escape-free heads guarantee the
	// untouched suffix renders under identical SGR state in both frames.
	if strings.ContainsRune(cur[:qc], 0x1b) || strings.ContainsRune(prev[:qp], 0x1b) {
		return rowUpdate{}, false
	}
	wCur, wPrev := ansi.Width(cur[:qc]), ansi.Width(prev[:qp])
	if wCur != wPrev || wCur > width {
		return rowUpdate{}, false // unequal heads would displace the suffix
	}
	return rowUpdate{partial: true, col: 0, seg: cur[:qc]}, true
}

// commonPrefixLen returns the length of the longest shared byte prefix.
func commonPrefixLen(a, b string) int {
	n := min(len(a), len(b))
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return i
}

// alignPrefixToBoundary returns the longest byte prefix shared by both rows
// that ALSO ends on a safe cut boundary (outside escape sequences and grapheme
// clusters). A raw byte-diff boundary can land mid-cluster — multi-byte runes
// often share leading bytes (braille spinner glyphs differ in their 3rd byte,
// regional indicators in their 4th) — so the boundary retreats into the
// byte-shared region, which stays provably identical in both rows.
func alignPrefixToBoundary(a, b string) int {
	p := commonPrefixLen(a, b)
	for p > 0 && !ansi.SafeCut(b, p) {
		p--
	}
	return p
}

// alignSuffixToBoundary returns the longest byte suffix shared by both rows
// after the given prefix, with the head/suffix split moved FORWARD to the next
// safe boundary when the raw split lands mid-cluster (the whole differing
// cluster then belongs to the head). Extension leaves the byte-shared region,
// so the claimed suffix is re-verified against both rows; an unusable suffix
// yields 0.
func alignSuffixToBoundary(a, b string, prefix int) int {
	maxS := min(len(a), len(b)) - prefix
	sp := 0
	for sp < maxS && a[len(a)-1-sp] == b[len(b)-1-sp] {
		sp++
	}
	qc := len(b) - sp
	if ansi.SafeCut(b, qc) {
		return sp
	}
	for qc < len(b) && !ansi.SafeCut(b, qc) {
		qc++
	}
	if qc >= len(b) {
		return 0 // extension consumed the whole row: no suffix left to keep
	}
	qp := len(a) - (len(b) - qc)
	if qp <= 0 || a[qp:] != b[qc:] || !ansi.SafeCut(a, qp) {
		return 0
	}
	return len(b) - qc
}

// startsPrintable reports whether the first non-escape grapheme cluster of s
// carries visible width. A partial segment must not BEGIN with a bare
// combining mark: written in isolation it composes with whatever the terminal
// already shows in that cell instead of repainting it, which a full-row
// rewrite would not do.
func startsPrintable(s string) bool {
	gr := uniseg.NewGraphemes(s)
	for gr.Next() {
		cl := gr.Str()
		if strings.ContainsRune(cl, 0x1b) {
			continue // escape noise: keep scanning for the first visible cell
		}
		return ansi.Width(cl) > 0
	}
	return true // nothing visible at all (pure resets): harmless to write
}

// rowEmittable reports whether a canvas row satisfies the preconditions for
// partial emission: no tabs or other C0 controls (ESC excepted), no OSC
// sequences (hyperlink state is terminal-global, not row-local), and either no
// escapes at all or a terminating reset so styling cannot bleed across frames
// through cells this frame leaves untouched.
func rowEmittable(s string) bool {
	hasEsc := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == 0x1b:
			hasEsc = true
			if strings.HasPrefix(s[i:], "\x1b]") {
				return false // OSC: hyperlink state outlives the row
			}
		case c < 0x20:
			return false // tab, CR, ...: column math becomes position-dependent
		}
	}
	return !hasEsc || strings.HasSuffix(s, ansi.Reset)
}

// emitRowUpdate writes one changed row into the frame buffer: a partial
// column-range update when planPartialRow found a safe dirty range excluding
// a row edge, otherwise the legacy full-row clear+rewrite.
func (c *Compositor) emitRowUpdate(buf *strings.Builder, screenRow int, prev, cur string, width int) {
	up := planPartialRow(prev, cur, width)
	if !up.partial {
		buf.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[2K", screenRow))
		buf.WriteString(truncateToWidth(cur, width, ""))
		return
	}
	buf.WriteString(fmt.Sprintf("\x1b[%d;%dH", screenRow, up.col+1))
	buf.WriteString(up.seg)
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
