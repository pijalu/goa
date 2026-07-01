// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/internal/ansi"
)

func TestBubble_HiddenWhenNoGoal(t *testing.T) {
	b := NewBubble()
	if lines := b.Render(80); lines != nil {
		t.Errorf("expected nil render with no goal, got %v", lines)
	}
}

func TestBubble_HiddenWhenGoalNotActive(t *testing.T) {
	b := NewBubble()
	b.SetSnapshot(&goal.GoalSnapshot{Status: goal.GoalPaused, Objective: "do something"})
	if lines := b.Render(80); lines != nil {
		t.Errorf("expected nil render for paused goal, got %v", lines)
	}
}

func TestBubble_ShowsActiveGoal(t *testing.T) {
	b := NewBubble()
	b.SetSnapshot(&goal.GoalSnapshot{
		Status:    goal.GoalActive,
		Name:      "indigo.elk",
		Objective: "Create a html page that renders a fire",
	})
	lines := b.Render(80)
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 lines, got %d: %v", len(lines), lines)
	}
	content := strings.Join(lines, "\n")
	if !strings.Contains(content, "⟐") {
		t.Error("expected bubble to contain ⟐ marker")
	}
	if !strings.Contains(content, "[indigo.elk]") {
		t.Error("expected bubble to contain goal name")
	}
	if !strings.Contains(content, "renders a fire") {
		t.Error("expected bubble to contain objective")
	}
}

func TestBubble_CollapseToggle(t *testing.T) {
	b := NewBubble()
	b.SetSnapshot(&goal.GoalSnapshot{
		Status:    goal.GoalActive,
		Name:      "indigo.elk",
		Objective: "Create a html page that renders a fire",
	})
	b.Render(80)
	b.HandleInput("ctrl+g")
	if !b.Collapsed() {
		t.Error("expected bubble to be collapsed after ctrl+g")
	}
	collapsed := b.Render(80)
	if !strings.Contains(strings.Join(collapsed, ""), "indigo.elk") {
		t.Error("expected collapsed bubble to still show goal name")
	}
}

func TestBubble_SeparatorColor(t *testing.T) {
	b := NewBubble()
	b.SetSnapshot(&goal.GoalSnapshot{
		Status:    goal.GoalActive,
		Objective: "render a fire",
	})
	b.SetSeparatorColor("#ff0000")
	lines := b.Render(80)
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 lines, got %d", len(lines))
	}
	want := ansi.Fg("#ff0000")
	if !strings.Contains(lines[0], want) {
		t.Errorf("top separator %q missing expected color", lines[0])
	}
	if !strings.Contains(lines[len(lines)-1], want) {
		t.Errorf("bottom separator %q missing expected color", lines[len(lines)-1])
	}
}
