// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package ansi

import "github.com/rivo/uniseg"

// ByteSpan is a half-open byte range [Start, End) within a string.
type ByteSpan struct {
	Start int
	End   int
}

// EscapeSpans returns the byte ranges of every ANSI escape sequence in s, in
// document order. Sequences follow the skipANSISequence rules: CSI
// (ESC [ params final), OSC (ESC ] ... BEL/ST), SS3 (ESC O final), and
// two-byte escapes (ESC F0-7E).
func EscapeSpans(s string) []ByteSpan {
	buf := []byte(s)
	var spans []ByteSpan
	for i := 0; i < len(buf); {
		if buf[i] == 0x1b {
			j := skipANSISequence(buf, i)
			spans = append(spans, ByteSpan{Start: i, End: j})
			i = j
			continue
		}
		i++
	}
	return spans
}

// InEscapeSpan reports whether byte offset o lies strictly inside an ANSI
// escape sequence within s.
func InEscapeSpan(s string, o int) bool {
	for _, sp := range EscapeSpans(s) {
		if sp.Start >= o {
			return false // spans are sorted; no later span can contain o
		}
		if o < sp.End {
			return true
		}
	}
	return false
}

// SafeCut reports whether the string s can be split at byte offset o without
// cutting an ANSI escape sequence in half or splitting a grapheme cluster
// (combining marks, ZWJ emoji sequences, regional-indicator flag pairs).
// Offsets 0 and len(s) are trivially safe.
//
// It is the gate used by partial-row emitters to decide between a partial
// column-range update and a full-row rewrite: slicing a row for emission is
// only sound at cell boundaries that keep every escape and every cluster
// whole.
func SafeCut(s string, o int) bool {
	if o <= 0 || o >= len(s) {
		return true
	}
	if InEscapeSpan(s, o) {
		return false
	}
	// Grapheme safety: o must not fall strictly inside a multi-rune cluster.
	// ESC control runes always form their own single-rune clusters, so
	// iterating the raw string is safe — escape-sequence integrity was already
	// checked above.
	gr := uniseg.NewGraphemes(s)
	for gr.Next() {
		start, end := gr.Positions()
		if start >= o {
			return true // clusters are ordered; no later cluster can straddle o
		}
		if o < end {
			return false
		}
	}
	return true
}
