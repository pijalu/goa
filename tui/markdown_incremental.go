// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import "strings"

// IncrementalMDRenderer accelerates the streaming case: a growing markdown
// document re-rendered every frame. A full MDStreamRenderer re-parses from
// byte 0 each frame — O(len) per frame, O(len²) over a streamed turn.
//
// The incremental renderer memoizes the rendered output of the document's
// STABLE PREFIX and only re-parses the mutable tail. The stable prefix ends at
// the last "hard boundary": a blank line that is NOT inside a fenced code
// block. Everything before that boundary is a sequence of complete, closed
// markdown blocks whose rendering cannot change as more text is appended
// (paragraphs, lists, tables, and closed fences all terminate at a blank line
// or closing fence). Only the tail after it — the currently-open block — is
// re-parsed each frame. As the document grows and new boundaries appear, the
// cache is EXTENDED by rendering just the newly-stabilized segment.
//
// Correctness invariant: we never split inside a fenced code block (a fence
// may contain blank lines that are content, not separators), and we only ever
// cache whole closed blocks. Concatenating cached prefix + freshly-rendered
// tail is byte-identical to rendering the whole document in one pass.
type IncrementalMDRenderer struct {
	r *MDStreamRenderer

	// stableText is the source text of the cached stable prefix (grows as the
	// document stabilizes); stableLines is its rendered form.
	stableText  string
	stableLines []string

	// boundaryScan caches the scanner state so that when text is a pure
	// append of the last scanned text, the boundary scan resumes from the
	// cached position instead of re-scanning from byte 0 (B005).
	boundaryScan boundaryScanner
}

// boundaryScanner holds the resumable state of the lastStableBoundary scan.
// On each Render call with appended text, only the newly-arrived suffix
// needs scanning; the cached boundary carries forward.
//
// The scanner always resumes from the start of the last complete line (the
// one ending in '\n'). It re-scans the incomplete tail line each time, so
// line-level classifications (fence open/close, blank, content) are always
// correct. State (inFence, prevAbsorbsBlank, boundary) is captured at the
// resume position — the state *before* the incomplete line began.
type boundaryScanner struct {
	// resumePos is the byte offset from which to resume scanning on the
	// next advance call. Always the start of a line (just past a '\n').
	resumePos int
	// inFence at resumePos.
	inFence bool
	// boundary found so far (monotonically increasing).
	boundary int
	// prevAbsorbsBlank at resumePos.
	prevAbsorbsBlank bool
	// scannedLen tracks the text length from the last advance call to
	// detect shrink/edit.
	scannedLen int
}

// advance scans new text appended since the last call and returns the
// updated stable boundary. If text shrank or was edited, the scanner resets
// and scans from byte 0.
func (bs *boundaryScanner) advance(text string) int {
	if len(text) < bs.scannedLen {
		*bs = boundaryScanner{}
	}
	bs.scannedLen = len(text)

	scanFrom := bs.resumePos
	if scanFrom > len(text) {
		scanFrom = 0
		bs.inFence = false
		bs.boundary = 0
		bs.prevAbsorbsBlank = false
	}

	suffix := text[scanFrom:]
	if suffix == "" {
		return bs.boundary
	}

	savedFence := bs.inFence
	savedAbsorb := bs.prevAbsorbsBlank

	bs.scanLines(suffix, scanFrom)
	bs.updateResumePos(suffix, scanFrom, savedFence, savedAbsorb)
	return bs.boundary
}

// scanLines iterates lines in suffix (starting at byte offset base in the
// original text) and updates boundary/fence/absorb state.
func (bs *boundaryScanner) scanLines(suffix string, base int) {
	lines := strings.SplitAfter(suffix, "\n")
	pos := base
	for i, l := range lines {
		trimmed := strings.TrimRight(l, "\n")
		isLast := i == len(lines)-1
		isIncomplete := isLast && !strings.HasSuffix(l, "\n")
		bs.classifyLine(trimmed, l, isLast, isIncomplete, pos)
		pos += len(l)
	}
}

// classifyLine processes one line and updates the scanner state.
func (bs *boundaryScanner) classifyLine(trimmed, raw string, isLast, isIncomplete bool, pos int) {
	switch {
	case strings.HasPrefix(trimmed, "```"):
		bs.inFence = !bs.inFence
		bs.prevAbsorbsBlank = false
	case !bs.inFence && trimmed == "" && raw != "" && !isLast:
		if !bs.prevAbsorbsBlank {
			bs.boundary = pos + len(raw)
		}
		bs.prevAbsorbsBlank = false
	case trimmed == "" && isLast && raw == "":
		// Trailing-newline artifact.
	default:
		if !isIncomplete {
			bs.prevAbsorbsBlank = lineAbsorbsBlank(trimmed)
		}
	}
}

