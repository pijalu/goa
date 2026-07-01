// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"fmt"

	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/internal/ansi"
)

// CompletionComponent renders a goal completion message inline.
type CompletionComponent struct {
	snapshot *goal.GoalSnapshot
}

// NewCompletion creates a completion component.
func NewCompletion(snapshot *goal.GoalSnapshot) *CompletionComponent {
	return &CompletionComponent{snapshot: snapshot}
}

// Render returns the completion lines.
func (c *CompletionComponent) Render(width int) []string {
	if c.snapshot == nil {
		return nil
	}
	text := fmt.Sprintf("✓ Goal complete — %s.", c.snapshot.Objective)
	stats := fmt.Sprintf("Worked %s over %s, using %s tokens.",
		goal.Pluralize(c.snapshot.TurnsUsed, "turn", "turns"),
		goal.FormatElapsed(c.snapshot.WallClockMs),
		goal.FormatTokens(c.snapshot.TokensUsed))
	return []string{
		padToWidth(ansi.Fg(ansiColorSuccess)+ansi.Bold+text+ansi.BoldReset+ansi.Reset, width),
		padToWidth(ansi.Faint+stats+ansi.Reset, width),
	}
}

// HandleInput is a no-op.
func (c *CompletionComponent) HandleInput(string) {}

// Invalidate is a no-op.
func (c *CompletionComponent) Invalidate() {}
