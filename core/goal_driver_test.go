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
	// an actual UpdateGoal tool call — not prose, a bash echo, or send_message.
	// Regression: a model announced "the goal is complete" in text and tried
	// send_message to a nonexistent coordinator, never calling UpdateGoal, so
	// the driver kept launching continuation turns.
	for _, want := range []string{
		"HOW TO END A GOAL",
		"UpdateGoal TOOL",
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
		{errors.New("model not configured"), PauseModelConfig},
		{errors.New("boom"), PauseRuntimeError},
	}
	for _, tc := range cases {
		if got := mapDriverError(tc.err); got != tc.want {
			t.Errorf("mapDriverError(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}