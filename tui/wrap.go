// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"github.com/pijalu/goa/internal/ansi"
	"github.com/rivo/uniseg"
)

// wrapChunk is one visual (wrapped) line: the exact text to display plus the
// half-open rune range [Start, End) it covers in the source string.
//
// Because Text is a faithful slice of the source, the editor's display, cursor
// placement, and cursor navigation can all share this single layout (one source
// of truth) instead of re-deriving it three different ways that drift apart —
// which is what corrupted the input cursor (see wrapChunks doc).
type wrapChunk struct {
	Text  string
	Start int // rune index in source (inclusive)
	End   int // rune index in source (exclusive)
}

// wrapChunks wraps source (which may contain '\n') into visual-line chunks at
// the given width. Wrapping is grapheme-cluster-aware and word-based:
//
//   - A line breaks at the space before a word that would overflow; the single
//     boundary space is consumed (it is neither shown at the end of the wrapped
//     line nor at the start of the next).
//   - Interior runs of spaces are preserved exactly (multiple spaces are NOT
//     collapsed), so what is rendered is exactly what was typed. This is what
//     keeps the hardware cursor on the glyph under it: the displayed width and
//     the cursor's column are computed from the same faithful text.
//   - A word longer than the width is broken by grapheme cluster so it never
//     splits a multi-rune cluster (e.g. a ZWJ emoji).
//
// Each chunk's Start/End are rune indices into source, so a cursor position
// (also a rune index) maps directly onto a chunk without any second wrapping
// pass.
func wrapChunks(source string, width int) []wrapChunk {
	if width < 1 {
		width = 1
	}
	runes := []rune(source)
	if len(runes) == 0 {
		return []wrapChunk{{Text: "", Start: 0, End: 0}}
	}

	var out []wrapChunk
	base := 0 // rune offset of the current logical line
	for base <= len(runes) {
		end := base
		for end < len(runes) && runes[end] != '\n' {
			end++
		}
		for _, c := range wrapLineChunks(runes[base:end], width) {
			out = append(out, wrapChunk{
				Text:  c.Text,
				Start: base + c.Start,
				End:   base + c.End,
			})
		}
		if end >= len(runes) {
			break
		}
		base = end + 1 // skip the '\n'
	}
	if len(out) == 0 {
		out = []wrapChunk{{Text: "", Start: 0, End: 0}}
	}
	return out
}

// wrapLineChunks wraps a single logical line (no '\n') into chunks. See
// wrapChunks for the wrapping rules. Layout state is owned by a lineLayout so
// each step stays small and focused.
func wrapLineChunks(runes []rune, width int) []wrapChunk {
	if len(runes) == 0 {
		return []wrapChunk{{Text: "", Start: 0, End: 0}}
	}
	if ansi.Width(string(runes)) <= width {
		return []wrapChunk{{Text: string(runes), Start: 0, End: len(runes)}}
	}

	lay := &lineLayout{runes: runes, width: width}
	for _, tk := range tokenize(runes) {
		lay.handle(tk)
	}
	lay.finish()
	return lay.chunks
}

// lineLayout accumulates wrapChunks for one logical line. It tracks the
// current (not yet flushed) line runes[lineStart:lineEnd] plus an optional run
// of separator whitespace (pending) sitting after it.
type lineLayout struct {
	chunks    []wrapChunk
	runes     []rune
	width     int
	lineStart int
	lineEnd   int
	lineW     int
	pendStart int
	pendEnd   int
	pendW     int
	hasPend   bool
}

// handle dispatches one token to its handler.
func (l *lineLayout) handle(tk token) {
	switch {
	case tk.isSpace:
		l.handleSpace(tk)
	case tk.width > l.width:
		l.handleLongWord(tk)
	default:
		l.handleWord(tk)
	}
}

// handleSpace accumulates a separator whitespace run after the current line.
func (l *lineLayout) handleSpace(tk token) {
	if !l.hasPend {
		l.pendStart = tk.rStart
	}
	l.pendEnd = tk.rEnd
	l.pendW += tk.width
	l.hasPend = true
}

// handleLongWord flushes the current line (dropping boundary spaces), then
// breaks the over-width word by grapheme cluster. The last sub-chunk becomes
// the new current line so later tokens can extend it.
func (l *lineLayout) handleLongWord(tk token) {
	if l.lineEnd > l.lineStart {
		l.flush()
	}
	l.hasPend, l.pendW = false, 0
	subs := breakWord(l.runes, tk.rStart, tk.rEnd, l.width)
	last := subs[len(subs)-1]
	l.chunks = append(l.chunks, subs[:len(subs)-1]...)
	l.lineStart, l.lineEnd, l.lineW = last.Start, last.End, ansi.Width(last.Text)
}

// handleWord places a normal word: break to a new line if it (plus its
// separator) does not fit, otherwise commit the separator and word.
func (l *lineLayout) handleWord(tk token) {
	if l.lineEnd > l.lineStart && l.lineW+l.pendW+tk.width > l.width {
		l.flush()
		l.lineStart, l.lineEnd, l.lineW = tk.rStart, tk.rEnd, tk.width
		l.hasPend, l.pendW = false, 0
		return
	}
	l.commitPending()
	l.lineEnd = tk.rEnd
	l.lineW += tk.width
}

