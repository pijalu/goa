// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/tui"
)

func TestGoalCommand_QueueManagerMoveAndDelete(t *testing.T) {
	cmd, queue := newManagerCommand(t, "")
	ids := appendQueued(t, queue, "alpha", "beta")

	// Phase 1: move the second goal (beta) up via the '+' emit.
	ctx := testContext()
	opens := 0
	var cursor string
	ctx.SelectOptionFunc = func(_ string, _ []tui.SelectorItem, current string, cb func(string, bool)) {
		opens++
		cursor = current
		if opens == 1 {
			cb("__moveup__"+ids[1], true) // simulate '+' on beta
			return
		}
		cb("", false) // close the reopened manager
	}

	if err := cmd.Run(ctx, []string{"manage"}); err != nil {
		t.Fatal(err)
	}
	assertQueueObjectives(t, queue, []string{"beta", "alpha"})
	expectManagerCursor(t, opens, 2, cursor, ids[1])

	// Phase 2: delete the front goal via the Delete emit + confirmation.
	opens = 0
	var confirmTitle string
	ctx.SelectOptionFunc = func(title string, _ []tui.SelectorItem, _ string, cb func(string, bool)) {
		opens++
		switch opens {
		case 1:
			cb("__delete__"+ids[1], true) // simulate Delete on beta
		case 2:
			confirmTitle = title
			cb("yes", true)
		default:
			cb("", false) // close the reopened manager
		}
	}

	if err := cmd.Run(ctx, []string{"manage"}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(confirmTitle, "Delete goal ") {
		t.Errorf("expected a delete confirmation, got selector %q", confirmTitle)
	}
	assertQueueObjectives(t, queue, []string{"alpha"})
	if opens != 3 {
		t.Errorf("expected manager→confirm→manager (3 opens), got %d", opens)
	}
}

func testContext() core.Context {
	return core.Context{
		OutputBuffer:     &strings.Builder{},
		SelectOptionFunc: func(string, []tui.SelectorItem, string, func(string, bool)) {},
		ShowInputFunc:    func(string, string, func(string, bool)) {},
		RequestMainInput: func(string, func(string)) {},
	}
}

// queuePublishRecorder captures goal publish events so a test can assert that
// a durable-queue mutation re-published the goal snapshot (the footer ◈ count
// depends on it — queue ops emit no lifecycle event of their own).
type queuePublishRecorder struct {
	snaps   int
	changes int
}

func (p *queuePublishRecorder) Publish(snap *goal.GoalSnapshot, change *goal.GoalChange) {
	p.snaps++
	if change != nil {
		p.changes++
	}
}

func TestGoalCommand_QueueOpsPublishRefresh(t *testing.T) {
	pub := &queuePublishRecorder{}
	mode := goal.NewGoalMode(nil, pub, nil, nil)
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "queue.json"))
	cmd := &GoalCommand{Mode: mode, Queue: queue}
	ctx := testContext()

	if err := cmd.Run(ctx, []string{"seed-active"}); err != nil {
		t.Fatal(err)
	}
	base := pub.snaps // the create lifecycle publish

	if err := cmd.Run(ctx, []string{"next", "queued-one"}); err != nil {
		t.Fatal(err)
	}
	if pub.snaps != base+1 {
		t.Errorf("queue insert published %d snaps (base %d), want exactly one refresh", pub.snaps, base)
	}
	if pub.changes != 0 {
		t.Errorf("queue insert published %d changes, want 0 (no chat marker)", pub.changes)
	}
}

type testAutonomySwitcher struct {
	level internal.AutonomyLevel
}

func (s *testAutonomySwitcher) Current() internal.AutonomyLevel { return s.level }

func (s *testAutonomySwitcher) SetAutonomy(l internal.AutonomyLevel) error {
	s.level = l
	return nil
}

type fakeAgentThatCompletesGoal struct {
	mode *goal.GoalMode
	done chan struct{}
}

