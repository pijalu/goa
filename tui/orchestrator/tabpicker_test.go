// SPDX-License-Identifier: GPL-3.0-or-later

package orchestrator

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/tui"
)

// buildPickerView creates a view with two agents for picker tests.
func buildPickerView(t *testing.T) *MultiAgentView {
	t.Helper()
	v := NewMultiAgentView("orchestration")
	v.ApplyEvent(AgentViewEvent{Kind: EvSourceStarted})
	v.ApplyEvent(AgentViewEvent{Kind: EvAgentStarted, AgentID: "c-1", Role: "coder"})
	v.ApplyEvent(AgentViewEvent{Kind: EvAgentStarted, AgentID: "r-1", Role: "reviewer"})
	return v
}

func TestSteerTargetPicker_DigitJumpPicksAndCloses(t *testing.T) {
	v := buildPickerView(t)
	var picked string
	var closed bool
	p := NewSteerTargetPicker(v)
	p.SetPickFunc(func(target string) { picked = target })
	p.SetCloseFunc(func() { closed = true })

	p.HandleInput("2") // coder (index 1)
	if picked != "c-1" {
		t.Errorf("digit 2 picked %q, want c-1", picked)
	}
	if !closed {
		t.Error("picker did not close after pick")
	}
}

func TestSteerTargetPicker_DigitOnePicksAll(t *testing.T) {
	v := buildPickerView(t)
	var picked string
	p := NewSteerTargetPicker(v)
	p.SetPickFunc(func(target string) { picked = target })
	p.SetCloseFunc(func() {})

	p.HandleInput("1") // all (index 0)
	if picked != "all" {
		t.Errorf("digit 1 picked %q, want all", picked)
	}
}

func TestSteerTargetPicker_OutOfRangeDigitIgnored(t *testing.T) {
	v := buildPickerView(t)
	p := NewSteerTargetPicker(v)
	called := false
	p.SetPickFunc(func(string) { called = true })
	p.SetCloseFunc(func() {})

	p.HandleInput("9") // only 3 targets
	if called {
		t.Error("digit 9 should not pick when only 3 targets exist")
	}
}

func TestSteerTargetPicker_ArrowThenEnter(t *testing.T) {
	v := buildPickerView(t)
	var picked string
	p := NewSteerTargetPicker(v)
	p.SetPickFunc(func(target string) { picked = target })
	p.SetCloseFunc(func() {})

	p.HandleInput(tui.KeyDown) // all -> coder
	p.HandleInput(tui.KeyEnter)
	if picked != "c-1" {
		t.Errorf("arrow+enter picked %q, want c-1", picked)
	}
}

func TestSteerTargetPicker_EscapeCancels(t *testing.T) {
	v := buildPickerView(t)
	var picked string
	closed := false
	p := NewSteerTargetPicker(v)
	p.SetPickFunc(func(target string) { picked = target })
	p.SetCloseFunc(func() { closed = true })

	p.HandleInput(tui.KeyEscape)
	if picked != "" {
		t.Errorf("escape picked %q, want empty (cancel)", picked)
	}
	if !closed {
		t.Error("picker did not close on escape")
	}
}

func TestSteerTargetPicker_RendersNumberedList(t *testing.T) {
	v := buildPickerView(t)
	p := NewSteerTargetPicker(v)
	p.SetPickFunc(func(string) {})
	p.SetCloseFunc(func() {})

	joined := strings.Join(stripAll(p.Render(60)), "\n")
	for _, want := range []string{"1", "2", "3", "all", "coder", "reviewer", "jump"} {
		if !strings.Contains(joined, want) {
			t.Errorf("picker render missing %q:\n%s", want, joined)
		}
	}
}

func TestSteerTargetPicker_HighlightsActiveTarget(t *testing.T) {
	v := buildPickerView(t)
	v.SetSteerTarget("c-1")
	p := NewSteerTargetPicker(v)
	p.SetPickFunc(func(string) {})
	p.SetCloseFunc(func() {})

	joined := strings.Join(p.Render(60), "\n")
	if !strings.Contains(joined, "●") {
		t.Error("picker should highlight the active target")
	}
}
