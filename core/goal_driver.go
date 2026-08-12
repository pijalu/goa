// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
concrete evidence. When a verify command is recorded, it executes after your confirmed
completion and a non-zero exit rejects it — keep it passing as you work. Turns with no
measurable progress (todo transitions or workspace changes) are counted by a stall
watchdog: continued stalling auto-blocks the goal for user direction, so make every
turn produce visible progress or end the goal explicitly.
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
	PauseRunawayLoop  = "Paused after detecting a runaway response loop"
	PauseSilentStop   = "Paused after model stopped mid-reasoning (send continue to resume)"
)

// RunawayRecoveryPrompt replaces the byte-identical ContinuationPrompt for
// the first turn after a goal resumes from a runaway-loop pause. Resuming
// into the same prompt would deterministically re-enter the conditions that
// tripped the guardrail (runaway-loop bricking); the varied prompt
// forces a diagnosis and a different approach instead.
const RunawayRecoveryPrompt = `The previous goal turn was stopped by a runaway-loop guardrail: the same
response repeated across consecutive turns without progress. Do NOT repeat
your previous reply. In one or two sentences, diagnose why progress stalled,
then take a concrete DIFFERENT action toward the active goal: use another
tool, gather new evidence, or — if the goal genuinely cannot advance — call
goal with action "update", status "blocked" with reason+expectation.`

// ErrAgentBusy is returned by an AgentRunner when the agent is busy with
// another turn. It is a clean stop for the drive loop — NOT an error: the
// goal stays active (never paused) and the post-turn hook of the in-flight
// turn re-starts the drive once the agent is idle. Without this guard, a
// drive started mid-turn hits the agent's queue-on-busy semantics: Run
// returns instantly, the loop hot-spins, and hundreds of continuation
// prompts flood the agent's input queue — phantom turns that keep arriving
// even after the goal is cleared (Issue 7: "goal cannot be
// stopped").
var ErrAgentBusy = errors.New("goal driver: agent busy with another turn")

// ErrSilentStop is returned by runTurn when the turn completed without error
// but the model stopped mid-reasoning (produced thinking but no answer content
// or tool calls — a reasoning-token or output limit on the provider side). The
// drive loop must pause the goal instead of auto-continuing, because the next
// continuation turn would deterministically hit the same limit again. The user
// is told to "send continue to resume", so the goal must wait for that resume.
var ErrSilentStop = errors.New("goal driver: model stopped after reasoning without producing a reply")

// SilentStopReporter is an optional interface that AgentRunner implementations
// can implement to report whether the most recently completed turn ended with a
// "silent stop" (model produced thinking/reasoning but no visible answer or
// tool calls). The goal driver checks this after each turn to decide whether to
// pause instead of auto-continuing into the same reasoning limit.
type SilentStopReporter interface {
	LastTurnSilentStop() bool
}

// AgentRunner is the subset of agentic.Agent used by GoalDriver.
type AgentRunner interface {
	Run(ctx context.Context, input string) error
}

// FreshAgentRunner is an optional extension of AgentRunner. When the active
// goal carries the fresh-context flag (per-goal clean-context), the
// driver routes its continuation turns through RunFresh so they execute on a
// clean context (objective + handover only) instead of the full conversation.
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
	// loop is active; called by Stop (ESC hard stop — "ESC: hard
	// stop for ALL ongoing activities"). Nil when no loop is running.
	stop context.CancelFunc

	// Stall watchdog (goals.stall_turns): after each turn of an unmanaged
	// goal, Probe fingerprints progress; when the fingerprint stops changing
	// for StallTurns consecutive turns the model is challenged via Remind,
	// and after stallChallengeLimit unanswered challenges the goal is
	// auto-blocked for user direction. All three fields nil/0 = disabled.
	Probe      func() string
	Remind     func(string)
	StallTurns int
	// stallGoalID/lastFP/stale/stallChallenges are watchdog state. They are
	// only touched from the single Drive loop, so no extra locking is
	// needed beyond Drive's own exclusivity guard.
	stallGoalID     string
	lastFP          string
	stale           int
	stallChallenges int

	// runawayPausedFor records the goal ID whose last turn was paused by
	// the runaway-loop guardrail. The first continuation after that resume
	// swaps the byte-identical ContinuationPrompt for RunawayRecoveryPrompt
	// and resets the agent's loop latch, so pause → resume cannot blindly
	// re-enter the conditions that tripped the guardrail.
	// Written by handleTurnError, consumed by runTurn — both run on the
	// single Drive loop goroutine, so no extra locking is needed.
	runawayPausedFor string

	// TeamOverlay manages goal-scoped team overlays (TEAMS.md §5.2). When a
	// team-bound goal is active, the drive loop applies the team's overlay for
	// the goal's duration and removes it when the goal ends (mirrors
	// FreshContext's per-goal tracking). overlayGoalID records which goal the
	// overlay is currently bound to so it is applied once per goal. Nil =
	// team overlays disabled (no TeamManager wired). Both fields are only
	// touched from the single Drive loop.
	TeamOverlay    TeamOverlayManager
	overlayGoalID  string
}