func (a *fakeAgentThatCompletesGoal) Run(ctx context.Context, prompt string) error {
	if a.done != nil {
		close(a.done)
	}
	_, _ = a.mode.MarkComplete(goal.GoalReasonInput{}, goal.GoalActorModel)
	return nil
}

func TestGoalCommand_CreateGoal_StartsDriver(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore("")
	done := make(chan struct{})
	driver := &core.GoalDriver{Mode: mode, Agent: &fakeAgentThatCompletesGoal{mode: mode, done: done}}
	switcher := &testAutonomySwitcher{level: internal.AutonomyYolo}
	cmd := &GoalCommand{Mode: mode, Queue: queue, Driver: driver, AutonomySwitcher: switcher}
	ctx := testContext()

	if err := cmd.Run(ctx, []string{"fix tests"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("driver did not start after goal creation")
	}
}

// fakeAgentSignalsRun records each Run invocation on a buffered channel so a
// test can tell a continuation turn happened without driving the goal to
// completion (which would clear it).
type fakeAgentSignalsRun struct {
	ran chan struct{}
}

func (a *fakeAgentSignalsRun) Run(ctx context.Context, prompt string) error {
	select {
	case a.ran <- struct{}{}:
	default:
	}
	return nil
}

func TestGoalCommand_Resume_StartsDriver(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore("")
	ran := make(chan struct{}, 4)
	driver := &core.GoalDriver{Mode: mode, Agent: &fakeAgentSignalsRun{ran: ran}}
	switcher := &testAutonomySwitcher{level: internal.AutonomyYolo}
	cmd := &GoalCommand{Mode: mode, Queue: queue, Driver: driver, AutonomySwitcher: switcher}
	ctx := testContext()

	if err := cmd.Run(ctx, []string{"fix tests"}); err != nil {
		t.Fatal(err)
	}
	// Drain the continuation turn triggered by goal creation.
	waitForRun(t, ran, "creation continuation")

	if err := cmd.Run(ctx, []string{"pause"}); err != nil {
		t.Fatal(err)
	}
	if mode.GetGoal().Goal.Status != goal.GoalPaused {
		t.Fatalf("status after pause = %q", mode.GetGoal().Goal.Status)
	}
	// The pause may race a queued continuation; give any in-flight turn a
	// moment, then drain so the resume assertion is not polluted.
	drainRuns(ran)

	if err := cmd.Run(ctx, []string{"resume"}); err != nil {
		t.Fatal(err)
	}
	if mode.GetGoal().Goal.Status != goal.GoalActive {
		t.Fatalf("status after resume = %q", mode.GetGoal().Goal.Status)
	}
	// The whole point: resume alone must schedule a new continuation turn.
	waitForRun(t, ran, "resume continuation")
}

func TestGoalCommand_Resume_NoCurrentGoal_PromotesFirstQueued(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "queue.json"))
	ran := make(chan struct{}, 4)
	driver := &core.GoalDriver{Mode: mode, Agent: &fakeAgentSignalsRun{ran: ran}}
	switcher := &testAutonomySwitcher{level: internal.AutonomyYolo}
	cmd := &GoalCommand{Mode: mode, Queue: queue, Driver: driver, AutonomySwitcher: switcher}
	ctx := testContext()

	if _, err := queue.AppendGoal(goal.UpcomingGoalInput{Objective: "first queued"}); err != nil {
		t.Fatal(err)
	}
	if _, err := queue.AppendGoal(goal.UpcomingGoalInput{Objective: "second queued"}); err != nil {
		t.Fatal(err)
	}

	if err := cmd.Run(ctx, []string{"resume"}); err != nil {
		t.Fatal(err)
	}
	defer driver.Stop()

	current := mode.GetGoal().Goal
	if current == nil {
		t.Fatal("resume should have promoted the first queued goal to current")
	}
	if current.Objective != "first queued" {
		t.Errorf("promoted objective = %q, want %q", current.Objective, "first queued")
	}
	if current.Status != goal.GoalActive {
		t.Errorf("status = %q, want active", current.Status)
	}
	queued, err := queue.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 1 || queued[0].Objective != "second queued" {
		t.Errorf("queue after promotion = %+v, want only [second queued]", queued)
	}
	if out := ctx.OutputBuffer.String(); !strings.Contains(out, "first queued") {
		t.Errorf("resume output = %q, want it to name the promoted goal", out)
	}
	// Like a plain resume, the promotion must schedule a continuation turn.
	waitForRun(t, ran, "resume-from-queue continuation")
}

