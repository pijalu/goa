// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

// Telemetry event names emitted by the runtime.
const (
	TelemetryRunStarted   = "orch_run_started"
	TelemetryRunFinished  = "orch_run_finished"
	TelemetryAgentCrashed = "orch_agent_crashed"
)

// Telemetry is the minimal tracker interface the runtime calls on lifecycle
// transitions. Mirrors core/goal.Telemetry so the same client satisfies both.
// Nil telemetry is a no-op.
type Telemetry interface {
	Track(event string, props map[string]any)
}

// nopTelemetry discards all events.
type nopTelemetry struct{}

func (nopTelemetry) Track(string, map[string]any) {}

// GoalBinder is the narrow goal-system surface a Runtime uses for goal-bound
// runs. Defining it here keeps core/orchestrator decoupled from core/goal
// (SOLID: dependency inversion). The production implementation wraps a
// *goal.GoalMode and is supplied by the command/adapter layer.
type GoalBinder interface {
	// Create starts (or replaces) a goal for the orchestration objective and
	// applies an optional aggregate token budget. Returns the new goal id.
	Create(objective string, tokenBudget int) (string, error)

	// CreateWithName is like Create but also supplies a friendly name for the
	// goal. Implementations that do not support names may ignore the parameter.
	CreateWithName(objective, name string, tokenBudget int) (string, error)

	// RecordTokens accrues a token delta to the bound goal and reports whether
	// the aggregate budget is now exhausted (true ⇒ the run should stop).
	RecordTokens(delta int) (overBudget bool, err error)

	// Complete marks the bound goal as successfully finished.
	Complete(reason string) error

	// Block marks the bound goal as blocked (e.g. budget exhausted) without
	// completing it.
	Block(reason string) error

	// Delete discards the bound goal. Used for ephemeral goals and when a run
	// is explicitly deleted.
	Delete(reason string) error
}
