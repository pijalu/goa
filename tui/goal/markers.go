// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"fmt"

	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/internal/ansi"
)

// MarkerComponent renders a low-profile lifecycle marker inline in the chat.
type MarkerComponent struct {
	change *goal.GoalChange
}

// NewMarker creates a lifecycle marker component.
func NewMarker(change *goal.GoalChange) *MarkerComponent {
	return &MarkerComponent{change: change}
}

// Render returns the marker lines: a compact headline (status + actor), then
// the reason and expectation each word-wrapped onto their own indented lines
// so a long explanation is never truncated to fit one line.
func (m *MarkerComponent) Render(width int) []string {
	if m.change == nil {
		return nil
	}
	lines := []string{padToWidth(m.headline(), width)}
	lines = append(lines, m.detailLines(width)...)
	return lines
}

// detailLines renders the change's reason and expectation as indented,
// word-wrapped lines. A marker with neither yields no extra lines.
func (m *MarkerComponent) detailLines(width int) []string {
	var out []string
	if m.change.Reason != nil && *m.change.Reason != "" {
		out = append(out, wrapDetail(*m.change.Reason, width)...)
	}
	if m.change.Expectation != nil && *m.change.Expectation != "" {
		out = append(out, wrapDetail("needs: "+*m.change.Expectation, width)...)
	}
	return out
}

// wrapDetail wraps one detail string to width, indenting continuation so the
// block reads as belonging to the headline above. Detail text is dimmed.
func wrapDetail(text string, width int) []string {
	const indent = "  "
	inner := width - len(indent)
	if inner < 1 {
		inner = 1
	}
	// Reasons/expectations are model-authored and may contain newlines;
	// ansi.Wrap is single-paragraph only, so split paragraphs first
	// "Corruption on goal change").
	var out []string
	for _, wl := range wrapParagraphs(text, inner) {
		out = append(out, indent+ansi.Faint+wl+ansi.Reset)
	}
	return out
}

// HandleInput is a no-op.
func (m *MarkerComponent) HandleInput(string) {}

// Invalidate is a no-op.
func (m *MarkerComponent) Invalidate() {}

func (m *MarkerComponent) headline() string {
	status := ""
	if m.change.Status != nil {
		status = string(*m.change.Status)
	}
	actor := ""
	if m.change.Actor != nil {
		switch *m.change.Actor {
		case goal.GoalActorUser:
			actor = "by the user"
		case goal.GoalActorModel:
			actor = "by the agent"
		case goal.GoalActorRuntime, goal.GoalActorSystem:
			actor = "by the system"
		}
	}
	line := fmt.Sprintf("◦ Goal %s", status)
	if actor != "" {
		line += " " + actor
	}
	return m.color() + ansi.Bold + line + ansi.BoldReset + ansi.Reset
}

func (m *MarkerComponent) color() string {
	switch {
	case m.change.Status == nil:
		return ansi.Fg(ansiColorDim)
	case *m.change.Status == goal.GoalPaused:
		return ansi.Fg(ansiColorWarning)
	case *m.change.Status == goal.GoalActive:
		return ansi.Fg(ansiColorPrimary)
	case *m.change.Status == goal.GoalBlocked:
		return ansi.Fg(ansiColorWarning)
	default:
		return ansi.Fg(ansiColorDim)
	}
}
