// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"testing"
)

// ── Editor Ctrl+C ──

func TestEditor_CtrlC_FallsThroughToTUI(t *testing.T) {
	e := NewEditor()
	e.SetText("hello world")
	e.SetFocused(true)

	// The Ctrl+C case was removed from Editor.HandleInput.
	// The decoded key "ctrl+c" does not start with \x1b so isPrintable
	// returns true and each char of "ctrl+c" is inserted as text.
	// This is correct — the TUI-level handler (tui.go:handleKey)
	// intercepts Ctrl+C BEFORE routing to the Editor in production.
	e.HandleInput(KeyCtrlC)

	// Verify Editor no longer has an early return for Ctrl+C
	// (the key name chars get inserted, confirming the case was removed)
	if e.Text() == "hello world" {
		t.Error("Editor's Ctrl+C early return was NOT removed")
	}
}

func TestEditor_Escape_DoesNotClearBuffer(t *testing.T) {
	e := NewEditor()
	e.SetFocused(true)
	e.SetText("hello")

	e.HandleInput(KeyEscape)

	if e.Text() != "hello" {
		t.Errorf("Escape should NOT clear buffer, got %q", e.Text())
	}
}

func TestEditor_Escape_WithCompletion_ClearsPopupOnly(t *testing.T) {
	e := NewEditor()
	e.SetFocused(true)
	e.SetText("/h")
	e.pos = 2

	// Trigger completion (simulate having items)
	e.compState.Phase = PhaseCommand
	e.compState.Items = []Completion{
		{Value: "/help", Display: "/help"},
	}
	e.compState.Idx = 0
	e.compState.Prefix = "/h"

	if !e.compState.Active() {
		t.Fatal("completion should be active")
	}

	// Escape should close the popup but NOT clear the buffer
	e.HandleInput(KeyEscape)

	if e.compState.Active() {
		t.Error("Escape should deactivate completion")
	}
	if e.Text() != "/h" {
		t.Errorf("Text should remain unchanged, got %q", e.Text())
	}
}

func TestEditor_Backspace_DeletesChar(t *testing.T) {
	e := NewEditor()
	e.SetFocused(true)
	e.SetText("/h")
	e.pos = 2

	// Backspace should remove last char
	e.HandleInput(KeyBackspace)

	if e.Text() != "/" {
		t.Errorf("Expected text '/', got %q", e.Text())
	}
}

// ── Editor Ctrl+C does not match deprecated bindings ──

func TestEditor_CtrlC_DoesNotMatchCopyBinding(t *testing.T) {
	// With KbCopy removed, Ctrl+C should not match the old "copy" binding
	e := NewEditor()

	// Verify that no keybinding named "input.copy" exists
	if e.kb.Matches(KeyCtrlC, "input.copy") {
		t.Error("'input.copy' binding should not exist, Ctrl+C should not match it")
	}
}

func TestEditor_CtrlC_OnlyMatchesSelectCancel(t *testing.T) {
	// Ctrl+C should ONLY match the 'select.cancel' binding (for Selector overlay),
	// nothing else in the editor context.
	e := NewEditor()
	allowedMatch := KbSelectCancel

	for name := range DefaultKeybindings() {
		if name == allowedMatch {
			continue // skip select.cancel — it's allowed for overlays
		}
		if e.kb.Matches(KeyCtrlC, name) {
			t.Errorf("Ctrl+C should not match keybinding %q (only %q is allowed)", name, allowedMatch)
		}
	}
}

// ── Keybindings ──

func TestKeybindings_KbDeleteLastMsg_Exists(t *testing.T) {
	km := DefaultKeybindingsManager()

	if !km.Matches("ctrl+shift+backspace", KbDeleteLastMsg) {
		t.Error("KbDeleteLastMsg should match 'ctrl+shift+backspace'")
	}
}

func TestKeybindings_KbDeleteWordBack_IncludesCtrlBackspace(t *testing.T) {
	km := DefaultKeybindingsManager()

	if !km.Matches("ctrl+backspace", KbDeleteWordBack) {
		t.Error("KbDeleteWordBack should match 'ctrl+backspace'")
	}
}

