// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
)

// ── Completion debounce ──

// scheduleAutoComp triggers auto-completion on the next render cycle.
// The actual completion runs synchronously on the TUI goroutine via
// a render request, so no timer or goroutine is needed.
// Debounce is handled by the render loop's pacing (50ms interval).
func (e *Editor) scheduleAutoComp() {
	if e.completer == nil {
		return
	}
	// Cancel any pending timer
	if e.compTimer != nil {
		e.compTimer.Stop()
		e.compTimer = nil
	}
	// Run completion synchronously now for immediate feedback
	e.updateAutoComp()
	// Request a render if we have a TUI reference
	if e.tui != nil && e.compState.Active() {
		e.tui.RequestRender()
	}
}

// ── Autocomplete (during typing) ──

// updateAutoComp queries the completer and shows suggestions.
// Called after each character insert/delete.
func (e *Editor) updateAutoComp() {
	if e.completer == nil {
		e.clearCompletion()
		return
	}
	prefix := e.resolveAutoCompPrefix()
	if prefix == "" || !shouldTriggerAutoComp(prefix) {
		e.clearCompletion()
		return
	}

	items := e.completer.Complete(prefix)
	if len(items) == 0 {
		e.clearCompletion()
		return
	}
	if len(items) == 1 && items[0].Value == prefix {
		e.clearCompletion() // exact match = no popup
		return
	}
	// Show popup but do NOT auto-insert, even for single match
	e.compState.Phase = PhaseCommand
	e.compState.Items = items
	e.compState.Idx = 0
	e.compState.Prefix = prefix
	e.compState.UserNavigated = false
}

func (e *Editor) resolveAutoCompPrefix() string {
	prefix := e.currentPrefix()
	if prefix != "" {
		return prefix
	}
	fullPrefix := string(e.buf[:e.pos])
	if strings.HasPrefix(fullPrefix, "/") {
		return fullPrefix
	}
	return ""
}

// shouldTriggerAutoComp reports whether the prefix should open the completion popup.
// Trigger conditions: /commands, /cmd:args, @paths, or path-like content.
func shouldTriggerAutoComp(prefix string) bool {
	if strings.HasPrefix(prefix, "/") || strings.Contains(prefix, ":") {
		return true
	}
	if strings.HasPrefix(prefix, "@") {
		return true
	}
	return strings.Contains(prefix, "/") && len(prefix) >= 2
}

func (e *Editor) AutoCompActive() bool {
	return e.compState.Active()
}

// AtFirstLine returns true if the cursor is on the first visual line of content.
func (e *Editor) AtFirstLine() bool {
	return e.pos <= 0
}

// AtLastLine returns true if the cursor is on the last visual line of content.
func (e *Editor) AtLastLine() bool {
	return e.pos >= len(e.buf)
}
