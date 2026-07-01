// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

// KillRing is a circular buffer for killed (deleted) text, supporting yank operations.
type KillRing struct {
	entries []string
	index   int // current position for yank
	maxSize int
}

// NewKillRing creates a kill ring with the given max size.
func NewKillRing(maxSize int) *KillRing {
	if maxSize < 1 {
		maxSize = 10
	}
	return &KillRing{maxSize: maxSize, index: -1}
}

// Push adds text to the kill ring. If accumulate is true and the last entry
// was a kill, the text is appended to the last entry instead of creating a new one.
func (kr *KillRing) Push(text string, accumulate bool) {
	if text == "" {
		return
	}
	if accumulate && len(kr.entries) > 0 {
		kr.entries[len(kr.entries)-1] += text
	} else {
		kr.entries = append(kr.entries, text)
		if len(kr.entries) > kr.maxSize {
			kr.entries = kr.entries[1:]
		}
	}
	kr.index = len(kr.entries) - 1
}

// Yank returns the current entry (for yank).
func (kr *KillRing) Yank() string {
	if len(kr.entries) == 0 || kr.index < 0 {
		return ""
	}
	return kr.entries[kr.index]
}

// YankPop returns the next entry (rotate back in the ring).
func (kr *KillRing) YankPop() string {
	if len(kr.entries) == 0 {
		return ""
	}
	kr.index--
	if kr.index < 0 {
		kr.index = len(kr.entries) - 1
	}
	return kr.entries[kr.index]
}

// Len returns the number of entries.
func (kr *KillRing) Len() int {
	return len(kr.entries)
}