func TestGoalCommand_Resume_NoCurrentGoal_EmptyQueue(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "queue.json"))
	cmd := &GoalCommand{Mode: mode, Queue: queue}
	ctx := testContext()

	err := cmd.Run(ctx, []string{"resume"})
	if err == nil || !strings.Contains(err.Error(), "no current goal to resume") {
		t.Errorf("resume error = %v, want 'no current goal to resume'", err)
	}
}

func TestGoalCommand_Next_NoActiveGoal_PrintsResumeNote(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "queue.json"))
	cmd := &GoalCommand{Mode: mode, Queue: queue}
	ctx := testContext()

	if err := cmd.Run(ctx, []string{"next", "parked work"}); err != nil {
		t.Fatal(err)
	}
	if mode.GetGoal().Goal != nil {
		t.Error("/goal:next must not auto-start the goal when none is active")
	}
	queued, err := queue.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 1 || queued[0].Objective != "parked work" {
		t.Errorf("queue = %+v, want [parked work]", queued)
	}
	if out := ctx.OutputBuffer.String(); !strings.Contains(out, "/goal:resume to start") {
		t.Errorf("output = %q, want the '/goal:resume to start' note", out)
	}
}

func TestGoalCommand_NextLast_NoActiveGoal_PrintsResumeNote(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "queue.json"))
	cmd := &GoalCommand{Mode: mode, Queue: queue}
	ctx := testContext()

	if err := cmd.Run(ctx, []string{"next", "last", "parked work"}); err != nil {
		t.Fatal(err)
	}
	if out := ctx.OutputBuffer.String(); !strings.Contains(out, "/goal:resume to start") {
		t.Errorf("output = %q, want the '/goal:resume to start' note", out)
	}
}

func TestGoalCommand_Next_WithActiveGoal_NoResumeNote(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "queue.json"))
	cmd := &GoalCommand{Mode: mode, Queue: queue}
	ctx := testContext()

	if err := cmd.Run(ctx, []string{"first"}); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Run(ctx, []string{"next", "queued work"}); err != nil {
		t.Fatal(err)
	}
	if out := ctx.OutputBuffer.String(); strings.Contains(out, "/goal:resume to start") {
		t.Errorf("output = %q, must not contain the parked-goal note with an active goal", out)
	}
}

// fakeAgentBlocksUntilCancel simulates an in-flight continuation turn: Run
// blocks until its context is cancelled, then returns the ctx error — the
// same unwinding a real agent performs on interrupt.
type fakeAgentBlocksUntilCancel struct {
	started chan struct{}
	done    chan error
}

