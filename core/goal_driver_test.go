// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pijalu/goa/core/goal"
)

type fakeAgent struct {
	errAfter int
	runs     int
}

func (a *fakeAgent) Run(ctx context.Context, prompt string) error {
	a.runs++
	if a.errAfter > 0 && a.runs >= a.errAfter {
		return errors.New("provider rate limit")
	}
	return nil
}

func TestContinuationPrompt_HowToEndGuidance(t *testing.T) {
	// The continuation prompt must tell the model exactly how a goal stops:
	// an actual goal tool call with action "update" — not prose, a bash echo,
	// or send_message. Regression: a model announced "the goal is complete" in
	// text and tried send_message to a nonexistent coordinator, never calling
	// the goal tool, so the driver kept launching continuation turns.
	for _, want := range []string{
		"HOW TO END A GOAL",
		"goal TOOL",
		`action "update"`,
		"does NOT end it",
		"send_message",
	} {
		if !strings.Contains(ContinuationPrompt, want) {
			t.Errorf("ContinuationPrompt missing how-to-end guidance %q", want)
		}
	}
}

type fakeAgentThatCompletes struct {
	mode *goal.GoalMode
	runs atomic.Int32
	done chan struct{}
}

func (a *fakeAgentThatCompletes) Run(ctx context.Context, prompt string) error {
	if a.runs.Add(1) == 1 && a.done != nil {
		close(a.done)
	}
	_, _ = a.mode.MarkComplete(goal.GoalReasonInput{}, goal.GoalActorModel)
	return nil
}

func TestGoalDriver_Drive(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	mode.CreateGoal(goal.CreateGoalInput{Objective: "fix tests"}, goal.GoalActorUser)
	agent := &fakeAgent{errAfter: 3}
	driver := &GoalDriver{Agent: agent, Mode: mode}

	err := driver.Drive(context.Background())
	if err == nil {
		t.Fatal("expected error from agent")
	}
	if agent.runs != 3 {
		t.Errorf("runs = %d", agent.runs)
	}
	if mode.GetGoal().Goal.Status != goal.GoalPaused {
		t.Errorf("status = %q, want paused", mode.GetGoal().Goal.Status)
	}
}

func TestGoalDriver_NoGoal(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	agent := &fakeAgent{}
	driver := &GoalDriver{Agent: agent, Mode: mode}
	if err := driver.Drive(context.Background()); err != nil {
		t.Fatal(err)
	}
	if agent.runs != 0 {
		t.Errorf("runs = %d", agent.runs)
	}
}

func TestGoalDriver_BudgetExceeded(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	mode.CreateGoal(goal.CreateGoalInput{Objective: "fix tests"}, goal.GoalActorUser)
	limit := 1
	mode.SetBudgetLimits(goal.GoalBudgetLimits{TurnBudget: &limit}, goal.GoalActorUser)
	mode.IncrementTurn()

	agent := &fakeAgent{}
	driver := &GoalDriver{Agent: agent, Mode: mode}
	if err := driver.Drive(context.Background()); err != nil {
		t.Fatal(err)
	}
	if agent.runs != 0 {
		t.Errorf("runs = %d", agent.runs)
	}
	if mode.GetGoal().Goal.Status != goal.GoalBlocked {
		t.Errorf("status = %q", mode.GetGoal().Goal.Status)
	}
}

func TestGoalDriver_ConcurrentDrive(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	mode.CreateGoal(goal.CreateGoalInput{Objective: "fix tests"}, goal.GoalActorUser)
	agent := &fakeAgentThatCompletes{mode: mode}
	driver := &GoalDriver{Agent: agent, Mode: mode}

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = driver.Drive(context.Background())
		}()
	}
	wg.Wait()

	if got := agent.runs.Load(); got != 1 {
		t.Errorf("runs = %d, want 1 (concurrent drives should be deduplicated)", got)
	}
}

func TestGoalDriver_Drive_NilAgent(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	mode.CreateGoal(goal.CreateGoalInput{Objective: "fix tests"}, goal.GoalActorUser)
	driver := &GoalDriver{Agent: nil, Mode: mode}

	if err := driver.Drive(context.Background()); err == nil {
		t.Fatal("expected error when agent is nil")
	}
}

