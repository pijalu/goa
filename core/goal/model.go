// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package goal implements the durable single-goal engine used by Goa.
//
// It owns the lifecycle rules, budget math, and actor boundaries that the
// slash command, model tools, and goal continuation driver depend on. Each
// session keeps exactly one current goal, rebuilt from an ordered event log.
package goal

import (
	"encoding/json"
	"time"
)

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
	GoalID              string  `json:"goalId,omitempty"`
	Name                string  `json:"name,omitempty"`      // friendly alias, e.g. "happy.fox"
	ManagedBy           string  `json:"managedBy,omitempty"` // e.g. "orchestrator" or empty
	Objective           string  `json:"objective"`
	CompletionCriterion *string `json:"completionCriterion,omitempty"`
	// VerifyCommand is an optional machine-checkable done-condition: when
	// set, the done-gate executes it (exit 0 = verified) after the model
	// confirms completion, instead of trusting the evidence prose alone.
	VerifyCommand *string `json:"verifyCommand,omitempty"`
	FreshContext  bool    `json:"freshContext,omitempty"`
	// Handover carries a continuity note from a predecessor goal (its
	// completion evidence, or explicit text the creator attached) into this
	// goal's reminder. Nil for stand-alone goals. Untrusted data, never
	// instructions — the model sees it inside an <untrusted_handover> block.
	Handoff        *string          `json:"handover,omitempty"`
	Todos          []GoalTodoItem   `json:"todos,omitempty"`
	Status         GoalStatus       `json:"status"`
	TurnsUsed      int              `json:"turnsUsed"`
	TokensUsed     int              `json:"tokensUsed"`
	WallClockMs    int64            `json:"wallClockMs"`
	Budget         GoalBudgetReport `json:"budget"`
	TerminalReason *string          `json:"terminalReason,omitempty"`
	// TerminalExpectation records what a blocked goal needs in order to
	// resume (model-provided). Nil for active/completed goals.
	TerminalExpectation *string `json:"terminalExpectation,omitempty"`
}

// GoalToolResult is the wrapper returned by read operations.
type GoalToolResult struct {
	Goal *GoalSnapshot `json:"goal"`
}