func TestKeybindings_NoUnusedCtrlC(t *testing.T) {
	// Ctrl+C should only be used by select.cancel (for Selector overlay)
	// All other bindings should NOT use "ctrl+c" as a key.
	allowedBindings := map[string]bool{
		KbSelectCancel: true,
	}
	for name, def := range DefaultKeybindings() {
		if allowedBindings[name] {
			continue
		}
		for _, k := range def.DefaultKeys {
			if k == "ctrl+c" {
				t.Errorf("Keybinding %q still uses 'ctrl+c' (should be reserved for clear/exit)", name)
			}
		}
	}
}

// ── Input Ctrl+C ──

func TestInput_CtrlC_NoEarlyReturn(t *testing.T) {
	inp := NewInput()
	inp.SetFocused(true)
	inp.SetText("hello")

	// Input never had a Ctrl+C case — verify no early return exists.
	// The key name chars get inserted because there's no interception.
	inp.HandleInput(KeyCtrlC)

	// Text changed from "hello" to "helloctrl+c" — confirms no early return
	if inp.Text() == "hello" {
		t.Error("Input has an unexpected Ctrl+C early return")
	}
}

// ── Editor Ctrl+D ──

func TestEditor_CtrlD_EmptyBuffer_EOFBehavior(t *testing.T) {
	e := NewEditor()
	e.SetFocused(true)
	e.SetText("")

	e.tui = NewTUI(NewProcessTerminal())

	// Ctrl+D on empty buffer should stop the TUI
	e.HandleInput(KeyCtrlD)

	if !e.tui.stopped.Load() {
		t.Error("Ctrl+D on empty buffer should stop the TUI")
	}
}

func TestEditor_CtrlD_NonEmptyBuffer_DeletesForward(t *testing.T) {
	e := NewEditor()
	e.SetFocused(true)
	e.SetText("hello")
	e.pos = 1

	// Ctrl+D on non-empty buffer should delete forward
	e.HandleInput(KeyCtrlD)

	if e.Text() != "hllo" {
		t.Errorf("Text() = %q, want %q", e.Text(), "hllo")
	}
	if e.pos != 1 {
		t.Errorf("Cursor = %d, want 1", e.pos)
	}
}

func TestEditor_CtrlD_AtEnd_DoesNothing(t *testing.T) {
	e := NewEditor()
	e.SetFocused(true)
	e.SetText("hello")
	e.pos = 5

	e.HandleInput(KeyCtrlD)

	if e.Text() != "hello" {
		t.Errorf("Text() = %q, want %q", e.Text(), "hello")
	}
}

func TestEditor_CtrlD_MatchesKeybinding(t *testing.T) {
	e := NewEditor()

	if !e.kb.Matches(KeyCtrlD, KbDeleteForward) {
		t.Error("Ctrl+D should match KbDeleteForward keybinding")
	}
	if !e.kb.Matches(KeyDelete, KbDeleteForward) {
		t.Error("Delete should match KbDeleteForward keybinding")
	}
}

// ── TUI-level Ctrl+C with cancel callback ──

func TestTUI_HandleCtrlC_EmptyEditor_CancelsInputRequest(t *testing.T) {
	tui := NewTUI(NewProcessTerminal())
	ed := NewEditor()
	ed.SetFocused(true)
	tui.SetFocus(ed)

	called := false
	tui.OnCancelInputRequest = func() bool {
		called = true
		return true
	}

	if !tui.handleCtrlC(KeyCtrlC, tui.Focused()) {
		t.Error("handleCtrlC should consume the key")
	}
	if !called {
		t.Error("OnCancelInputRequest should be invoked when editor is empty")
	}
	if tui.stopped.Load() {
		t.Error("TUI should not stop when cancel callback handled the request")
	}
}

func TestTUI_HandleCtrlC_EmptyEditor_NilCallback(t *testing.T) {
	tui := NewTUI(NewProcessTerminal())
	ed := NewEditor()
	ed.SetFocused(true)
	tui.SetFocus(ed)

	// No cancel callback set: Ctrl+C should be consumed (and attempt Stop)
	// without panicking. We cannot assert Stop() fully without a started
	// terminal, but consuming the key and not panicking is the contract.
	if !tui.handleCtrlC(KeyCtrlC, tui.Focused()) {
		t.Error("handleCtrlC should consume the key")
	}
}