// blockingAgent simulates an in-flight turn: Run blocks until its ctx is
// cancelled (what a real agent does on Interrupt/Stop) and counts entries.
type blockingAgent struct {
	runs    atomic.Int32
	started chan struct{}
}

func (a *blockingAgent) Run(ctx context.Context, _ string) error {
	a.runs.Add(1)
	select {
	case a.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return ctx.Err()
}

// TestGoalDriver_StopEndsDrive is the regression test for bugs.md "ESC: hard
// stop for ALL ongoing activities": the /goal command starts the driver on
// context.Background(), so AgentManager.Interrupt() (ESC) could kill the
// current turn but the loop immediately launched the next continuation.
// Stop() must cancel the drive loop itself: the in-flight Run returns, and
// NO further Run calls happen.
func TestGoalDriver_StopEndsDrive(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	mode.CreateGoal(goal.CreateGoalInput{Objective: "run forever"}, goal.GoalActorUser)
	agent := &blockingAgent{started: make(chan struct{}, 8)}
	driver := &GoalDriver{Agent: agent, Mode: mode}

	done := make(chan error, 1)
	go func() { done <- driver.Drive(context.Background()) }()

	<-agent.started // first continuation turn is in flight
	driver.Stop()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Drive should return the cancelled ctx error after Stop")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Drive did not exit after Stop")
	}
	if n := agent.runs.Load(); n != 1 {
		t.Fatalf("runs = %d after Stop, want exactly 1 (no further continuations)", n)
	}
	// Stop with no active loop must be a safe no-op (handleEscape fires it
	// on every ESC, goal or not).
	driver.Stop()
}

func TestGoalDriver_Start(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	mode.CreateGoal(goal.CreateGoalInput{Objective: "fix tests"}, goal.GoalActorUser)
	done := make(chan struct{})
	agent := &fakeAgentThatCompletes{mode: mode, done: done}
	driver := &GoalDriver{Agent: agent, Mode: mode}

	driver.Start(context.Background())
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("driver did not start")
	}

	if got := agent.runs.Load(); got != 1 {
		t.Errorf("runs = %d, want 1", got)
	}
}

// stallTestDriver builds a driver wired with the stall watchdog fakes.
func stallTestDriver(agent AgentRunner, mode *goal.GoalMode, probe *fakeProbe, remind *fakeRemind, turns int) *GoalDriver {
	return &GoalDriver{Agent: agent, Mode: mode, Probe: probe.Fingerprint, Remind: remind.Record, StallTurns: turns}
}

// TestGoalDriver_StallWatchdog_ChallengeThenAutoBlock covers the goals.stall_turns
// watchdog escalation: an unmanaged goal whose progress fingerprint never
// changes is challenged after StallTurns stale turns, then auto-blocked after
// the second unanswered challenge.
func TestGoalDriver_StallWatchdog_ChallengeThenAutoBlock(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	if _, err := mode.CreateGoal(goal.CreateGoalInput{Objective: "spin"}, goal.GoalActorUser); err != nil {
		t.Fatal(err)
	}
	probe := &fakeProbe{}
	remind := &fakeRemind{}
	// StallTurns=2: turns 1-2 stale → challenge 1; turns 3-4 stale →
	// challenge 2 → auto-block ends the loop.
	agent := &fakeAgent{}
	driver := stallTestDriver(agent, mode, probe, remind, 2)
	if err := driver.Drive(context.Background()); err != nil {
		t.Fatal(err)
	}
	if remind.Len() != 1 {
		t.Fatalf("reminders = %d, want 1 (challenge before block)", remind.Len())
	}
	if !strings.Contains(remind.messages[0], "No measurable progress") {
		t.Errorf("challenge text unexpected: %q", remind.messages[0])
	}
	g := mode.GetGoal().Goal
	if g == nil || g.Status != goal.GoalBlocked {
		t.Fatalf("status = %v, want blocked", g)
	}
	if g.TerminalReason == nil || *g.TerminalReason != "no measurable progress" {
		t.Errorf("terminal reason = %v, want %q", g.TerminalReason, "no measurable progress")
	}
	if g.TerminalExpectation == nil || *g.TerminalExpectation != "user direction on how to proceed" {
		t.Errorf("terminal expectation = %v", g.TerminalExpectation)
	}
}

