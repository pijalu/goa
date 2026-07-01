// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/core/swarm"
)

// GoalInjector implements agentic.GoalStateProvider.
type GoalInjector struct {
	Mode *goal.GoalMode
}

// ActiveGoalReminder returns the appropriate goal context text for the
// current goal status, or an empty string when there is no goal.
func (g *GoalInjector) ActiveGoalReminder() string {
	result := g.Mode.GetGoal()
	if result.Goal == nil {
		return ""
	}
	switch result.Goal.Status {
	case goal.GoalActive:
		return goal.BuildActiveGoalReminder(*result.Goal)
	case goal.GoalBlocked:
		return goal.BuildBlockedNote(*result.Goal)
	case goal.GoalPaused:
		return goal.BuildPausedNote(*result.Goal)
	default:
		return ""
	}
}

// ReminderProvider chains multiple goal-state-style reminder sources into a
// single agentic.GoalStateProvider. Each turn the agent prepends the joined
// reminder text to the system prompt (see Agent.buildProviderContext). It is
// the extension point used to inject the swarm-mode enter reminder while
// swarm mode is active under a manual or task trigger.
type ReminderProvider struct {
	Sources []GoalReminderSource
}

// GoalReminderSource is anything that can contribute a per-turn reminder
// string in the same shape as agentic.GoalStateProvider.
type GoalReminderSource interface {
	ActiveGoalReminder() string
}

// ActiveGoalReminder joins every non-empty source reminder with a blank line.
func (r *ReminderProvider) ActiveGoalReminder() string {
	var parts []string
	for _, s := range r.Sources {
		if s == nil {
			continue
		}
		if text := s.ActiveGoalReminder(); text != "" {
			parts = append(parts, text)
		}
	}
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "\n\n"
		}
		out += p
	}
	return out
}

// SwarmReminder exposes the swarm enter reminder as a goal-state-style
// provider so ReminderProvider can chain it. The reminder is emitted only
// while swarm mode is active under a manual or task trigger (the tool trigger
// omits it, matching kimi-code).
type SwarmReminder struct {
	State *swarm.State
}

// ActiveGoalReminder returns the swarm enter reminder, or "" when inactive.
func (s SwarmReminder) ActiveGoalReminder() string {
	if s.State == nil {
		return ""
	}
	if !s.State.IsActive() {
		return ""
	}
	if t := s.State.Trigger(); t == swarm.ToolTrigger {
		return ""
	}
	return swarm.EnterReminder()
}
