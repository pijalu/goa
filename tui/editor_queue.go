// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import "strings"

// ── Message queue (pending messages merged by default) ──

// QueuePending adds the current buffer to the pending queue and clears it.
func (e *Editor) QueuePending() {
	text := strings.TrimSpace(string(e.buf))
	if text == "" {
		return
	}
	e.queue = append(e.queue, text)
	e.Clear()
}

// FlushQueue returns all queued messages and clears the queue.
func (e *Editor) FlushQueue() []string {
	q := e.queue
	e.queue = nil
	return q
}

// PendingText returns the merged pending message text for display.
func (e *Editor) PendingText() string {
	return strings.Join(e.queue, "\n")
}

// HasPending returns true if there are queued messages.
func (e *Editor) HasPending() bool {
	return len(e.queue) > 0
}
