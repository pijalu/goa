// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"io"
	"sync"
	"testing"
	"time"
)

// escapeTestTerminal builds a ProcessTerminal wired to capture forwarded
// input events without touching real stdin/stdout.
func escapeTestTerminal() (*ProcessTerminal, *sync.Mutex, *[]string) {
	var mu sync.Mutex
	var events []string
	term := &ProcessTerminal{
		stdinBuffer: NewStdinBuffer(),
		done:        make(chan struct{}),
	}
	term.onInput = func(s string) {
		mu.Lock()
		events = append(events, s)
		mu.Unlock()
	}
	return term, &mu, &events
}

func collectEvents(mu *sync.Mutex, events *[]string) []string {
	mu.Lock()
	defer mu.Unlock()
	return append([]string(nil), (*events)...)
}

func waitForEvents(t *testing.T, mu *sync.Mutex, events *[]string, want int) []string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got := collectEvents(mu, events)
		if len(got) >= want {
			return got
		}
		time.Sleep(time.Millisecond)
	}
	return collectEvents(mu, events)
}

// TestPollEscapeDebounce_TimerDispatchedThenLateKey reproduces the W2
// Windows failure: the fallback timer dispatches Escape, then the blocked
// continuation read returns the user's next keypress. The key must arrive
// unmodified and Escape must be dispatched exactly once.
func TestPollEscapeDebounce_TimerDispatchedThenLateKey(t *testing.T) {
	oldRead := stdinRead
	oldNoPoll := escapeDebounceNoPoll
	defer func() {
		stdinRead = oldRead
		escapeDebounceNoPoll = oldNoPoll
	}()
	escapeDebounceNoPoll = false

	term, mu, events := escapeTestTerminal()
	defer close(term.done)

	// Continuation read blocks until the test releases it with the next
	// keypress — simulating the unsupported-deadline blocking Read.
	release := make(chan struct{})
	stdinRead = func(buf []byte) (int, error) {
		<-release
		return copy(buf, "j"), nil
	}

	term.startEscapeDebounce()
	go term.pollEscapeDebounce()

	// Let the fallback timer (2*escapeDebounceTimeout) dispatch Escape.
	got := waitForEvents(t, mu, events, 1)
	if len(got) != 1 || got[0] != "\x1b" {
		t.Fatalf("after timer: got %q, want exactly [ESC]", got)
	}

	// The blocked read now returns the next keypress.
	close(release)
	got = waitForEvents(t, mu, events, 2)
	want := []string{"\x1b", "j"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("final events: got %q, want %q (Esc once, next key unmodified)", got, want)
	}
}

// TestPollEscapeDebounce_MergesWhilePending covers the fast-path: bytes
// arriving before the timeout merge with the pending escape as one
// sequence, and the fallback timer never fires a duplicate Escape.
func TestPollEscapeDebounce_MergesWhilePending(t *testing.T) {
	oldRead := stdinRead
	oldNoPoll := escapeDebounceNoPoll
	defer func() {
		stdinRead = oldRead
		escapeDebounceNoPoll = oldNoPoll
	}()
	escapeDebounceNoPoll = false

	term, mu, events := escapeTestTerminal()
	defer close(term.done)

	stdinRead = func(buf []byte) (int, error) {
		return copy(buf, "[A"), nil
	}

	term.startEscapeDebounce()
	term.pollEscapeDebounce()

	got := collectEvents(mu, events)
	if len(got) != 1 || got[0] != "\x1b[A" {
		t.Fatalf("got %q, want [ESC[A]", got)
	}

	// Wait past the fallback timer: no duplicate Escape may appear.
	time.Sleep(escapeDebounceTimeout * 4)
	got = collectEvents(mu, events)
	if len(got) != 1 {
		t.Fatalf("after debounce window: got %q, want exactly one event", got)
	}
}

// TestPollEscapeDebounce_TimeoutDispatchesEscapeOnce covers the plain
// timeout path: no continuation bytes arrive before the deadline, so the
// poll dispatches Escape; the stopped timer must not fire a second one.
func TestPollEscapeDebounce_TimeoutDispatchesEscapeOnce(t *testing.T) {
	oldRead := stdinRead
	oldNoPoll := escapeDebounceNoPoll
	defer func() {
		stdinRead = oldRead
		escapeDebounceNoPoll = oldNoPoll
	}()
	escapeDebounceNoPoll = false

	term, mu, events := escapeTestTerminal()
	defer close(term.done)

	stdinRead = func(buf []byte) (int, error) {
		return 0, io.EOF // deadline expiry surfaces as an error
	}

	term.startEscapeDebounce()
	term.pollEscapeDebounce()

	got := collectEvents(mu, events)
	if len(got) != 1 || got[0] != "\x1b" {
		t.Fatalf("got %q, want [ESC]", got)
	}

	time.Sleep(escapeDebounceTimeout * 4)
	got = collectEvents(mu, events)
	if len(got) != 1 {
		t.Fatalf("after debounce window: got %q, want exactly one Escape", got)
	}
}

// TestPollEscapeDebounce_NoPollPath covers the Windows behavior: the
// continuation poll never block-reads, leaving the pending Escape to the
// fallback timer; the next keypress flows through the normal readLoop
// path unmodified.
func TestPollEscapeDebounce_NoPollPath(t *testing.T) {
	oldNoPoll := escapeDebounceNoPoll
	oldRead := stdinRead
	defer func() {
		escapeDebounceNoPoll = oldNoPoll
		stdinRead = oldRead
	}()
	escapeDebounceNoPoll = true

	stdinRead = func(buf []byte) (int, error) {
		t.Error("stdinRead must not be called when escapeDebounceNoPoll is set")
		return 0, io.EOF
	}

	term, mu, events := escapeTestTerminal()
	defer close(term.done)

	term.startEscapeDebounce()
	term.pollEscapeDebounce() // must return immediately without reading

	// Escape still pending: a subsequent multi-byte read is not merged.
	if !term.escapePending.Load() {
		t.Fatal("escape should still be pending after no-poll path")
	}

	// Fallback timer dispatches Escape exactly once.
	got := waitForEvents(t, mu, events, 1)
	if len(got) != 1 || got[0] != "\x1b" {
		t.Fatalf("got %q, want [ESC]", got)
	}
}
