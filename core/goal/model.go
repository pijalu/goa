// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package goal implements the durable single-goal engine used by Goa.
//
// It owns the lifecycle rules, budget math, and actor boundaries that the
// slash command, model tools, and goal continuation driver depend on. Each
// session keeps exactly one current goal, rebuilt from an ordered event log.
package goal

import "time"

// GoalStatus is the lifecycle state of a goal.
type GoalStatus string

const (
	GoalActive  GoalStatus = "active"
	GoalPaused  GoalStatus = "paused"
	GoalBlocked GoalStatus = "blocked"
	GoalDone    GoalStatus = "complete"
)

// GoalActor identifies who performed a goal action.
type GoalActor string

const (
	GoalActorUser    GoalActor = "user"
	GoalActorModel   GoalActor = "model"
	GoalActorRuntime GoalActor = "runtime"
	GoalActorSystem  GoalActor = "system"
)

// GoalBudgetLimits defines optional hard limits on the goal.
// All fields are optional (nil = unlimited).
type GoalBudgetLimits struct {
	TokenBudget       *int   `json:"tokenBudget,omitempty"`
	TurnBudget        *int   `json:"turnBudget,omitempty"`
	WallClockBudgetMs *int64 `json:"wallClockBudgetMs,omitempty"`
}

// GoalBudgetReport is the computed budget view with remaining counters.
type GoalBudgetReport struct {
	TokenBudget          *int   `json:"tokenBudget"`
	TurnBudget           *int   `json:"turnBudget"`
	WallClockBudgetMs    *int64 `json:"wallClockBudgetMs"`
	RemainingTokens      *int   `json:"remainingTokens"`
	RemainingTurns       *int   `json:"remainingTurns"`
	RemainingWallClockMs *int64 `json:"remainingWallClockMs"`
	OverBudget           bool   `json:"overBudget"`
}

// GoalSnapshot is the public, computed projection of internal goal state.
// WallClockMs always includes the live in-flight interval.
type GoalSnapshot struct {
	GoalID              string           `json:"goalId,omitempty"`
	Name                string           `json:"name,omitempty"` // friendly alias, e.g. "happy.fox"
	ManagedBy           string           `json:"managedBy,omitempty"` // e.g. "orchestrator" or empty
	Objective           string           `json:"objective"`
	CompletionCriterion *string          `json:"completionCriterion,omitempty"`
	Status              GoalStatus       `json:"status"`
	TurnsUsed           int              `json:"turnsUsed"`
	TokensUsed          int              `json:"tokensUsed"`
	WallClockMs         int64            `json:"wallClockMs"`
	Budget              GoalBudgetReport `json:"budget"`
	TerminalReason      *string          `json:"terminalReason,omitempty"`
}

// GoalToolResult is the wrapper returned by read operations.
type GoalToolResult struct {
	Goal *GoalSnapshot `json:"goal"`
}

// UpcomingGoal is a queued goal waiting to become active.
type UpcomingGoal struct {
	ID        string    `json:"id"`
	Name      string    `json:"name,omitempty"` // friendly alias, e.g. "happy.fox"
	ManagedBy string    `json:"managedBy,omitempty"`
	Objective string    `json:"objective"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// GoalChangeKind describes the kind of change for UI rendering.
type GoalChangeKind string

const (
	GoalChangeLifecycle  GoalChangeKind = "lifecycle"
	GoalChangeCompletion GoalChangeKind = "completion"
)

// GoalChange describes what changed on a goal.updated event.
type GoalChange struct {
	Kind   GoalChangeKind
	Status *GoalStatus
	Reason *string
	Actor  *GoalActor
	Stats  *GoalChangeStats
}

// GoalChangeStats is a counter snapshot at the moment of change.
type GoalChangeStats struct {
	TurnsUsed   int
	TokensUsed  int
	WallClockMs int64
}

// CreateGoalInput is the input for creating a goal.
type CreateGoalInput struct {
	Objective           string
	Name                string // optional friendly alias; auto-generated when empty
	ManagedBy           string // empty for normal goals; "orchestrator" for ephemeral orchestrator goals
	CompletionCriterion *string
	Replace             bool
}

// GoalReasonInput carries an optional reason string for lifecycle changes.
type GoalReasonInput struct {
	Reason *string
}

// goalStage is the mutable internal state rebuilt from event records.
type goalStage struct {
	goalID              string
	name                string
	managedBy           string
	objective           string
	completionCriterion *string
	status              GoalStatus
	turnsUsed           int
	tokensUsed          int
	wallClockMs         int64
	wallClockResumedAt  *int64
	budgetLimits        GoalBudgetLimits
	terminalReason      *string
	updatedAt           time.Time
}

// GoalEventType identifies a record in the event-sourced log.
type GoalEventType string

const (
	GoalEventCreate GoalEventType = "goal.create"
	GoalEventUpdate GoalEventType = "goal.update"
	GoalEventClear  GoalEventType = "goal.clear"
)

// GoalEventRecord is a single event in the event-sourced log.
// Only the fields relevant to the record type are populated.
type GoalEventRecord struct {
	Type      GoalEventType `json:"type"`
	Timestamp time.Time     `json:"timestamp"`

	// goal.create fields
	GoalID              *string `json:"goalId,omitempty"`
	Name                *string `json:"name,omitempty"` // friendly alias
	ManagedBy           *string `json:"managedBy,omitempty"`
	Objective           *string `json:"objective,omitempty"`
	CompletionCriterion *string `json:"completionCriterion,omitempty"`

	// goal.update fields (patches)
	Status       *string           `json:"status,omitempty"`
	Reason       *string           `json:"reason,omitempty"`
	Actor        *string           `json:"actor,omitempty"`
	TurnsUsed    *int              `json:"turnsUsed,omitempty"`
	TokensUsed   *int              `json:"tokensUsed,omitempty"`
	WallClockMs  *int64            `json:"wallClockMs,omitempty"`
	BudgetLimits *GoalBudgetLimits `json:"budgetLimits,omitempty"`
}
