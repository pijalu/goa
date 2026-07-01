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

// Render returns the marker line.
func (m *MarkerComponent) Render(width int) []string {
	if m.change == nil {
		return nil
	}
	return []string{padToWidth(m.headline(), width)}
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
	if m.change.Reason != nil && *m.change.Reason != "" {
		line += fmt.Sprintf("            (ctrl+o: %s)", *m.change.Reason)
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
