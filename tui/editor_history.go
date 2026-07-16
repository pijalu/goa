// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import "strings"

// ── History ──

func (e *Editor) submit() {
	text := strings.TrimSpace(string(e.buf))
	if text == "" {
		return
	}
	text = e.expandPasteMarkers(text)
	e.addHistory(text)
	e.clearLocked()
	if e.onSubmit != nil {
		cb := e.onSubmit
		captured := text
		e.queueCallback(func() { cb(captured) })
	}
}

func (e *Editor) addHistory(s string) {
	if s == "" {
		return
	}
	// Global dedup: if the entry exists anywhere, remove the old occurrence
	// so the new one is recorded at the most recent position.
	for i, h := range e.history {
		if h == s {
			e.history = append(e.history[:i], e.history[i+1:]...)
			break
		}
	}
	e.history = append(e.history, s)
	if len(e.history) > 100 {
		e.history = e.history[1:]
	}
}

// GetHistory returns the current input history.
func (e *Editor) GetHistory() []string {
	return append([]string(nil), e.history...)
}

// SetHistory replaces the input history with the provided list.
func (e *Editor) SetHistory(history []string) {
	if history == nil {
		history = []string{}
	}
	e.history = history
	e.histIdx = -1
}