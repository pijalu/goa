// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"testing"
)

// TestApplyCommand_RecoversFromPanic verifies that a panic inside a command
// submitted to the commandLoop is recovered and does not propagate.
func TestApplyCommand_RecoversFromPanic(t *testing.T) {
	term := &fakeTerminal{w: 80, h: 24}
	tui := NewTUI(term)
	tui.RunLoops()
	defer tui.Stop()

	var ran bool
	tui.Apply(func() {
		panic("intentional command panic")
	})
	tui.Apply(func() {
		ran = true
	})

	tui.ApplySync(func() {}) // flush pending commands
	if !ran {
		t.Fatal("commandLoop stopped processing after a panic")
	}
}