// TeamOverlayManager is the subset of the TeamManager the goal drive loop needs
// to apply/remove goal-scoped team overlays. It is defined here (in package
// core) to avoid a core → core/team dependency cycle; the concrete
// *team.Manager satisfies it.
type TeamOverlayManager interface {
	// ApplyOverlay applies the named team as a goal overlay (snapshotting the
	// session state for later restore). It is idempotent-safe: re-applying the
	// same overlay is a no-op when one is already active.
	ApplyOverlay(name string) error
	// RemoveOverlay tears down the active goal overlay, restoring the session.
	// It is a no-op when no overlay is active.
	RemoveOverlay() error
}

// stallChallengeLimit is the number of unanswered stall challenges after
// which the watchdog auto-blocks the goal instead of challenging again.
const stallChallengeLimit = 2

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
			d.syncTeamOverlay(nil)
			return nil
		}
		// Apply/remove the goal-scoped team overlay to match the active goal
		// (TEAMS.md §5.2). Done once per goal before the first turn.
		d.syncTeamOverlay(active)
		if active.Budget.OverBudget {
			reason := "A configured budget was reached"
			d.Mode.MarkBlocked(goal.GoalReasonInput{Reason: &reason}, goal.GoalActorSystem)
			return nil
		}

		d.Mode.IncrementTurn()

		err := d.runTurn(ctx, active)
		if err != nil {
			return d.handleTurnError(err)
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
		d.checkStall(current)
	}
}

// syncTeamOverlay keeps the goal-scoped team overlay aligned to the active goal
// (TEAMS.md §5.2). When a team-bound goal is active and the overlay is not yet
// bound to it, the team is applied for the goal's duration. When the active goal
// clears (nil) or has no team, any lingering overlay is removed so the session
// state is restored. It is a no-op when no TeamOverlay manager is wired. Errors
// are logged via the overlay manager's own logging; the drive loop continues
// (a missing/undefined team does not block the goal — it runs session-default).
func (d *GoalDriver) syncTeamOverlay(active *goal.GoalSnapshot) {
	if d.TeamOverlay == nil {
		return
	}
	want := ""
	wantID := ""
	if active != nil {
		want = strings.TrimSpace(active.Team)
		wantID = active.GoalID
	}
	switch {
	case want == "" && d.overlayGoalID != "":
		// Active goal ended (or has no team): tear down the lingering overlay.
		_ = d.TeamOverlay.RemoveOverlay()
		d.overlayGoalID = ""
	case want != "" && d.overlayGoalID != wantID:
		// New team-bound goal: apply its overlay. RemoveOverlay is safe first
		// (no-op when none) so a prior overlay for a different goal is cleared.
		if d.overlayGoalID != "" {
			_ = d.TeamOverlay.RemoveOverlay()
		}
		_ = d.TeamOverlay.ApplyOverlay(want)
		d.overlayGoalID = wantID
	}
}

// handleTurnError maps a turn failure to the drive-loop verdict. ErrAgentBusy
// is a CLEAN stop (nil): another turn owns the agent, so the goal stays
// active and the in-flight turn's post-turn hook re-starts the drive once
// the agent is idle (Issue 7). Any other error pauses the goal with
// a mapped reason and propagates.
func (d *GoalDriver) handleTurnError(err error) error {
	if errors.Is(err, ErrAgentBusy) {
		return nil
	}
	reason := mapDriverError(err)
	if errors.Is(err, ErrSilentStop) {
		reason = PauseSilentStop
	}
	pauseReason := reason
	if reason == PauseRunawayLoop {
		// The guardrail error carries the (elided) repeated sequence; keep it
		// in the stored pause reason so the TUI stop surface and the goal
		// events log show WHAT was judged a loop (runaway-loop
		// visibility).
		pauseReason = reason + ": " + err.Error()
	}
	paused, _ := d.Mode.PauseActiveGoal(goal.GoalReasonInput{Reason: &pauseReason}, goal.GoalActorRuntime)
	if reason == PauseRunawayLoop && paused != nil {
		// Mark the goal so the next continuation after resume swaps in the
		// varied recovery prompt and resets the agent's loop latch.
		d.runawayPausedFor = paused.GoalID
	}
	return err
}

