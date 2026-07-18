// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import "testing"

func TestTUI_PluginHotkeyResolves(t *testing.T) {
	engine := NewTUI(nil)
	var fired bool
	engine.RegisterPluginHotkey("ctrl+shift+q", func() { fired = true })

	fn, ok := engine.resolveAppShortcut("ctrl+shift+q")
	if !ok {
		t.Fatal("plugin hotkey did not resolve")
	}
	fn()
	if !fired {
		t.Fatal("handler not invoked")
	}
}

func TestTUI_PluginHotkeyReRegistrationReplaces(t *testing.T) {
	engine := NewTUI(nil)
	var first, second bool
	engine.RegisterPluginHotkey("ctrl+shift+q", func() { first = true })
	engine.RegisterPluginHotkey("ctrl+shift+q", func() { second = true })

	fn, _ := engine.resolveAppShortcut("ctrl+shift+q")
	fn()
	if first {
		t.Fatal("old handler should have been replaced")
	}
	if !second {
		t.Fatal("new handler not invoked")
	}
	if len(engine.pluginHotkeys) != 1 {
		t.Fatalf("pluginHotkeys len = %d, want 1", len(engine.pluginHotkeys))
	}
}

func TestTUI_PluginHotkeyDoesNotOverrideBuiltin(t *testing.T) {
	engine := NewTUI(nil)
	var pluginFired bool
	// ctrl+shift+m is the built-in autonomy cycle.
	engine.RegisterPluginHotkey("ctrl+shift+m", func() { pluginFired = true })

	fn, ok := engine.resolveAppShortcut("ctrl+shift+m")
	if !ok {
		t.Fatal("ctrl+shift+m should resolve")
	}
	// The resolved callback is the built-in (OnCycleAutonomy), which is nil
	// here; invoking it must not call the plugin handler.
	if fn != nil {
		fn()
	}
	if pluginFired {
		t.Fatal("plugin hotkey should not shadow a built-in")
	}
}

func TestTUI_PluginHotkeyUnknownKeyNoMatch(t *testing.T) {
	engine := NewTUI(nil)
	engine.RegisterPluginHotkey("ctrl+shift+q", func() {})
	if _, ok := engine.resolvePluginHotkey("ctrl+shift+z"); ok {
		t.Fatal("unrelated key matched")
	}
}
