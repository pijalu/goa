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

func TestBubble_CapsAtThreeLinesWithEllipsis(t *testing.T) {
	b := NewBubble()
	// An objective long enough to wrap well beyond 3 lines at width 40.
	b.SetSnapshot(&goal.GoalSnapshot{
		Status:    goal.GoalActive,
		Name:      "big.task",
		Objective: "refactor the entire renderer pipeline to stream partial frames and repaint only dirty rows while preserving scrollback semantics across resizes",
	})
	lines := b.Render(40)
	// Layout: separator + body + separator → body must be exactly 3 lines.
	if len(lines) != 5 {
		t.Fatalf("expected 5 lines (sep + 3 body + sep), got %d: %v", len(lines), lines)
	}
	third := ansi.Strip(lines[3])
	if !strings.HasSuffix(strings.TrimSpace(third), "...") {
		t.Errorf("expected third body line to end with ellipsis, got %q", third)
	}
	if got := ansi.Width(lines[3]); got > 40 {
		t.Errorf("third body line exceeds width: %d > 40", got)
	}
}

func TestBubble_UnderThreeLinesNoEllipsis(t *testing.T) {
	b := NewBubble()
	b.SetSnapshot(&goal.GoalSnapshot{
		Status:    goal.GoalActive,
		Objective: "short task",
	})
	lines := b.Render(80)
	for i, l := range lines {
		if strings.Contains(l, "...") {
			t.Errorf("line %d unexpectedly contains ellipsis: %q", i, ansi.Strip(l))
		}
	}
}
