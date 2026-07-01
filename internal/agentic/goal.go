// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

// GoalStateProvider injects goal context into the LLM system prompt.
// Called once per turn at the continuation boundary.
type GoalStateProvider interface {
	ActiveGoalReminder() string
}