// TestGoalDriver_StallWatchdog_ProgressResetsStreak: a changed fingerprint
// resets the stale counter, so no challenge fires.
func TestGoalDriver_StallWatchdog_ProgressResetsStreak(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	if _, err := mode.CreateGoal(goal.CreateGoalInput{Objective: "work"}, goal.GoalActorUser); err != nil {
		t.Fatal(err)
	}
	probe := &fakeProbe{}
	remind := &fakeRemind{}
	// Fingerprint changes on turn 2 (one stale turn only), so no
	// challenge ever fires; the goal completes on turn 3.
	agent := &changingAgent{mode: mode, probe: probe, changeAt: 2, completeAt: 3}
	driver := stallTestDriver(agent, mode, probe, remind, 2)
	if err := driver.Drive(context.Background()); err != nil {
		t.Fatal(err)
	}
	if remind.Len() != 0 {
		t.Errorf("reminders = %d, want 0 (progress resets the streak)", remind.Len())
	}
}

// TestGoalDriver_StallWatchdog_ManagedGoalsSkipped: orchestrator-managed goals
// have their own supervision and are never stall-watched.
func TestGoalDriver_StallWatchdog_ManagedGoalsSkipped(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	if _, err := mode.CreateGoal(goal.CreateGoalInput{Objective: "orch", ManagedBy: "orchestrator"}, goal.GoalActorUser); err != nil {
		t.Fatal(err)
	}
	probe := &fakeProbe{}
	remind := &fakeRemind{}
	agent := &completeAfterAgent{mode: mode, runs: 5}
	driver := stallTestDriver(agent, mode, probe, remind, 1)
	if err := driver.Drive(context.Background()); err != nil {
		t.Fatal(err)
	}
	if remind.Len() != 0 {
		t.Errorf("reminders = %d, want 0 (orchestrator goals have their own supervision)", remind.Len())
	}
}

// TestGoalDriver_StallWatchdog_DisabledWithoutProbe: a nil probe disables the
// watchdog even when StallTurns is set.
func TestGoalDriver_StallWatchdog_DisabledWithoutProbe(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	if _, err := mode.CreateGoal(goal.CreateGoalInput{Objective: "x"}, goal.GoalActorUser); err != nil {
		t.Fatal(err)
	}
	remind := &fakeRemind{}
	agent := &completeAfterAgent{mode: mode, runs: 4}
	driver := &GoalDriver{Agent: agent, Mode: mode, Remind: remind.Record, StallTurns: 1}
	if err := driver.Drive(context.Background()); err != nil {
		t.Fatal(err)
	}
	if remind.Len() != 0 {
		t.Errorf("reminders = %d, want 0 (nil probe disables the watchdog)", remind.Len())
	}
}

// fakeProbe returns a fixed fingerprint until changed.
type fakeProbe struct{ fp string }

func (p *fakeProbe) Fingerprint() string { return p.fp }

// fakeRemind records injected stall challenges.
type fakeRemind struct{ messages []string }

func (r *fakeRemind) Record(text string) { r.messages = append(r.messages, text) }
func (r *fakeRemind) Len() int           { return len(r.messages) }

// changingAgent changes the probe fingerprint at changeAt and completes the
// goal at completeAt.
type changingAgent struct {
	mode       *goal.GoalMode
	probe      *fakeProbe
	runs       int
	changeAt   int
	completeAt int
}

func (a *changingAgent) Run(ctx context.Context, prompt string) error {
	a.runs++
	if a.runs == a.changeAt {
		a.probe.fp = "progress"
	}
	if a.runs >= a.completeAt {
		_, _ = a.mode.MarkComplete(goal.GoalReasonInput{}, goal.GoalActorModel)
	}
	return nil
}

// completeAfterAgent completes the goal after a fixed number of runs.
type completeAfterAgent struct {
	mode *goal.GoalMode
	runs int
	seen int
}

