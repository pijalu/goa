// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import "testing"

func TestAgentDrivenGate_InitialState(t *testing.T) {
	g := NewAgentDrivenGate()
	if g.Enabled() {
		t.Error("new gate should be disabled")
	}
	if g.Prompt() != "" {
		t.Error("new gate prompt should be empty")
	}
}

func TestAgentDrivenGate_SetEnabled(t *testing.T) {
	g := NewAgentDrivenGate()
	var calls []bool
	g.SetChangeCallback(func(enabled bool) { calls = append(calls, enabled) })

	g.SetEnabled(true)
	if !g.Enabled() {
		t.Error("gate should be enabled")
	}
	if len(calls) != 1 || !calls[0] {
		t.Errorf("callback = %v, want [true]", calls)
	}

	g.SetEnabled(true)
	if len(calls) != 1 {
		t.Error("callback should not fire when state unchanged")
	}

	g.SetEnabled(false)
	if g.Enabled() {
		t.Error("gate should be disabled")
	}
	if len(calls) != 2 || calls[1] {
		t.Errorf("callback = %v, want [true false]", calls)
	}
}

func TestAgentDrivenGate_Prompt(t *testing.T) {
	g := NewAgentDrivenGate()
	g.SetPrompt("be agentic")
	if g.Prompt() != "be agentic" {
		t.Errorf("prompt = %q, want be agentic", g.Prompt())
	}
}