func (a *fakeAgentBlocksUntilCancel) Run(ctx context.Context, _ string) error {
	select {
	case a.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	err := ctx.Err()
	select {
	case a.done <- err:
	default:
	}
	return err
}

func TestGoalCommand_Cancel_StopsDriveLoop(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore("")
	fake := &fakeAgentBlocksUntilCancel{started: make(chan struct{}, 1), done: make(chan error, 1)}
	driver := &core.GoalDriver{Mode: mode, Agent: fake}
	switcher := &testAutonomySwitcher{level: internal.AutonomyYolo}
	cmd := &GoalCommand{Mode: mode, Queue: queue, Driver: driver, AutonomySwitcher: switcher}
	ctx := testContext()

	if err := cmd.Run(ctx, []string{"fix tests"}); err != nil {
		t.Fatal(err)
	}
	// Wait until the drive loop is inside the (blocking) continuation turn.
	select {
	case <-fake.started:
	case <-time.After(time.Second):
		t.Fatal("drive loop did not start a continuation turn")
	}

	if err := cmd.Run(ctx, []string{"cancel"}); err != nil {
		t.Fatal(err)
	}

	// The cancel must interrupt the in-flight turn via the loop's context.
	select {
	case err := <-fake.done:
		if err != context.Canceled {
			t.Fatalf("turn ended with %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not interrupt the in-flight continuation turn")
	}
}

func waitForRun(t *testing.T, ran chan struct{}, what string) {
	t.Helper()
	select {
	case <-ran:
	case <-time.After(2 * time.Second):
		t.Fatalf("driver did not run %s without a user message", what)
	}
}

func drainRuns(ran chan struct{}) {
	for {
		select {
		case <-ran:
		case <-time.After(50 * time.Millisecond):
			return
		}
	}
}

func TestGoalCommand_Meta(t *testing.T) {
	cmd := &GoalCommand{}
	if cmd.Name() != "goal" {
		t.Errorf("name = %q", cmd.Name())
	}
	if cmd.Aliases() != nil {
		t.Error("aliases should be nil")
	}
	if cmd.ShortHelp() == "" {
		t.Error("short help empty")
	}
	if cmd.LongHelp() == "" {
		t.Error("long help empty")
	}
}

func TestPermissionOptions(t *testing.T) {
	cmd := &GoalCommand{}
	manual := cmd.permissionOptions(internal.AutonomyConfirm)
	if len(manual) != 4 {
		t.Errorf("manual options = %d", len(manual))
	}
	yolo := cmd.permissionOptions(internal.AutonomyYolo)
	if len(yolo) != 3 {
		t.Errorf("yolo options = %d", len(yolo))
	}
}

func TestGoalCommand_BareGoal_InteractiveCreate(t *testing.T) {
	// /goal (no args) → main input prompt; typing text creates the goal.
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	ctx := testContext()

	called := false
	ctx.RequestMainInput = func(prompt string, cb func(string)) {
		called = true
		if !strings.Contains(strings.ToLower(prompt), "goal") {
			t.Errorf("prompt should mention goal: %q", prompt)
		}
		cb("typed objective")
	}
	if err := cmd.Run(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("RequestMainInput not invoked for bare /goal")
	}
	if mode.GetGoal().Goal == nil || mode.GetGoal().Goal.Objective != "typed objective" {
		t.Errorf("goal not created from interactive input")
	}
}

func TestGoalCommand_BareGoal_InteractiveEmptyCancels(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	ctx := testContext()

	ctx.RequestMainInput = func(_ string, cb func(string)) {
		cb("   ") // whitespace-only aborts
	}
	if err := cmd.Run(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if mode.GetGoal().Goal != nil {
		t.Error("empty interactive input should not create a goal")
	}
}

func TestGoalCommand_FirstOrLast_FirstReplaces(t *testing.T) {
	// Create while a goal is active, choose "first" → replaces active goal.
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	ctx := testContext()

	cmd.Run(ctx, []string{"first goal"})

	ctx.SelectOptionFunc = func(_ string, _ []tui.SelectorItem, _ string, cb func(string, bool)) {
		cb("first", true)
	}
	if err := cmd.Run(ctx, []string{"new", "second goal"}); err != nil {
		t.Fatal(err)
	}
	if mode.GetGoal().Goal.Objective != "second goal" {
		t.Errorf("active goal = %q, want second goal", mode.GetGoal().Goal.Objective)
	}
}

func TestGoalCommand_FirstOrLast_ShowsActiveGoalDetails(t *testing.T) {
	// When a goal is already active, creating a new one must FIRST print the
	// active goal's details before asking where the new goal should go.
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	ctx := testContext()

	if err := cmd.Run(ctx, []string{"first goal"}); err != nil {
		t.Fatal(err)
	}
	active := mode.GetGoal().Goal

	ctx.OutputBuffer.Reset()
	var selectTitle string
	ctx.SelectOptionFunc = func(title string, _ []tui.SelectorItem, _ string, cb func(string, bool)) {
		selectTitle = title
		cb("cancel", true)
	}
	if err := cmd.Run(ctx, []string{"new", "second goal"}); err != nil {
		t.Fatal(err)
	}

	out := ctx.OutputBuffer.String()
	if !strings.Contains(out, active.Name) {
		t.Errorf("active goal details should include name %q: %s", active.Name, out)
	}
	if !strings.Contains(out, "first goal") {
		t.Errorf("active goal details should include objective: %s", out)
	}
	if !strings.Contains(out, "Active goal") {
		t.Errorf("details should be labelled 'Active goal': %s", out)
	}
	if !strings.Contains(selectTitle, "already active") {
		t.Errorf("picker title should mention active goal: %q", selectTitle)
	}
}

func TestGoalCommand_PromptsSayCtrlC(t *testing.T) {
	// The main-input prompt title must say 'ctrl-c', not 'empty to cancel'.
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))
	cmd := &GoalCommand{Mode: mode, Queue: queue}
	ctx := testContext()

	seen := map[string]string{}
	ctx.RequestMainInput = func(prompt string, cb func(string)) {
		seen[prompt] = prompt
		cb("") // cancel
	}

	_ = cmd.Run(ctx, nil)              // bare /goal → create prompt
	_ = cmd.Run(ctx, []string{"next"}) // queue prompt
	// Set up an active goal so replace-interactive is reachable.
	ctx.RequestMainInput = nil
	_ = cmd.Run(ctx, []string{"active"})
	ctx.RequestMainInput = func(prompt string, cb func(string)) {
		seen[prompt] = prompt
		cb("")
	}
	_ = cmd.Run(ctx, []string{"replace"}) // replace prompt

	for prompt := range seen {
		if strings.Contains(strings.ToLower(prompt), "empty to cancel") {
			t.Errorf("prompt should not say 'empty to cancel': %q", prompt)
		}
		if !strings.Contains(strings.ToLower(prompt), "ctrl-c") {
			t.Errorf("prompt should say 'ctrl-c': %q", prompt)
		}
	}
}

func TestGoalCommand_FirstOrLast_CancelDoesNothing(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	ctx := testContext()

	cmd.Run(ctx, []string{"first goal"})

	ctx.SelectOptionFunc = func(_ string, _ []tui.SelectorItem, _ string, cb func(string, bool)) {
		cb("cancel", true)
	}
	if err := cmd.Run(ctx, []string{"new", "second goal"}); err != nil {
		t.Fatal(err)
	}
	if mode.GetGoal().Goal.Objective != "first goal" {
		t.Errorf("active goal changed after cancel: %q", mode.GetGoal().Goal.Objective)
	}
}

func TestGoalCommand_NextInteractive_PrependsToQueue(t *testing.T) {
	// /goal:next (bare) → main input; the typed goal runs NEXT: inserted at
	// the FRONT of the queue, ahead of already-queued goals (default :first).
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))
	cmd := &GoalCommand{Mode: mode, Queue: queue}
	ctx := testContext()

	if _, err := queue.AppendGoal(goal.UpcomingGoalInput{Objective: "existing"}); err != nil {
		t.Fatal(err)
	}
	ctx.RequestMainInput = func(_ string, cb func(string)) {
		cb("queued objective")
	}
	if err := cmd.Run(ctx, []string{"next"}); err != nil {
		t.Fatal(err)
	}
	queued, _ := queue.Read()
	if len(queued) != 2 {
		t.Fatalf("queue = %v, want 2 goals", queued)
	}
	if queued[0].Objective != "queued objective" || queued[1].Objective != "existing" {
		t.Errorf("queue order = [%q %q], want [queued objective existing]",
			queued[0].Objective, queued[1].Objective)
	}
}

