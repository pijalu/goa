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

// Render returns the completion lines. The objective is model-authored and
// may span several paragraphs (e.g. unblocking-investigation goals); it is
// wrapped per paragraph so every returned row is a single visual line —
// an embedded "\n" would print the continuation at the column where the
// previous line ended (bugs.md "Goal completion screen corruption").
func (c *CompletionComponent) Render(width int) []string {
	if c.snapshot == nil {
		return nil
	}
	if width < 10 {
		width = 10
	}
	// The headline prefix is part of the wrapped text so the first row also
	// fits width.
	rows := wrapParagraphs("✓ Goal complete — "+c.snapshot.Objective, width)
	style := ansi.Fg(ansiColorSuccess) + ansi.Bold
	lines := make([]string, 0, len(rows)+1)
	for i, row := range rows {
		if i == len(rows)-1 {
			row += "."
		}
		lines = append(lines, padToWidth(style+row+ansi.BoldReset+ansi.Reset, width))
	}
	stats := fmt.Sprintf("Worked %s over %s, using %s tokens.",
		goal.Pluralize(c.snapshot.TurnsUsed, "turn", "turns"),
		goal.FormatElapsed(c.snapshot.WallClockMs),
		goal.FormatTokens(c.snapshot.TokensUsed))
	lines = append(lines, padToWidth(ansi.Faint+stats+ansi.Reset, width))
	return lines
}

// HandleInput is a no-op.
func (c *CompletionComponent) HandleInput(string) {}

// Invalidate is a no-op.
func (c *CompletionComponent) Invalidate() {}