// commitPending folds the pending separator whitespace into the current line.
// On a fresh line the spaces are leading and kept; otherwise they are interior.
func (l *lineLayout) commitPending() {
	if !l.hasPend {
		return
	}
	if l.lineEnd == l.lineStart {
		l.lineStart = l.pendStart
	}
	l.lineW += l.pendW
	l.lineEnd = l.pendEnd
	l.hasPend, l.pendW = false, 0
}

// finish appends any trailing whitespace to the last line and flushes it.
// Trailing whitespace is never a wrap point.
func (l *lineLayout) finish() {
	l.commitPending()
	l.flush()
}

// flush emits the current line as a chunk.
func (l *lineLayout) flush() {
	l.chunks = append(l.chunks, wrapChunk{
		Text:  string(l.runes[l.lineStart:l.lineEnd]),
		Start: l.lineStart,
		End:   l.lineEnd,
	})
}

// token is a maximal run of whitespace or non-whitespace grapheme clusters.
type token struct {
	isSpace bool
	rStart  int
	rEnd    int
	width   int
}

func tokenize(runes []rune) []token {
	segs := segmentGraphemes(runes)
	var toks []token
	i := 0
	for i < len(segs) {
		j := i + 1
		for j < len(segs) && segs[j].whitespace == segs[i].whitespace {
			j++
		}
		w := 0
		for k := i; k < j; k++ {
			w += segs[k].width
		}
		toks = append(toks, token{
			isSpace: segs[i].whitespace,
			rStart:  segs[i].rStart,
			rEnd:    segs[j-1].rEnd,
			width:   w,
		})
		i = j
	}
	return toks
}

// gseg is a precomputed grapheme cluster with its rune range and display width.
type gseg struct {
	rStart     int
	rEnd       int
	width      int
	whitespace bool
}

// segmentGraphemes splits runes into grapheme clusters, each tagged with its
// rune range (local to runes), display width, and whether it is whitespace.
func segmentGraphemes(runes []rune) []gseg {
	segs := make([]gseg, 0, len(runes))
	gr := uniseg.NewGraphemes(string(runes))
	idx := 0
	for gr.Next() {
		cluster := gr.Str()
		n := len([]rune(cluster))
		first := []rune(cluster)[0]
		segs = append(segs, gseg{
			rStart:     idx,
			rEnd:       idx + n,
			width:      ansi.ClusterWidth(cluster),
			whitespace: isSpaceRune(first),
		})
		idx += n
	}
	return segs
}

// breakWord splits a single word (no whitespace) covering runes[rStart:rEnd]
// into chunks no wider than width, never splitting a grapheme cluster.
func breakWord(runes []rune, rStart, rEnd, width int) []wrapChunk {
	var sub []wrapChunk
	segs := segmentGraphemes(runes[rStart:rEnd])
	chunkStart, curW := 0, 0
	flush := func(localEnd int) {
		sub = append(sub, wrapChunk{
			Text:  string(runes[rStart+chunkStart : rStart+localEnd]),
			Start: rStart + chunkStart,
			End:   rStart + localEnd,
		})
		chunkStart, curW = localEnd, 0
	}
	for _, s := range segs {
		if curW+s.width > width && curW > 0 {
			flush(s.rStart)
		}
		curW += s.width
	}
	if len(segs) > 0 {
		flush(segs[len(segs)-1].rEnd)
	}
	return sub
}

// cursorChunk locates the visual line (chunk index) and the rune offset within
// it for a cursor at rune position pos in source.
//
//   - pos inside [Start, End) -> that chunk.
//   - pos == End at a line-ending newline (the cursor rests on the newline) ->
//     that chunk, at its end.
//   - pos == End at a wrap boundary or at EOF -> the next chunk, or the last
//     chunk if there is no next one.
func cursorChunk(chunks []wrapChunk, source string, pos int) (idx, offset int) {
	runes := []rune(source)
	if pos > len(runes) {
		pos = len(runes)
	}
	for i, c := range chunks {
		if pos >= c.Start && pos < c.End {
			return i, pos - c.Start
		}
		if pos == c.End && pos < len(runes) && runes[pos] == '\n' {
			return i, c.End - c.Start
		}
	}
	last := chunks[len(chunks)-1]
	off := pos - last.Start
	if off < 0 {
		off = 0
	}
	if max := last.End - last.Start; off > max {
		off = max
	}
	return len(chunks) - 1, off
}

// runeOffsetToByte returns the byte position in s at the given rune offset.
// Used to insert the cursor marker exactly at a rune (grapheme) boundary.
func runeOffsetToByte(s string, runeOff int) int {
	if runeOff <= 0 {
		return 0
	}
	n := 0
	for i := range s {
		if n == runeOff {
			return i
		}
		n++
	}
	return len(s)
}

func isSpaceRune(r rune) bool {
	return r == ' ' || r == '\t'
}