// lineAbsorbsBlank reports whether a content line belongs to a block type
// (blockquote, list, table) that absorbs a trailing blank line into itself.
func lineAbsorbsBlank(trimmed string) bool {
	t := strings.TrimLeft(trimmed, " \t")
	return strings.HasPrefix(t, ">") ||
		isUnorderedListItem(trimmed) || isOrderedListItem(trimmed) ||
		isTableRow(trimmed) || isTableSeparator(trimmed)
}

// updateResumePos sets resumePos for the next advance call and restores
// fence/absorb state when the tail line is incomplete.
func (bs *boundaryScanner) updateResumePos(suffix string, scanFrom int, savedFence, savedAbsorb bool) {
	if strings.HasSuffix(suffix, "\n") {
		bs.resumePos = scanFrom + len(suffix)
		return
	}
	lastNewline := strings.LastIndex(suffix, "\n")
	if lastNewline >= 0 {
		bs.resumePos = scanFrom + lastNewline + 1
	} else {
		bs.resumePos = scanFrom
	}
	bs.inFence = savedFence
	bs.prevAbsorbsBlank = savedAbsorb
}

// NewIncrementalMDRenderer wraps a fresh MDStreamRenderer for the given width.
func NewIncrementalMDRenderer(width int, theme *Theme) *IncrementalMDRenderer {
	return &IncrementalMDRenderer{r: NewMDStreamRenderer(width, theme)}
}

// Render returns the rendered lines for text, reusing and extending the cached
// stable prefix when text is a pure append of it. On any non-append change
// (edit, shrink, or a document with no stable boundary yet) it re-renders and
// re-caches from scratch.
func (ir *IncrementalMDRenderer) Render(text string) []string {
	if text == "" {
		ir.stableText, ir.stableLines = "", nil
		ir.boundaryScan = boundaryScanner{}
		return nil
	}
	// If the new text is not an append of the cached stable prefix, reset.
	if !strings.HasPrefix(text, ir.stableText) {
		ir.stableText, ir.stableLines = "", nil
		ir.boundaryScan = boundaryScanner{}
	}

	// Find the newest stable boundary, resuming from the cached scan
	// position when possible (B005: avoids O(n) full-text scan per frame).
	boundary := ir.boundaryScan.advance(text)
	if boundary > len(ir.stableText) {
		// The document stabilized further: render only the newly-stable segment
		// and append it to the cache (fresh slice to avoid aliasing the tail).
		newSegment := text[len(ir.stableText):boundary]
		seg := ir.r.Render(newSegment)
		merged := make([]string, 0, len(ir.stableLines)+len(seg))
		merged = append(merged, ir.stableLines...)
		merged = append(merged, seg...)
		ir.stableLines = merged
		ir.stableText = text[:boundary]
	}

	// Render only the open tail and return a fresh concatenation (never alias
	// the cached prefix slice, so a later append can't corrupt prior frames).
	tail := text[len(ir.stableText):]
	tailLines := ir.r.Render(tail)
	out := make([]string, 0, len(ir.stableLines)+len(tailLines))
	out = append(out, ir.stableLines...)
	out = append(out, tailLines...)
	return out
}

// lastStableBoundary returns the byte index just past the last hard block
// boundary in text, or 0 when there is none.
//
// A hard boundary is a blank line OUTSIDE any fenced code block, followed by
// more content, whose PRECEDING block terminates cleanly at that blank line.
// Not all blocks terminate cleanly at a single blank: blockquotes, lists, and
// tables ABSORB a trailing blank line into themselves (see collectBlockquote /
// collectList / collectTable), so a blank immediately after one of those is
// part of the block, not a separator — splitting there would cache a truncated
// block and desync from the whole-document render. A blank line DOES cleanly
// end a paragraph, heading, thematic break, or (already-closed) fence. So a
// candidate blank is accepted as a boundary only when the preceding content
// line is NOT a quote/list-item/table-row.
func lastStableBoundary(text string) int {
	lines := strings.SplitAfter(text, "\n")
	inFence := false
	lastBoundary := 0
	pos := 0
	prevAbsorbsBlank := false
	for i, l := range lines {
		trimmed := strings.TrimRight(l, "\n")
		isLast := i == len(lines)-1
		switch {
		case strings.HasPrefix(trimmed, "```"):
			inFence = !inFence
			prevAbsorbsBlank = false // fence open/close lines terminate cleanly
		case !inFence && trimmed == "" && l != "" && !isLast:
			// Candidate blank-line boundary (real blank line with content after).
			if !prevAbsorbsBlank {
				lastBoundary = pos + len(l)
			}
			prevAbsorbsBlank = false
		case trimmed == "" && isLast && l == "":
			// Trailing-newline artifact: not a real blank line.
		default:
			// A content line: does its block type absorb a following blank?
			t := strings.TrimLeft(trimmed, " \t")
			prevAbsorbsBlank = strings.HasPrefix(t, ">") ||
				isUnorderedListItem(trimmed) || isOrderedListItem(trimmed) ||
				isTableRow(trimmed) || isTableSeparator(trimmed)
		}
		pos += len(l)
	}
	return lastBoundary
}
