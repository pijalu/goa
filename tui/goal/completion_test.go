// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"testing"

	"github.com/pijalu/goa/core/goal"
)

func TestCompletion_Render(t *testing.T) {
	snap := &goal.GoalSnapshot{
		Objective:   "fix tests",
		TurnsUsed:   5,
		TokensUsed:  1000,
		WallClockMs: 60000,
	}
	c := NewCompletion(snap)
	lines := c.Render(80)
	if len(lines) != 2 {
		t.Fatalf("lines = %d", len(lines))
	}
	for _, want := range []string{"fix tests", "5 turns", "1.0k tokens"} {
		if !contains(lines[0]+lines[1], want) && !contains(lines[1], want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestCompletion_RenderWide(t *testing.T) {
	snap := &goal.GoalSnapshot{Objective: "x"}
	c := NewCompletion(snap)
	if c.Render(0) == nil {
		t.Error("expected non-nil render")
	}
}

func TestCompletion_NoOps(t *testing.T) {
	c := NewCompletion(nil)
	if c.Render(80) != nil {
		t.Error("expected nil render")
	}
	c.HandleInput("x")
	c.Invalidate()
}
