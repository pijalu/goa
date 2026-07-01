// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"context"
	"errors"
	"sync"

	"github.com/pijalu/goa/core/goal"
)

// ContinuationPrompt is the prompt appended for each autonomous continuation turn.
const ContinuationPrompt = `Continue working toward the active goal.
Keep the self-audit brief. Do not explore unrelated interpretations once the
goal can be decided. If the objective is simple, already answered, impossible,
unsafe, or contradictory, do not run another goal turn. Explain briefly if useful,
then call UpdateGoal with "complete" or "blocked" in the same turn. Otherwise,
weigh the objective and any completion criteria against the work done so far.
Goal mode is iterative: do one coherent slice of work, then reassess.
Call UpdateGoal("complete") only when all required work is done, any stated
validation has passed, and there is no useful next action. Do not mark complete
after only producing a plan, summary, first pass, or partial result.
If an external condition or required user input prevents progress, or the objective
cannot be completed as stated, call UpdateGoal("blocked"). Otherwise keep going —
use the existing conversation context and your tools, and do not ask the user for
input unless a real blocker prevents progress.`

// Pause reasons used when the driver parks a goal after an error.
const (
	PauseRateLimit    = "Paused after provider rate limit"
	PauseConnError    = "Paused after provider connection error"
	PauseAuthError    = "Paused after provider authentication error"
	PauseAPIError     = "Paused after provider API error"
	PauseModelConfig  = "Paused after model configuration error"
	PauseRuntimeError = "Paused after runtime error"
)

// AgentRunner is the subset of agentic.Agent used by GoalDriver.
type AgentRunner interface {
	Run(ctx context.Context, input string) error
}

// GoalDriver runs continuation turns while a goal is active.
type GoalDriver struct {
	Agent   AgentRunner
	Mode    *goal.GoalMode
	mu      sync.Mutex
	driving bool
}

// Drive executes continuation turns while the goal is active. Only one Drive
// loop runs at a time; concurrent calls return immediately.
func (d *GoalDriver) Drive(ctx context.Context) error {
	d.mu.Lock()
	if d.driving {
		d.mu.Unlock()
		return nil
	}
	if d.Agent == nil {
		d.mu.Unlock()
		return errors.New("goal driver has no agent")
	}
	d.driving = true
	d.mu.Unlock()

	defer func() {
		d.mu.Lock()
		d.driving = false
		d.mu.Unlock()
	}()

	for {
		active := d.Mode.GetActiveGoal()
		if active == nil {
			return nil
		}
		if active.Budget.OverBudget {
			reason := "A configured budget was reached"
			d.Mode.MarkBlocked(goal.GoalReasonInput{Reason: &reason}, goal.GoalActorSystem)
			return nil
		}

		d.Mode.IncrementTurn()

		err := d.Agent.Run(ctx, ContinuationPrompt)
		if err != nil {
			reason := mapDriverError(err)
			d.Mode.PauseActiveGoal(goal.GoalReasonInput{Reason: &reason}, goal.GoalActorRuntime)
			return err
		}

		current := d.Mode.GetActiveGoal()
		if current == nil {
			return nil
		}
		if current.Budget.OverBudget {
			reason := "A configured budget was reached"
			d.Mode.MarkBlocked(goal.GoalReasonInput{Reason: &reason}, goal.GoalActorSystem)
			return nil
		}
	}
}

// Start begins autonomous driving in a background goroutine if an agent and an
// active goal are available. It is safe to call repeatedly; concurrent drives
// are deduplicated by Drive's internal guard.
func (d *GoalDriver) Start(ctx context.Context) {
	if d.Agent == nil || d.Mode.GetActiveGoal() == nil {
		return
	}
	go func() {
		_ = d.Drive(ctx)
	}()
}

func mapDriverError(err error) string {
	if errors.Is(err, context.Canceled) {
		return "Paused after interruption"
	}
	msg := err.Error()
	switch {
	case containsCI(msg, "rate limit"):
		return PauseRateLimit
	case containsCI(msg, "authentication") || containsCI(msg, "auth"):
		return PauseAuthError
	case containsCI(msg, "connection"):
		return PauseConnError
	case containsCI(msg, "api error"):
		return PauseAPIError
	case containsCI(msg, "model config") || containsCI(msg, "not configured"):
		return PauseModelConfig
	case containsCI(msg, "runaway loop") || containsCI(msg, "stream loop"):
		return "Paused after detecting a runaway response loop"
	default:
		return PauseRuntimeError
	}
}

func containsCI(s, substr string) bool {
	return len(s) >= len(substr) && containsAtCI(s, substr)
}

func containsAtCI(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if equalCI(s[i:i+len(substr)], substr) {
			return true
		}
	}
	return false
}

func equalCI(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