func (a *completeAfterAgent) Run(ctx context.Context, prompt string) error {
	a.seen++
	if a.seen >= a.runs {
		_, _ = a.mode.MarkComplete(goal.GoalReasonInput{}, goal.GoalActorModel)
	}
	return nil
}

func TestMapDriverError(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{context.Canceled, "Paused after interruption"},
		{errors.New("rate limit hit"), PauseRateLimit},
		{errors.New("auth failed"), PauseAuthError},
		{errors.New("connection refused"), PauseConnError},
		{errors.New("api error 500"), PauseAPIError},
		{errors.New(`Engine protocol predict request returned 400: {"error":{"code":400,"message":"Unable to generate parser for this template. ... System message must be at the beginning.","type":"invalid_request_error"}}`), PauseRequestError},
		{errors.New("404 model not found"), PauseRequestError},
		{errors.New("Error: 408 - request timeout"), PauseRequestError},
		{errors.New("model not configured"), PauseModelConfig},
		{errors.New("boom"), PauseRuntimeError},
	}
	for _, tc := range cases {
		if got := mapDriverError(tc.err); got != tc.want {
			t.Errorf("mapDriverError(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}
// fakeFreshAgent records whether turns were routed via RunFresh (fresh context)
// or the ordinary Run path, and how many began a fresh context.
type fakeFreshAgent struct {
	mode        *goal.GoalMode
	freshRuns   int
	normalRuns  int
	beginCount  int
	sawBeginSeq []bool
}

func (a *fakeFreshAgent) Run(ctx context.Context, prompt string) error {
	a.normalRuns++
	_, _ = a.mode.MarkComplete(goal.GoalReasonInput{}, goal.GoalActorModel)
	return nil
}

func (a *fakeFreshAgent) RunFresh(ctx context.Context, prompt string, begin bool) error {
	a.freshRuns++
	a.sawBeginSeq = append(a.sawBeginSeq, begin)
	if begin {
		a.beginCount++
	}
	_, _ = a.mode.MarkComplete(goal.GoalReasonInput{}, goal.GoalActorModel)
	return nil
}

// TestGoalDriver_FreshContextRouting verifies a goal with the clean-context
// flag is routed through FreshAgentRunner.RunFresh (begin=true on the first
// turn) while a default goal uses the ordinary Run path (bugs.md: per-goal
// clean-context flag).
func TestGoalDriver_FreshContextRouting(t *testing.T) {
	// Fresh-context goal routes through RunFresh with begin=true.
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	if _, err := mode.CreateGoal(goal.CreateGoalInput{Objective: "self-contained", FreshContext: true}, goal.GoalActorModel); err != nil {
		t.Fatal(err)
	}
	fresh := &fakeFreshAgent{mode: mode}
	driver := &GoalDriver{Agent: fresh, Mode: mode}
	if err := driver.Drive(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fresh.freshRuns != 1 || fresh.normalRuns != 0 {
		t.Errorf("fresh goal: freshRuns=%d normalRuns=%d, want 1/0", fresh.freshRuns, fresh.normalRuns)
	}
	if fresh.beginCount != 1 || len(fresh.sawBeginSeq) != 1 || !fresh.sawBeginSeq[0] {
		t.Errorf("begin sequence = %v (count %d), want single begin=true", fresh.sawBeginSeq, fresh.beginCount)
	}

	// Default goal (flag unset) uses the ordinary Run path, never RunFresh.
	mode2 := goal.NewGoalMode(nil, nil, nil, nil)
	if _, err := mode2.CreateGoal(goal.CreateGoalInput{Objective: "normal"}, goal.GoalActorUser); err != nil {
		t.Fatal(err)
	}
	def := &fakeFreshAgent{mode: mode2}
	driver2 := &GoalDriver{Agent: def, Mode: mode2}
	if err := driver2.Drive(context.Background()); err != nil {
		t.Fatal(err)
	}
	if def.normalRuns != 1 || def.freshRuns != 0 {
		t.Errorf("default goal: normalRuns=%d freshRuns=%d, want 1/0", def.normalRuns, def.freshRuns)
	}
}
