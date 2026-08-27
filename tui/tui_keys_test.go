// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import "testing"

// TestAppShortcut_AltThinkingLevel_MacOptionAlias is the regression test for
// the missing altAlias on the thinking-level shortcut. On macOS with the
// default keyboard layout, Option+t delivers the literal character '†'
// (U+2020) rather than the ESC+t sequence. resolveAppShortcut maps '†' to
// "alt+t" via optionKeyAliases and matches it against the shortcut's altAlias
// — but the alt+t entry was the ONLY built-in alt shortcut without an
// altAlias, so the alias path never matched and Option+t silently did nothing
// on Mac. Every built-in alt shortcut must carry its altAlias.
func TestAppShortcut_AltThinkingLevel_MacOptionAlias(t *testing.T) {
	engine := NewTUI(nil)
	var fired bool
	engine.OnCycleThinkingLevel = func() { fired = true }

	// "†" is what macOS Option+t produces with the default keyboard layout.
	fn, ok := engine.resolveAppShortcut("†")
	if !ok {
		t.Fatal("Option+t ('†') did not resolve to any app shortcut on macOS")
	}
	fn()
	if !fired {
		t.Fatal("Option+t ('†') resolved but did not invoke OnCycleThinkingLevel")
	}
}

// TestAppShortcut_AltAliases_AllBuiltins pins the invariant that every
// built-in alt+<letter> shortcut resolves via BOTH its ESC-prefixed form and
// its macOS Option-character alias, so no Option-key binding is silently dead
// on macOS. Each entry drives its own host callback; we only assert that
// resolveAppShortcut RESOLVES (returns a non-nil callback) for both forms.
func TestAppShortcut_AltAliases_AllBuiltins(t *testing.T) {
	// aliasChar is the macOS Option-key character; escForm is ESC+<base>.
	cases := []struct {
		name      string
		aliasChar string // macOS Option+key literal (optionKeyAliases key)
		escForm   string // "alt+<base>" produced by ESC+key
	}{
		{"edit-steering", "ê", "alt+e"},
		{"change-mode", "µ", "alt+m"},
		{"open-mode-selector", "ø", "alt+o"},
		{"cycle-thinking", "†", "alt+t"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			engine := NewTUI(nil)
			if _, ok := engine.resolveAppShortcut(tc.escForm); !ok {
				t.Errorf("ESC form %q did not resolve to a shortcut", tc.escForm)
			}
			if _, ok := engine.resolveAppShortcut(tc.aliasChar); !ok {
				t.Errorf("macOS Option char %q did not resolve to a shortcut (missing altAlias)", tc.aliasChar)
			}
		})
	}
}

// TestAppShortcut_AgentTabCycle_MacOptionAliases verifies the multi-agent tab
// Alt+[/Alt+] aliases resolve via the macOS Option characters '“' (Option+[)
// and '‘' (Option+]) — the default-layout literal chars — not only the
// ESC-prefixed alt+[ / alt+] forms. Shift+Tab remains the primary prev-tab
// binding on every keyboard.
func TestAppShortcut_AgentTabCycle_MacOptionAliases(t *testing.T) {
	engine := NewTUI(nil)
	var next, prev bool
	engine.OnAgentTabNext = func() { next = true }
	engine.OnAgentTabPrev = func() { prev = true }

	// ESC-prefixed forms.
	if fn, ok := engine.resolveAppShortcut("alt+]"); !ok {
		t.Error("alt+] did not resolve")
	} else {
		fn()
	}
	if fn, ok := engine.resolveAppShortcut("alt+["); !ok {
		t.Error("alt+[ did not resolve")
	} else {
		fn()
	}
	if !next || !prev {
		t.Fatalf("ESC forms: next=%v prev=%v, want both true", next, prev)
	}

	// macOS Option-char forms.
	next, prev = false, false
	if fn, ok := engine.resolveAppShortcut("‘"); !ok { // Option+]
		t.Error("Option+] ('‘') did not resolve")
	} else {
		fn()
	}
	if fn, ok := engine.resolveAppShortcut("“"); !ok { // Option+[
		t.Error("Option+[ ('“') did not resolve")
	} else {
		fn()
	}
	if !next || !prev {
		t.Fatalf("Mac Option chars: next=%v prev=%v, want both true", next, prev)
	}
}

// TestAppShortcut_AgentTabJump_MacOptionAlias verifies Option+<digit> resolves
// to the tab jump via the macOS Option character (e.g. Option+2 = '™').
func TestAppShortcut_AgentTabJump_MacOptionAlias(t *testing.T) {
	engine := NewTUI(nil)
	got := -1
	engine.OnAgentTabDigit = func(i int) { got = i }

	// ESC-prefixed alt+2 -> index 1.
	if fn, ok := engine.resolveAppShortcut("alt+2"); !ok {
		t.Fatal("alt+2 did not resolve")
	} else {
		fn()
	}
	if got != 1 {
		t.Fatalf("alt+2 jumped to %d, want 1", got)
	}

	// macOS Option+2 delivers '™' -> alias alt+2 -> index 1.
	got = -1
	if fn, ok := engine.resolveAppShortcut("™"); !ok {
		t.Fatal("Option+2 ('™') did not resolve")
	} else {
		fn()
	}
	if got != 1 {
		t.Fatalf("Option+2 ('™') jumped to %d, want 1", got)
	}
}
