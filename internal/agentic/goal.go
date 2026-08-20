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

// StickyProvider supplies always-on instruction blocks (sticky knowledge
// skills) that are persisted into the conversation history once per content
// change. Implementations must return byte-stable output for an unchanged
// sticky set so dedup by string compare works and provider-side prompt
// caching stays effective.
type StickyProvider interface {
	// StickyInstructions returns the current sticky instruction blocks in
	// stable order. Empty/nil means no sticky skills are active.
	StickyInstructions() []string
}

// PreTurnProvider supplies additional user-role content delivered at the
// start of every turn (user turns and goal continuation turns alike), ahead of
// the turn's own user message. It is the generic delivery hook used by the
// schedule subsystem to inject due reminders "on next turn". Implementations
// must be safe for concurrent calls and must consume/claim whatever they
// return (a second call in the same turn must return empty) so each delivery
// happens exactly once.
type PreTurnProvider interface {
	// PreTurnMessages returns user-role message texts to deliver at the start
	// of the next turn. Empty/nil means nothing is due.
	PreTurnMessages() []string
}
