// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/core/goal"
)

func TestMarker_Paused(t *testing.T) {
	status := goal.GoalPaused
	actor := goal.GoalActorUser
	reason := "break"
	m := NewMarker(&goal.GoalChange{
		Kind:   goal.GoalChangeLifecycle,
		Status: &status,
		Actor:  &actor,
		Reason: &reason,
	})

	lines := m.Render(80)
	if len(lines) == 0 {
		t.Fatal("expected marker line")
	}
	if !strings.Contains(lines[0], "paused") {
		t.Errorf("marker should contain paused: %s", lines[0])
	}
	if !strings.Contains(lines[0], "break") {
		t.Error("marker should show reason")
	}
}

func TestMarker_AllStatuses(t *testing.T) {
	for _, status := range []goal.GoalStatus{goal.GoalActive, goal.GoalPaused, goal.GoalBlocked, goal.GoalDone} {
		actor := goal.GoalActorModel
		m := NewMarker(&goal.GoalChange{Status: &status, Actor: &actor})
		lines := m.Render(80)
		if len(lines) == 0 {
			t.Errorf("%s: no lines", status)
		}
	}
}

func TestMarker_NoStatus(t *testing.T) {
	m := NewMarker(&goal.GoalChange{})
	lines := m.Render(80)
	if len(lines) == 0 {
		t.Fatal("expected lines")
	}
}

func TestMarker_SystemActor(t *testing.T) {
	status := goal.GoalBlocked
	actor := goal.GoalActorRuntime
	m := NewMarker(&goal.GoalChange{Status: &status, Actor: &actor})
	lines := m.Render(80)
	if !strings.Contains(lines[0], "system") {
		t.Errorf("line = %q", lines[0])
	}
}

func TestMarker_HandleInput(t *testing.T) {
	m := NewMarker(nil)
	m.HandleInput("x")
	m.Invalidate()
}