// checkStall runs the stall watchdog after a completed turn. It only guards
// unmanaged goals (orchestrator-owned goals have their own supervision).
// Caller: the single Drive loop, after the post-turn budget check.
func (d *GoalDriver) checkStall(current *goal.GoalSnapshot) {
	if d.Probe == nil || d.Remind == nil || d.StallTurns <= 0 || current.ManagedBy != "" {
		return
	}
	fp := d.Probe()
	if current.GoalID != d.stallGoalID {
		d.stallGoalID, d.lastFP, d.stale, d.stallChallenges = current.GoalID, fp, 0, 0
		return
	}
	if fp != d.lastFP {
		d.lastFP, d.stale, d.stallChallenges = fp, 0, 0
		return
	}
	d.stale++
	if d.stale < d.StallTurns {
		return
	}
	d.stale = 0
	d.stallChallenges++
	d.Mode.TrackStall(d.stallChallenges)
	if d.stallChallenges >= stallChallengeLimit {
		reason := "no measurable progress"
		expectation := "user direction on how to proceed"
		_, _ = d.Mode.MarkBlocked(goal.GoalReasonInput{Reason: &reason, Expectation: &expectation}, goal.GoalActorSystem)
		return
	}
	d.Remind(fmt.Sprintf("No measurable progress in %d turns (no todo transitions, no workspace changes). Make measurable progress now, revise the todo list to match reality, or call goal with action \"update\", status \"blocked\" with reason+expectation. Further stalled turns will auto-block the goal for user review.", d.StallTurns))
}

// runTurn executes one continuation turn for the active goal. A goal carrying
// the fresh-context flag is routed through FreshAgentRunner.RunFresh (when the
// configured runner supports it) so its turns execute on a clean context; the
// first such turn passes begin=true so the runner can reset context and render
// a visible boundary. Default goals (and runners without fresh support) use
// the ordinary Run path against the current conversation.
func (d *GoalDriver) runTurn(ctx context.Context, active *goal.GoalSnapshot) error {
	prompt := d.turnPrompt(active)
	var err error
	if active.FreshContext {
		if fr, ok := d.Agent.(FreshAgentRunner); ok {
			begin := d.freshBegunFor != active.GoalID
			d.freshBegunFor = active.GoalID
			err = fr.RunFresh(ctx, prompt, begin)
			if err != nil {
				return err
			}
			return d.checkSilentStop()
		}
	}
	// Normal Run path (not fresh-context, or runner lacks fresh support).
	// Any non-fresh turn resets the begin tracker.
	d.freshBegunFor = ""
	err = d.Agent.Run(ctx, prompt)
	if err != nil {
		return err
	}
	return d.checkSilentStop()
}

// checkSilentStop returns ErrSilentStop when the agent reports that the just-
// completed turn ended with a silent stop (model stopped mid-reasoning). The
// goal driver pauses the goal so the user can resume with "continue" instead
// of auto-continuing into the same reasoning limit.
func (d *GoalDriver) checkSilentStop() error {
	if sr, ok := d.Agent.(SilentStopReporter); ok && sr.LastTurnSilentStop() {
		return ErrSilentStop
	}
	return nil
}

// turnPrompt picks the continuation prompt for this turn. The first turn
// after a runaway-loop pause gets the varied RunawayRecoveryPrompt and the
// agent's loop latch is reset — resuming with the byte-identical
// ContinuationPrompt would deterministically re-enter the conditions that
// tripped the guardrail, and the still-latched agent would reject the turn
// outright (runaway-loop bricking).
func (d *GoalDriver) turnPrompt(active *goal.GoalSnapshot) string {
	if d.runawayPausedFor == "" || d.runawayPausedFor != active.GoalID {
		return ContinuationPrompt
	}
	d.runawayPausedFor = ""
	if lr, ok := d.Agent.(interface{ ResetLoopStop() }); ok {
		lr.ResetLoopStop()
	}
	return RunawayRecoveryPrompt
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
// half of the ESC hard stop ("ESC: hard stop for ALL ongoing
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
	{PauseRequestError, []string{"400", "invalid_request", "404", "408", "422", "unprocessable"}},
	{PauseConnError, []string{"connection"}},
	{PauseAPIError, []string{"api error"}},
	{PauseModelConfig, []string{"model config", "not configured"}},
	{PauseRunawayLoop, []string{"runaway loop", "stream loop"}},
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
