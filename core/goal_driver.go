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
then call the goal tool with action "update", status "complete" or "blocked" in the same turn. Otherwise,
weigh the objective and any completion criteria against the work done so far.
Goal mode is iterative: do one coherent slice of work, then reassess.
Call goal with action "update", status "complete" only when all required work is done, any stated
validation has passed, and there is no useful next action. Do not mark complete
after only producing a plan, summary, first pass, or partial result. When a completion
criterion is recorded, the first "complete" call is intercepted by a verification
challenge: audit the criterion, then call "complete" again with "reason" citing the
concrete evidence.
If an external condition or required user input prevents progress, or the objective
cannot be completed as stated, call goal with action "update", status "blocked" with BOTH
"reason" (the concrete blocker) and "expectation" (exactly what input or change unblocks
it). Do not pause just to ask whether to continue — that defeats goal mode. Otherwise
keep going: use the existing conversation context and your tools, and do not ask the
user for input unless a real blocker prevents progress.

HOW TO END A GOAL: the goal only stops when you make an actual goal TOOL
CALL with action "update", status "complete" or "blocked". Writing "the goal is complete" (or
similar) in your reply text does NOT end it — the driver will start another
continuation turn. Do not announce completion in prose, do not echo a summary
with the bash tool, and do not send the result to another agent with
send_message; none of those change the goal state. When the work is truly done,
invoke the goal tool with action "update" in that same turn and let its result speak for itself.`

// Pause reasons used when the driver parks a goal after an error.
const (
	PauseRateLimit    = "Paused after provider rate limit"
	PauseConnError    = "Paused after provider connection error"
	PauseAuthError    = "Paused after provider authentication error"
	PauseAPIError     = "Paused after provider API error"
	PauseRequestError = "Paused after provider request error"
	PauseModelConfig  = "Paused after model configuration error"
	PauseRuntimeError = "Paused after runtime error"
)

// AgentRunner is the subset of agentic.Agent used by GoalDriver.
type AgentRunner interface {
	Run(ctx context.Context, input string) error
}

// FreshAgentRunner is an optional extension of AgentRunner. When the active
// goal carries the fresh-context flag (bugs.md: per-goal clean-context), the
// driver routes its continuation turns through RunFresh so they execute on a
// clean context (objective + handoff only) instead of the full conversation.
// History is preserved by the implementation and restored when the goal ends.
type FreshAgentRunner interface {
	AgentRunner
	// RunFresh runs one turn on a clean context. begin is true on the first
	// continuation turn of a fresh-context goal (the implementation should
	// snapshot/reset context and surface a visible boundary); it is false on
	// subsequent turns of the same fresh-context goal.
	RunFresh(ctx context.Context, input string, begin bool) error
}

// GoalDriver runs continuation turns while a goal is active.
type GoalDriver struct {
	Agent   AgentRunner
	Mode    *goal.GoalMode
	mu      sync.Mutex
	driving bool
	// freshBegun tracks whether the current fresh-context goal has already
	// emitted its begin (context-reset) turn, so only the first continuation
	// passes begin=true. Reset whenever the active goal changes or clears.
	freshBegunFor string
	// stop cancels the current drive loop's context. Set by Drive while a
	// loop is active; called by Stop (ESC hard stop — bugs.md "ESC: hard
	// stop for ALL ongoing activities"). Nil when no loop is running.
	stop context.CancelFunc
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
	// Derive a cancellable loop ctx so Stop (ESC hard stop) can end the drive
	// even when the caller passed context.Background() — which is exactly
	// what the /goal command does (core/commands/goal.go), previously making
	// an active goal immune to ESC: Interrupt() cancelled the current turn
	// and the loop immediately launched the next continuation.
	ctx, stop := context.WithCancel(ctx)
	d.stop = stop
	d.mu.Unlock()

	defer func() {
		d.mu.Lock()
		d.driving = false
		d.stop = nil
		d.mu.Unlock()
		stop()
	}()

	for {
		// Hard-stop check before launching another turn: without this, a Stop
		// landing between turns would still start one more continuation.
		if err := ctx.Err(); err != nil {
			return err
		}
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

		err := d.runTurn(ctx, active)
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

// runTurn executes one continuation turn for the active goal. A goal carrying
// the fresh-context flag is routed through FreshAgentRunner.RunFresh (when the
// configured runner supports it) so its turns execute on a clean context; the
// first such turn passes begin=true so the runner can reset context and render
// a visible boundary. Default goals (and runners without fresh support) use
// the ordinary Run path against the current conversation.
func (d *GoalDriver) runTurn(ctx context.Context, active *goal.GoalSnapshot) error {
	if active.FreshContext {
		if fr, ok := d.Agent.(FreshAgentRunner); ok {
			begin := d.freshBegunFor != active.GoalID
			d.freshBegunFor = active.GoalID
			return fr.RunFresh(ctx, ContinuationPrompt, begin)
		}
	}
	// Any non-fresh turn (or a different goal) resets the begin tracker.
	d.freshBegunFor = ""
	return d.Agent.Run(ctx, ContinuationPrompt)
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

// Stop cancels the active drive loop: the in-flight turn's context is
// cancelled and no further continuation turns are launched. It is the goal
// half of the ESC hard stop (bugs.md "ESC: hard stop for ALL ongoing
// activities") — App.handleEscape pairs it with AgentManager.Interrupt so the
// current turn dies AND the loop cannot continue. No-op when no loop runs.
func (d *GoalDriver) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stop != nil {
		d.stop()
	}
}

// driverErrorRules maps error substrings to pause reasons, evaluated in
// order (first match wins). Table-driven to keep mapDriverError within the
// cyclomatic budget. HTTP 4xx request errors (e.g. LM Studio's "400 ...
// System message must be at the beginning") come BEFORE the connection
// catch-all: the request reached the server and was rejected — the pause
// reason must say the request itself was refused, not that the connection
// failed.
var driverErrorRules = []struct {
	reason  string
	substrs []string
}{
	{PauseRateLimit, []string{"rate limit"}},
	{PauseAuthError, []string{"authentication", "auth"}},
	{PauseRequestError, []string{"400", "invalid_request", "404", "422", "unprocessable"}},
	{PauseConnError, []string{"connection"}},
	{PauseAPIError, []string{"api error"}},
	{PauseModelConfig, []string{"model config", "not configured"}},
	{"Paused after detecting a runaway response loop", []string{"runaway loop", "stream loop"}},
}

func mapDriverError(err error) string {
	if errors.Is(err, context.Canceled) {
		return "Paused after interruption"
	}
	msg := err.Error()
	for _, rule := range driverErrorRules {
		for _, sub := range rule.substrs {
			if containsCI(msg, sub) {
				return rule.reason
			}
		}
	}
	return PauseRuntimeError
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
