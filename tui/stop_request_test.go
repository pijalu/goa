// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"testing"
	"time"
)

// newStopTestTUI builds an engine with a focused, EMPTY editor — the state
// where Ctrl+C / Ctrl+D mean "quit" for the interactive TUI.
func newStopTestTUI(t *testing.T) (*TUI, *int) {
	t.Helper()
	term := &fakeTerminal{w: 80, h: 24}
	engine := NewTUI(term)
	ed := NewEditor()
	ed.SetTUI(engine)
	ed.SetFocused(true)
	engine.AddChild(ed)
	engine.SetFocus(ed)
	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(engine.Stop)
	stops := new(int)
	engine.OnStopRequest = func() { *stops++ }
	return engine, stops
}

// waitStopped polls an engine's stop channel.
func waitStopped(t *testing.T, engine *TUI) bool {
	t.Helper()
	select {
	case <-engine.Stopped():
		return true
	case <-time.After(500 * time.Millisecond):
		return false
	}
}

// TestStopRequest_HookOwnsCtrlC pins the server-mode contract: with a host
// hook installed, Ctrl+C on an empty editor routes to the hook and the
// engine keeps running — the session's interrupt key can never take a
// served process down.
func TestStopRequest_HookOwnsCtrlC(t *testing.T) {
	engine, stops := newStopTestTUI(t)
	engine.SendKey(KeyCtrlC)
	if *stops != 1 {
		t.Fatalf("after Ctrl+C stops = %d, want 1", *stops)
	}
	if waitStopped(t, engine) {
		t.Fatal("engine stopped despite the host hook owning the stop request")
	}
}

// TestCtrlDAlwaysQuits pins the EOF contract: Ctrl+D on an empty editor
// stops the session even when a host hook is installed — the served
// session's owner asked for exactly that (it is not the process-level
// Ctrl+C, which stays the console's).
func TestCtrlDAlwaysQuits(t *testing.T) {
	engine, stops := newStopTestTUI(t)
	engine.SendKey(KeyCtrlD)
	if !waitStopped(t, engine) {
		t.Fatal("Ctrl+D on an empty editor did not stop the engine")
	}
	if *stops != 0 {
		t.Fatalf("hook calls = %d, want 0 (Ctrl+D quits directly)", *stops)
	}
}

// TestStopRequest_NilHookQuits pins the interactive-TUI contract: no hook,
// Ctrl+C on an empty editor quits, exactly as documented in the header.
func TestStopRequest_NilHookQuits(t *testing.T) {
	term := &fakeTerminal{w: 80, h: 24}
	engine := NewTUI(term)
	ed := NewEditor()
	ed.SetTUI(engine)
	ed.SetFocused(true)
	engine.AddChild(ed)
	engine.SetFocus(ed)
	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(engine.Stop)
	engine.SendKey(KeyCtrlC)
	if !waitStopped(t, engine) {
		t.Fatal("Ctrl+C on an empty editor did not stop the engine")
	}
}

// TestStopRequest_NonEmptyEditorClears pins the other half of handleCtrlC:
// with content in the editor, Ctrl+C clears it and never stops anything.
func TestStopRequest_NonEmptyEditorClears(t *testing.T) {
	engine, stops := newStopTestTUI(t)
	ed, ok := engine.Focused().(*Editor)
	if !ok {
		t.Fatalf("focused component = %T, want the editor", engine.Focused())
	}
	ed.SetText("draft text")
	engine.SendKey(KeyCtrlC)
	if ed.Text() != "" {
		t.Fatalf("editor text = %q, want cleared", ed.Text())
	}
	if *stops != 0 {
		t.Fatalf("stops = %d, want 0 (clear, not quit)", *stops)
	}
	if waitStopped(t, engine) {
		t.Fatal("engine stopped on a non-empty-editor Ctrl+C")
	}
}