// UpcomingGoal is a queued goal waiting to become active.
type UpcomingGoal struct {
	ID                  string  `json:"id"`
	Name                string  `json:"name,omitempty"` // friendly alias, e.g. "happy.fox"
	ManagedBy           string  `json:"managedBy,omitempty"`
	Objective           string  `json:"objective"`
	CompletionCriterion *string `json:"completionCriterion,omitempty"` // carried into the goal on promotion
	VerifyCommand       *string `json:"verifyCommand,omitempty"`       // carried into the goal on promotion
	FreshContext        bool    `json:"freshContext,omitempty"`        // run on a clean context when promoted
	// Handover is the continuity note stored with the queued goal. It is
	// carried into the goal on promotion (postpone/promote/auto-promotion)
	// unless the caller supplies an explicit handover at that moment.
	Handoff   *string   `json:"handover,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// UpcomingGoalInput carries the fields needed to enqueue a goal. Using a
// struct keeps the append signature stable as per-goal options grow.
type UpcomingGoalInput struct {
	Objective           string
	CompletionCriterion *string
	VerifyCommand       *string
	FreshContext        bool
	// Handover is the continuity note to store with the queued goal (carried
	// into the goal when it is promoted). Free text, untrusted data.
	Handoff *string
}

// GoalChangeKind describes the kind of change for UI rendering.
type GoalChangeKind string

const (
	GoalChangeLifecycle  GoalChangeKind = "lifecycle"
	GoalChangeCompletion GoalChangeKind = "completion"
	// GoalChangeClear marks a goal.clear event that comes from an explicit
	// CancelGoal: the change rides on the clear event (nil snapshot) so
	// consumers can tell "cancelled" apart from "completed" — a completion
	// clear carries NO change, and a cancel clear carries the cancelling
	// actor. The actor decides the successor policy: a user/model cancel
	// promotes the queued successor PAUSED (it must not auto-start), while
	// runtime/framework clears (postpone, unblock, orchestrator) keep the
	// start-the-next-goal behavior.
	GoalChangeClear GoalChangeKind = "clear"
)

// GoalChange describes what changed on a goal.updated event.
type GoalChange struct {
	Kind   GoalChangeKind
	Status *GoalStatus
	Reason *string
	// Expectation carries the model-provided unblock condition for blocked
	// transitions; nil for other lifecycle changes.
	Expectation *string
	Actor       *GoalActor
	Stats       *GoalChangeStats
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
	// VerifyCommand is an optional machine-checkable done-condition executed
	// by the done-gate after the model confirms completion (exit 0 = pass).
	VerifyCommand *string
	Replace       bool
	// FreshContext, when true, runs this goal's continuation turns on a new
	// agent with a clean context (objective + handoff only) instead of reusing
	// the current conversation. History is preserved in the durable transcript;
	// it is simply not sent to the new agent. Default false = reuse current.
	FreshContext bool
	// Handoff is an optional note from a predecessor goal (typically the
	// evidence recorded at its completion), shown to the model as untrusted
	// context in the goal reminder.
	Handoff *string
}

// GoalReasonInput carries the justification for lifecycle changes. Reason is
// the "why"; Expectation is the "what is needed to unblock" for blocked
// transitions. The tool layer enforces non-empty values for model-initiated
// pause/blocked; runtime/user paths may leave them nil.
type GoalReasonInput struct {
	Reason      *string
	Expectation *string
}

// goalStage is the mutable internal state rebuilt from event records.
type goalStage struct {
	goalID              string
	name                string
	managedBy           string
	objective           string
	completionCriterion *string
	verifyCommand       *string
	freshContext        bool
	handoff             *string
	todos               []GoalTodoItem
	status              GoalStatus
	turnsUsed           int
	tokensUsed          int
	wallClockMs         int64
	wallClockResumedAt  *int64
	budgetLimits        GoalBudgetLimits
	terminalReason      *string
	terminalExpectation *string
	// pendingVerification is set when the done-gate (verify mode) has issued
	// its challenge and the goal awaits a confirmed, evidence-backed complete
	// call. In-memory only: it is not persisted to the event log, so a session
	// restart mid-verification simply re-challenges the next complete attempt.
	pendingVerification bool
	// verifyFailures counts consecutive failed machine verifications
	// (verifyCommand non-zero or judge FAIL). At maxVerifyFailures the goal
	// is auto-blocked for user review instead of looping forever. In-memory
	// only; a session restart resets the streak.
	verifyFailures int
	updatedAt      time.Time
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
	VerifyCommand       *string `json:"verifyCommand,omitempty"`
	Handoff             *string `json:"handover,omitempty"`
	FreshContext        *bool   `json:"freshContext,omitempty"`

	// goal.update fields (patches)
	Status       *string           `json:"status,omitempty"`
	Reason       *string           `json:"reason,omitempty"`
	Expectation  *string           `json:"expectation,omitempty"`
	Todos        []GoalTodoItem    `json:"todos,omitempty"`
	Actor        *string           `json:"actor,omitempty"`
	TurnsUsed    *int              `json:"turnsUsed,omitempty"`
	TokensUsed   *int              `json:"tokensUsed,omitempty"`
	WallClockMs  *int64            `json:"wallClockMs,omitempty"`
	BudgetLimits *GoalBudgetLimits `json:"budgetLimits,omitempty"`
}

// UnmarshalJSON decodes a goal event record, accepting both the current
// "handover" key and the legacy "handoff" key written before the surface
// rename, so persisted event logs from older sessions keep their continuity
// note on replay (backwards compatible, additive).
func (r *GoalEventRecord) UnmarshalJSON(data []byte) error {
	type recordAlias GoalEventRecord
	var aux struct {
		recordAlias
		LegacyHandoff *string `json:"handoff"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	*r = GoalEventRecord(aux.recordAlias)
	if r.Handoff == nil {
		r.Handoff = aux.LegacyHandoff
	}
	return nil
}
