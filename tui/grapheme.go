// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"github.com/rivo/uniseg"
)

// GraphemeHelper provides grapheme cluster-aware string operations.
// Uses uniseg for Unicode text segmentation (grapheme clusters, words, sentences).

// GraphemeCount returns the number of grapheme clusters in s.
func GraphemeCount(s string) int {
	return uniseg.GraphemeClusterCount(s)
}

// GraphemePosition converts a byte/character position to grapheme cluster position.
// Returns the grapheme cluster index and the byte offset of that cluster.
// If bytePos is 0 or negative, returns (0, 0).
func GraphemePosition(s string, bytePos int) (clusterIdx, clusterByteStart int) {
	if bytePos <= 0 || s == "" {
		return 0, 0
	}
	if bytePos >= len(s) {
		return GraphemeCount(s) - 1, len(s)
	}

	gr := uniseg.NewGraphemes(s)
	idx := 0
	for gr.Next() {
		start, end := gr.Positions()
		if end > bytePos {
			return idx, start
		}
		idx++
		if end >= bytePos {
			return idx, end
		}
	}
	return idx, len(s)
}

// PrevGraphemeStart returns the byte offset of the start of the grapheme
// cluster that contains or precedes bytePos. If bytePos is 0, returns 0.
func PrevGraphemeStart(s string, bytePos int) int {
	if bytePos <= 0 {
		return 0
	}
	if bytePos >= len(s) {
		bytePos = len(s)
	}

	gr := uniseg.NewGraphemes(s)
	for gr.Next() {
		start, end := gr.Positions()
		if end >= bytePos {
			return start
		}
	}
	return bytePos
}

// NextGraphemeEnd returns the byte offset of the end of the grapheme cluster
// that contains bytePos. If bytePos >= len(s), returns len(s).
func NextGraphemeEnd(s string, bytePos int) int {
	if bytePos >= len(s) || s == "" {
		return len(s)
	}

	gr := uniseg.NewGraphemes(s)
	for gr.Next() {
		start, end := gr.Positions()
		if start >= bytePos {
			return end
		}
	}
	return len(s)
}

// WordBreaks returns a slice of byte offsets for word boundaries in s.
func WordBreaks(s string) []int {
	var breaks []int
	remaining := s
	pos := 0
	state := 0
	for len(remaining) > 0 {
		word, rest, newState := uniseg.FirstWordInString(remaining, state)
		if len(word) == 0 {
			break
		}
		pos += len(word)
		breaks = append(breaks, pos)
		remaining = rest
		state = newState
	}
	return breaks
}

// GraphemeClusters returns all grapheme clusters in s as a slice of strings.
func GraphemeClusters(s string) []string {
	var clusters []string
	gr := uniseg.NewGraphemes(s)
	for gr.Next() {
		clusters = append(clusters, gr.Str())
	}
	return clusters
}

// GraphemeAt returns the grapheme cluster at byte position pos in s.
// Returns empty string if pos is out of range.
func GraphemeAt(s string, pos int) string {
	gr := uniseg.NewGraphemes(s)
	for gr.Next() {
		start, end := gr.Positions()
		if start <= pos && pos < end {
			return gr.Str()
		}
	}
	return ""
}
