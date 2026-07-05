// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

// GoalStateProvider injects goal context into the LLM system prompt.
// Called once per turn at the continuation boundary.
type GoalStateProvider interface {
	// ActiveGoalReminder returns the static goal reminder that should be
	// prepended to the system prompt. It must be byte-stable across turns
	// for an unchanged goal so provider-side prompt caching remains effective.
	ActiveGoalReminder() string
	// ActiveGoalProgress returns the dynamic per-turn progress text. It is
	// delivered as a user message so it does not bust the cached system prompt.
	ActiveGoalProgress() string
}
