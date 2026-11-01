// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"sync"
	"testing"
	"time"
)

// B49 regression tests: @-completion reruns a filesystem search on every
// keystroke on the UI goroutine, which can stall editing while fd walks a
// large tree. The search must be asynchronous and debounced: keystrokes
// never block on it and coalesce into one search for the final prefix; a
// search outlived by newer typing applies nothing (stale-drop). Cheap
// in-memory completions (slash commands) stay synchronous.

// countingCompleter counts Complete calls (async-safe) and returns canned
// items, letting tests observe when and how often the search runs.
type countingCompleter struct {
	mu    sync.Mutex
	calls int
	items []Completion
}

func (c *countingCompleter) Complete(prefix string) []Completion {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return c.items
}

func (c *countingCompleter) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// delayedCompleter slows the search so tests can type while it is in
// flight (stale-drop scenarios).
type delayedCompleter struct {
	countingCompleter
	delay time.Duration
}

func (c *delayedCompleter) Complete(prefix string) []Completion {
	time.Sleep(c.delay)
	return c.countingCompleter.Complete(prefix)
}

// waitAutoComp waits for n finished async searches (applied or
// stale-dropped). Each signal happens after compState was (not) mutated, so
// compState reads that follow are race-free.
func (e *Editor) waitAutoComp(n int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for i := 0; i < n; i++ {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false
		}
		select {
		case <-e.compApplied:
		case <-time.After(remaining):
			return false
		}
	}
	return true
}

func TestEditor_AutoComp_FilePrefixAsyncDebounced(t *testing.T) {
	ed := NewEditor()
	ed.SetFocused(true)
	cc := &countingCompleter{items: []Completion{{Value: "@ab.md", Display: "@ab.md"}}}
	ed.SetCompleter(cc)
	ed.compDebounce = 15 * time.Millisecond

	for _, ch := range []string{"@", "a", "b"} {
		ed.HandleInput(ch)
	}

	// Editing must never block on the search: nothing ran synchronously.
	if n := cc.count(); n != 0 {
		t.Fatalf("completion ran synchronously on keystrokes: %d call(s), want 0", n)
	}

	// The coalesced search eventually runs exactly once for the final prefix
	// and its result lands in the popup.
	if !ed.waitAutoComp(1, 2*time.Second) {
		t.Fatal("debounced completion never applied")
	}
	if n := cc.count(); n != 1 {
		t.Errorf("Complete calls after debounce = %d, want 1 (keystrokes must coalesce)", n)
	}
	if !ed.compState.Active() {
		t.Error("expected completion to be active after the async search applied")
	}
	if ed.compState.Prefix != "@ab" {
		t.Errorf("applied prefix = %q, want @ab", ed.compState.Prefix)
	}
}

func TestEditor_AutoComp_BackspaceAsync(t *testing.T) {
	ed := NewEditor()
	ed.SetFocused(true)
	cc := &countingCompleter{items: []Completion{{Value: "@ab.md", Display: "@ab.md"}}}
	ed.SetCompleter(cc)
	ed.compDebounce = 15 * time.Millisecond

	ed.SetText("@ab")
	ed.HandleInput(KeyBackspace)

	if n := cc.count(); n != 0 {
		t.Fatalf("backspace ran completion synchronously: %d call(s), want 0", n)
	}
	if !ed.waitAutoComp(1, 2*time.Second) {
		t.Fatal("debounced completion never ran after backspace")
	}
	if n := cc.count(); n != 1 {
		t.Errorf("Complete calls = %d, want 1", n)
	}
	if ed.compState.Prefix != "@a" {
		t.Errorf("applied prefix = %q, want @a", ed.compState.Prefix)
	}
}

func TestEditor_AutoComp_StaleResultDropped(t *testing.T) {
	ed := NewEditor()
	ed.SetFocused(true)
	cc := &delayedCompleter{delay: 80 * time.Millisecond}
	cc.items = []Completion{{Value: "@ab.md", Display: "@ab.md"}}
	ed.SetCompleter(cc)
	ed.compDebounce = 5 * time.Millisecond

	ed.HandleInput("@ab")
	time.Sleep(20 * time.Millisecond) // search #1 in flight
	ed.HandleInput("c")               // invalidates #1, schedules #2

	// Both searches must finish: #1 stale-dropped, #2 applied.
	if !ed.waitAutoComp(2, 2*time.Second) {
		t.Fatal("async searches never finished")
	}
	if n := cc.count(); n != 2 {
		t.Errorf("Complete calls = %d, want 2", n)
	}
	// The popup must reflect the current prefix, not the stale @ab result.
	if ed.compState.Prefix != "@abc" {
		t.Errorf("applied prefix = %q, want @abc (stale @ab result must be dropped)", ed.compState.Prefix)
	}
}

func TestEditor_AutoComp_CommandPrefixStaysSync(t *testing.T) {
	ed := NewEditor()
	ed.SetFocused(true)
	cc := &countingCompleter{items: []Completion{{Value: "/help", Display: "/help"}}}
	ed.SetCompleter(cc)
	ed.compDebounce = 15 * time.Millisecond

	ed.HandleInput("/")

	// Slash commands are in-memory lookups: keep immediate feedback.
	if n := cc.count(); n != 1 {
		t.Fatalf("command completion calls = %d, want exactly 1 (synchronous)", n)
	}
	if !ed.compState.Active() {
		t.Error("command completion should be active immediately")
	}
}
