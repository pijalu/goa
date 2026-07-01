// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"testing"
)

func TestDefaultKeybindings_HasAppShortcuts(t *testing.T) {
	defs := DefaultKeybindings()
	for _, name := range []string{
		KbCycleThinkingLevel,
		KbChangeMode,
		KbOpenModeSelector,
		KbCycleAutonomy,
		KbChangeModel,
		KbToggleThinkingBlocks,
	} {
		if _, ok := defs[name]; !ok {
			t.Errorf("missing keybinding %q", name)
		}
	}
}

func TestTUI_HandleAppShortcuts_CycleThinkingLevel(t *testing.T) {
	tui := NewTUI(NewProcessTerminal())
	called := false
	tui.OnCycleThinkingLevel = func() { called = true }
	if !tui.handleAppShortcuts("shift+tab") {
		t.Error("shift+tab should be consumed")
	}
	if !called {
		t.Error("OnCycleThinkingLevel was not called")
	}
}

func TestTUI_HandleAppShortcuts_ChangeMode(t *testing.T) {
	tui := NewTUI(NewProcessTerminal())
	called := false
	tui.OnChangeMode = func() { called = true }
	if !tui.handleAppShortcuts("alt+m") {
		t.Error("alt+m should be consumed")
	}
	if !called {
		t.Error("OnChangeMode was not called")
	}
}

func TestTUI_HandleAppShortcuts_ChangeModeUppercase(t *testing.T) {
	tui := NewTUI(NewProcessTerminal())
	called := false
	tui.OnChangeMode = func() { called = true }
	if !tui.handleAppShortcuts("alt+M") {
		t.Error("alt+M should be consumed")
	}
	if !called {
		t.Error("OnChangeMode was not called for alt+M")
	}
}

func TestTUI_HandleAppShortcuts_OpenModeSelector(t *testing.T) {
	tui := NewTUI(NewProcessTerminal())
	called := false
	tui.OnOpenModeSelector = func() { called = true }
	if !tui.handleAppShortcuts("alt+o") {
		t.Error("alt+o should be consumed")
	}
	if !called {
		t.Error("OnOpenModeSelector was not called")
	}
}

func TestTUI_HandleAppShortcuts_CycleAutonomy(t *testing.T) {
	tui := NewTUI(NewProcessTerminal())
	called := false
	tui.OnCycleAutonomy = func() { called = true }
	if !tui.handleAppShortcuts("ctrl+shift+m") {
		t.Error("ctrl+shift+m should be consumed")
	}
	if !called {
		t.Error("OnCycleAutonomy was not called")
	}
}

func TestTUI_HandleAppShortcuts_ChangeModel(t *testing.T) {
	tui := NewTUI(NewProcessTerminal())
	called := false
	tui.OnChangeModel = func() { called = true }
	if !tui.handleAppShortcuts("ctrl+l") {
		t.Error("ctrl+l should be consumed")
	}
	if !called {
		t.Error("OnChangeModel was not called")
	}
}

func TestTUI_HandleAppShortcuts_ToggleThinkingBlocks(t *testing.T) {
	tui := NewTUI(NewProcessTerminal())
	called := false
	tui.OnToggleThinkingBlocks = func() { called = true }
	if !tui.handleAppShortcuts("ctrl+t") {
		t.Error("ctrl+t should be consumed")
	}
	if !called {
		t.Error("OnToggleThinkingBlocks was not called")
	}
}

func TestTUI_HandleAppShortcuts_OptionKeyAliases(t *testing.T) {
	tui := NewTUI(NewProcessTerminal())

	t.Run("Option+m maps to alt+m", func(t *testing.T) {
		called := false
		tui.OnChangeMode = func() { called = true }
		if !tui.handleAppShortcuts("µ") {
			t.Error("µ should be consumed as alt+m alias")
		}
		if !called {
			t.Error("OnChangeMode was not called for µ")
		}
	})
}