func TestGoalCommand_NextAdd_PrependsToQueue(t *testing.T) {
	// /goal:next:<text> queues the goal to run NEXT (the default :first):
	// FRONT of the queue, ahead of already-queued goals.
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))
	cmd := &GoalCommand{Mode: mode, Queue: queue}
	ctx := testContext()

	if _, err := queue.AppendGoal(goal.UpcomingGoalInput{Objective: "existing"}); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Run(ctx, []string{"next", "urgent fix"}); err != nil {
		t.Fatal(err)
	}
	queued, _ := queue.Read()
	if len(queued) != 2 || queued[0].Objective != "urgent fix" || queued[1].Objective != "existing" {
		t.Errorf("queue = %v, want [urgent fix existing]", queued)
	}
	if out := ctx.OutputBuffer.String(); !strings.Contains(out, "run next") {
		t.Errorf("output should say the goal runs next: %q", out)
	}
}

func TestGoalCommand_NextAdd_ExplicitFirstPrependsToQueue(t *testing.T) {
	// /goal:next:first:<text> is the explicit form of the default.
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))
	cmd := &GoalCommand{Mode: mode, Queue: queue}
	ctx := testContext()

	if _, err := queue.AppendGoal(goal.UpcomingGoalInput{Objective: "existing"}); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Run(ctx, []string{"next", "first", "urgent fix"}); err != nil {
		t.Fatal(err)
	}
	queued, _ := queue.Read()
	if len(queued) != 2 || queued[0].Objective != "urgent fix" || queued[1].Objective != "existing" {
		t.Errorf("queue = %v, want [urgent fix existing]", queued)
	}
}

