// SPDX-License-Identifier-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/tui"
)

// buildPickerView creates a view with 2 tabs (Stats, Conversation) for picker
// tests, with the Stats tab active initially.
func buildPickerView(t *testing.T) *MultiAgentView {
	t.Helper()
	v := NewMultiAgentView("orchestration")
	v.ApplyEvent(AgentViewEvent{Kind: EvSourceStarted})
	v.ApplyEvent(AgentViewEvent{Kind: EvAgentStarted, AgentID: "c-1", Role: "coder"})
	v.ApplyEvent(AgentViewEvent{Kind: EvAgentStarted, AgentID: "r-1", Role: "reviewer"})
	return v
}

func TestAgentTabPicker_DigitJumpPicksAndCloses(t *testing.T) {
	v := buildPickerView(t)
	var picked string
	var closed bool
	p := NewAgentTabPicker(v)
	p.SetPickFunc(func(key string) { picked = key })
	p.SetCloseFunc(func() { closed = true })

	p.HandleInput("2") // conversation (index 1)
	if picked != "conversation" {
		t.Errorf("digit 2 picked %q, want conversation", picked)
	}
	if !closed {
		t.Error("picker did not close after pick")
	}
}

func TestAgentTabPicker_DigitOnePicksStats(t *testing.T) {
	v := buildPickerView(t)
	var picked string
	p := NewAgentTabPicker(v)
	p.SetPickFunc(func(key string) { picked = key })
	p.SetCloseFunc(func() {})

	p.HandleInput("1") // stats (index 0)
	if picked != "stats" {
		t.Errorf("digit 1 picked %q, want stats", picked)
	}
}

func TestAgentTabPicker_OutOfRangeDigitIgnored(t *testing.T) {
	v := buildPickerView(t)
	p := NewAgentTabPicker(v)
	called := false
	p.SetPickFunc(func(string) { called = true })
	p.SetCloseFunc(func() {})

	p.HandleInput("9") // only 2 tabs
	if called {
		t.Error("digit 9 should not pick when only 2 tabs exist")
	}
}

func TestAgentTabPicker_ArrowThenEnter(t *testing.T) {
	v := buildPickerView(t)
	var picked string
	p := NewAgentTabPicker(v)
	p.SetPickFunc(func(key string) { picked = key })
	p.SetCloseFunc(func() {})

	p.HandleInput(tui.KeyDown) // Stats -> Conversation
	p.HandleInput(tui.KeyEnter)
	if picked != "conversation" {
		t.Errorf("arrow+enter picked %q, want conversation", picked)
	}
}

func TestAgentTabPicker_EscapeCancels(t *testing.T) {
	v := buildPickerView(t)
	var picked string
	closed := false
	p := NewAgentTabPicker(v)
	p.SetPickFunc(func(key string) { picked = key })
	p.SetCloseFunc(func() { closed = true })

	p.HandleInput(tui.KeyEscape)
	if picked != "" {
		t.Errorf("escape picked %q, want empty (cancel)", picked)
	}
	if !closed {
		t.Error("picker did not close on escape")
	}
}

func TestAgentTabPicker_RendersNumberedList(t *testing.T) {
	v := buildPickerView(t)
	p := NewAgentTabPicker(v)
	p.SetPickFunc(func(string) {})
	p.SetCloseFunc(func() {})

	joined := strings.Join(stripAll(p.Render(60)), "\n")
	for _, want := range []string{"1", "2", "Conversation", "Stats", "jump"} {
		if !strings.Contains(joined, want) {
			t.Errorf("picker render missing %q:\n%s", want, joined)
		}
	}
}
