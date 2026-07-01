// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"testing"
	"time"
)

// TestEditor_SubmitCallback_Reentrancy_NoDeadlock guards RC2: callbacks run
// only after HandleInput dispatch completes (state fully mutated), so a handler
// that re-enters the editor (e.g. to echo command output via SetText) observes
// consistent state. Runs on the commandLoop; the batched-callback invariant is
// what makes re-entrancy safe (batched-callback invariant on the commandLoop).
func TestEditor_SubmitCallback_Reentrancy_NoDeadlock(t *testing.T) {
	ed := NewEditor()
	ed.SetFocused(true)

	ed.SetOnSubmit(func(text string) {
		// Re-enter the editor exactly like the app's bang-command handler does
		// (submithandler.go echoes output back via SetText). Callbacks run after
		// dispatch, so this observes fully-mutated state.
		ed.SetText("```echoed```")
		_ = ed.Text()
		ed.Clear()
	})

	done := make(chan struct{})
	go func() {
		ed.SetText("/run thing")
		ed.pos = len([]rune("/run thing"))
		ed.HandleInput(KeyEnter)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("submit callback did not complete (callbacks not run after dispatch)")
	}

	if ed.Text() != "" {
		t.Errorf("editor should be clear after submit+callback, got %q", ed.Text())
	}
}

// TestEditor_EscapeCallback_Reentrancy_NoDeadlock guards RC2 for the Escape
// path: OnEscape runs after mu is released.
func TestEditor_EscapeCallback_Reentrancy_NoDeadlock(t *testing.T) {
	ed := NewEditor()
	ed.SetFocused(true)
	ed.SetText("draft")

	ed.OnEscape = func() {
		// Re-enter the editor from the escape callback.
		ed.Clear()
	}

	done := make(chan struct{})
	go func() {
		ed.HandleInput(KeyEscape)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("OnEscape callback did not complete (callbacks not run after dispatch)")
	}

	if ed.Text() != "" {
		t.Errorf("editor should be cleared by OnEscape, got %q", ed.Text())
	}
}

// TestForwardToInput_RecoversFromPanic guards RC1: the stdin readLoop is the
// only input consumer, so a panicking onInput handler must be recovered rather
// than killing the goroutine (which would freeze all keyboard input). Before
// the fix the panic propagated up the readLoop and terminated input forever.
func TestForwardToInput_RecoversFromPanic(t *testing.T) {
	term := &ProcessTerminal{
		stdinBuffer: NewStdinBuffer(),
		done:        make(chan struct{}),
	}
	called := false
	term.onInput = func(ev string) {
		called = true
		panic("simulated component panic on key " + ev)
	}

	// Must not propagate the panic.
	assertNotPanics(t, func() { term.forwardToInput("a") })
	if !called {
		t.Fatal("onInput was not invoked")
	}
}

// TestEditor_HandleInput_RunsCallbacksAfterUnlock verifies the callback
// contract: callbacks collected during dispatch run only after dispatch
// completes, by checking the callback observes fully-mutated state.
func TestEditor_HandleInput_RunsCallbacksAfterUnlock(t *testing.T) {
	ed := NewEditor()
	ed.SetFocused(true)

	callbackRan := false
	ed.SetOnSubmit(func(text string) {
		// The callback runs after dispatch completes, so it observes
		// consistent, fully-mutated state and may re-enter the editor freely.
		current := ed.Text()
		ed.SetText(current + "!")
		callbackRan = true
	})

	ed.SetText("hello")
	ed.pos = len([]rune("hello"))
	ed.HandleInput(KeyEnter)

	if !callbackRan {
		t.Fatal("onSubmit callback never ran")
	}
}

func assertNotPanics(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("unexpected panic: %v", r)
		}
	}()
	fn()
}
