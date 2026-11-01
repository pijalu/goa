// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"time"
)

// ── Completion debounce ──

// scheduleAutoComp triggers auto-completion after typing pauses. Cheap
// in-memory completions (slash commands, history search) run synchronously
// as before; filesystem completions (@ prefixes) are debounced and run off
// the UI goroutine — a per-keystroke fd or ReadDir walk on the command loop
// stalled editing on large trees (B49). Rapid keystrokes each push the run
// further out and coalesce into one search for the final prefix; a search
// outlived by newer typing applies nothing (stale-drop).
func (e *Editor) scheduleAutoComp() {
	// History-search mode drives its own completion from the searcher, so it
	// must refresh even when no base completer is configured.
	if e.completer == nil && !e.searchMode {
		return
	}
	if e.searchMode {
		e.updateAutoComp()
		return
	}
	prefix := e.resolveAutoCompPrefix()
	if prefix == "" || !shouldTriggerAutoComp(prefix) {
		// Left the completion context: close the popup and drop any
		// pending run.
		e.stopCompTimer()
		e.clearCompletion()
		if e.tui != nil {
			e.tui.RequestRender()
		}
		return
	}
	if !strings.HasPrefix(prefix, "@") {
		// In-memory completer (commands, arguments): immediate feedback.
		e.updateAutoComp()
		if e.tui != nil && e.compState.Active() {
			e.tui.RequestRender()
		}
		return
	}
	// Filesystem completion (B49): debounce and search off the UI goroutine.
	e.stopCompTimer()
	gen := e.compGen.Add(1)
	e.compTimer = time.AfterFunc(e.compDebounce, func() {
		e.compSearch(prefix, gen)
	})
}

// compSearch runs the @ completion off the UI goroutine and hands the
// result to the command loop, which owns compState. The search itself is
// bounded by the filefind engine (shallow ReadDir first, capped fd rescue
// — B49).
func (e *Editor) compSearch(prefix string, gen int64) {
	items := e.completer.Complete(prefix)
	if e.tui != nil {
		e.tui.ApplySync(func() {
			e.applyCompResult(prefix, items, gen)
			e.tui.RequestRender()
		})
		return
	}
	// No engine attached (headless use, tests): apply directly; callers
	// synchronize through compApplied.
	e.applyCompResult(prefix, items, gen)
}

// applyCompResult installs an async completion result unless the generation
// moved on while the search ran (the user kept typing — stale-drop). Every
// finished search signals compApplied AFTER compState settled, so waiters
// reading compState after a signal are race-free.
func (e *Editor) applyCompResult(prefix string, items []Completion, gen int64) {
	defer e.signalCompApplied()
	if e.compGen.Load() != gen {
		return
	}
	if len(items) == 0 || (len(items) == 1 && items[0].Value == prefix) {
		e.clearCompletion() // nothing to show
		return
	}
	e.compState.Phase = PhaseCommand
	e.compState.Items = items
	e.compState.Idx = 0
	e.compState.Prefix = prefix
	e.compState.UserNavigated = false
}

func (e *Editor) stopCompTimer() {
	if e.compTimer != nil {
		e.compTimer.Stop()
		e.compTimer = nil
	}
}

// signalCompApplied wakes one waiter synchronizing on a finished search.
// Never blocks: extra signals collapse into the buffered slot.
func (e *Editor) signalCompApplied() {
	select {
	case e.compApplied <- struct{}{}:
	default:
	}
}

// ── Autocomplete (during typing) ──

// updateAutoComp queries the completer or history searcher and shows
// suggestions. Called after each character insert/delete.
func (e *Editor) updateAutoComp() {
	if e.searchMode {
		e.updateSearchCompletion()
		return
	}
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
		// Command completion (/) is only triggered when / is at the very start
		// of the input buffer (position 0), not when it appears mid-text
		// (e.g., "read /help"). We check by looking at the buffer content
		// rather than cursor position, since currentPrefix may return a
		// /-prefixed segment from mid-text.
		if strings.HasPrefix(prefix, "/") && !e.isSlashAtBufferStart(prefix) {
			return ""
		}
		return prefix
	}
	fullPrefix := string(e.buf[:e.pos])
	if strings.HasPrefix(fullPrefix, "/") {
		return fullPrefix
	}
	return ""
}

// isSlashAtBufferStart reports whether the /-prefixed prefix corresponds to
// a / character at buffer position 0. This prevents mid-text / from being
// treated as a command (e.g., "read /help" should not trigger command
// completion even though currentPrefix returns "/help").
func (e *Editor) isSlashAtBufferStart(prefix string) bool {
	// Find where the prefix starts in the full buffer
	bufStr := string(e.buf)
	slashPos := strings.Index(bufStr, prefix)
	return slashPos == 0
}

// shouldTriggerAutoComp reports whether the prefix should open the completion popup.
// Trigger conditions: /commands and their :args, @paths, or path-like content.
// Colon alone (text:no-command) does NOT trigger — only /command:args does
// (which starts with / and is already caught by the first condition).
func shouldTriggerAutoComp(prefix string) bool {
	if strings.HasPrefix(prefix, "/") {
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