func TestGoalCommand_NextAdd_LastAppendsToQueue(t *testing.T) {
	// /goal:next:last:<text> appends the goal at the END of the queue.
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))
	cmd := &GoalCommand{Mode: mode, Queue: queue}
	ctx := testContext()

	if _, err := queue.AppendGoal(goal.UpcomingGoalInput{Objective: "existing"}); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Run(ctx, []string{"next", "last", "tidy docs"}); err != nil {
		t.Fatal(err)
	}
	queued, _ := queue.Read()
	if len(queued) != 2 || queued[0].Objective != "existing" || queued[1].Objective != "tidy docs" {
		t.Errorf("queue = %v, want [existing tidy docs]", queued)
	}
}

// TestGoalCommand_NextAdd_ReuseShorthand is the end-to-end regression for
// bugs.md "/goal:next:reuse": the rfirst/rlast shorthand must queue a
// REUSE-context goal (FreshContext=false) at the requested placement — before
// the fix, /goal:next:rfirst:… silently queued a FRESH-context goal whose
// objective began with the literal word "rfirst".
func TestGoalCommand_NextAdd_ReuseShorthand(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))
	cmd := &GoalCommand{Mode: mode, Queue: queue}
	ctx := testContext()

	if _, err := queue.AppendGoal(goal.UpcomingGoalInput{Objective: "existing"}); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Run(ctx, []string{"next", "rfirst", "urgent fix"}); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Run(ctx, []string{"next", "rlast", "tidy docs"}); err != nil {
		t.Fatal(err)
	}
	queued, _ := queue.Read()
	if len(queued) != 3 {
		t.Fatalf("queue = %v, want 3 goals", queued)
	}
	// rfirst → front; rlast → end; both reuse (FreshContext=false) with the
	// token stripped from the objective.
	if queued[0].Objective != "urgent fix" || queued[0].FreshContext {
		t.Errorf("rfirst goal = %+v, want objective %q with FreshContext=false", queued[0], "urgent fix")
	}
	if queued[2].Objective != "tidy docs" || queued[2].FreshContext {
		t.Errorf("rlast goal = %+v, want objective %q with FreshContext=false", queued[2], "tidy docs")
	}
}

