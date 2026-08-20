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

// TestCompletion_MultiLineObjective pins the "Goal completion screen
// corruption" fix: unblocking-investigation objectives contain embedded
// newlines ("...\n\nThe goal \"X\" was blocked because: ..."). Every rendered
// row must be a single visual line — no embedded "\n" (raw-mode LF prints the
// continuation at the column where the previous line ended, producing the
// misaligned "The goal ..." fragment at column ~80).
func TestCompletion_MultiLineObjective(t *testing.T) {
	snap := &goal.GoalSnapshot{
		Objective: "UNBLOCKING INVESTIGATION — find a solution for a blocked goal.\n\nThe goal \"Implement G05: ALTER TABLE Token-Level Rename\" was blocked because: schema entries lost\nIt was waiting for: user direction",
		TurnsUsed: 1, TokensUsed: 24900, WallClockMs: 154000,
	}
	c := NewCompletion(snap)
	lines := c.Render(80)
	if len(lines) < 4 {
		t.Fatalf("expected wrapped multi-row render, got %d rows: %q", len(lines), lines)
	}
	for i, l := range lines {
		if strings.Contains(l, "\n") {
			t.Errorf("row %d contains an embedded newline: %q", i, l)
		}
		if w := ansi.Width(l); w > 80 {
			t.Errorf("row %d exceeds width: %d > 80 (%q)", i, w, l)
		}
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"✓ Goal complete", "UNBLOCKING INVESTIGATION", "The goal \"Implement G05", "was blocked because", "It was waiting for"} {
		if !strings.Contains(joined, want) {
			t.Errorf("render missing %q", want)
		}
	}
}