// TestGoalCommand_NextInteractive_ReuseCarriesContextMode covers the bare
// /goal:next:reuse form (no text): the interactive objective prompt must
// still honor the reuse token — before the fix the parsed context mode was
// dropped at dispatch and the typed goal silently got the configured default.
func TestGoalCommand_NextInteractive_ReuseCarriesContextMode(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))
	cmd := &GoalCommand{Mode: mode, Queue: queue}
	ctx := testContext()
	var submit func(string)
	ctx.RequestMainInput = func(_ string, onSubmit func(string)) { submit = onSubmit }

	if err := cmd.Run(ctx, []string{"next", "reuse"}); err != nil {
		t.Fatal(err)
	}
	if submit == nil {
		t.Fatal("expected the interactive objective prompt")
	}
	submit("typed objective")
	queued, _ := queue.Read()
	if len(queued) != 1 || queued[0].Objective != "typed objective" || queued[0].FreshContext {
		t.Errorf("queue = %+v, want the typed goal with FreshContext=false (reuse)", queued)
	}
}

func TestGoalCommand_FirstOrLast_AppendsToEnd(t *testing.T) {
	// The "Queue it for later" choice of the first-or-last prompt must keep
	// APPENDING to the end of the queue (it is not /goal:next).
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))
	cmd := &GoalCommand{Mode: mode, Queue: queue}
	ctx := testContext()

	if err := cmd.Run(ctx, []string{"active goal"}); err != nil {
		t.Fatal(err)
	}
	if _, err := queue.AppendGoal(goal.UpcomingGoalInput{Objective: "existing"}); err != nil {
		t.Fatal(err)
	}
	ctx.SelectOptionFunc = func(_ string, _ []tui.SelectorItem, _ string, cb func(string, bool)) {
		cb("last", true)
	}
	if err := cmd.Run(ctx, []string{"new", "second goal"}); err != nil {
		t.Fatal(err)
	}
	queued, _ := queue.Read()
	if len(queued) != 2 || queued[0].Objective != "existing" || queued[1].Objective != "second goal" {
		t.Errorf("queue = %v, want [existing second goal]", queued)
	}
}

func TestGoalCommand_GoalGetsFriendlyName(t *testing.T) {
	// Newly created goals get a non-empty friendly name in the snapshot.
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	ctx := testContext()

	if err := cmd.Run(ctx, []string{"fix tests"}); err != nil {
		t.Fatal(err)
	}
	g := mode.GetGoal().Goal
	if g == nil || !internal.SplitFriendlyName(g.Name) {
		t.Errorf("goal name not a friendly adjective.noun: %+v", g)
	}
	out := ctx.OutputBuffer.String()
	if !strings.Contains(out, g.Name) {
		t.Errorf("started output should include friendly name %q: %s", g.Name, out)
	}
}

func TestGoalCommand_QueueGoalsGetUniqueFriendlyNames(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))
	mode.SetNamePool(queue) // active-goal generator should avoid queue names
	cmd := &GoalCommand{Mode: mode, Queue: queue}
	ctx := testContext()

	for i := 0; i < 5; i++ {
		if err := cmd.Run(ctx, []string{"next", fmt.Sprintf("obj-%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	queued, _ := queue.Read()
	seen := make(map[string]bool, len(queued))
	for _, g := range queued {
		if !internal.SplitFriendlyName(g.Name) {
			t.Errorf("queued goal name not friendly: %q", g.Name)
		}
		if seen[g.Name] {
			t.Errorf("duplicate queued name: %q", g.Name)
		}
		seen[g.Name] = true
	}
}
