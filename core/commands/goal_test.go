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
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/tui"
)

func TestGoalCommand_parseArgs(t *testing.T) {
	cmd := &GoalCommand{}
	cases := []struct {
		args []string
		want string
	}{
		// Bare /goal → interactive create flow (item 2)
		{args: []string{}, want: "create-interactive"},
		// Colon subcommands
		{args: []string{"status"}, want: "status"},
		{args: []string{"pause"}, want: "pause"},
		{args: []string{"resume"}, want: "resume"},
		{args: []string{"cancel"}, want: "cancel"},
		{args: []string{"manage"}, want: "manage"},
		// /goal:new — bare and with text
		{args: []string{"new"}, want: "create-interactive"},
		{args: []string{"new", "fix tests"}, want: "create"},
		// /goal:next — bare and with text
		{args: []string{"next"}, want: "next-interactive"},
		{args: []string{"next", "fix tests"}, want: "next-add"},
		// /goal:replace — bare and with text
		{args: []string{"replace"}, want: "replace-interactive"},
		{args: []string{"replace", "new goal"}, want: "replace"},
		// /goal:reorder
		{args: []string{"reorder", "1B,2A"}, want: "reorder"},
		{args: []string{"reorder"}, want: "error"},
		// Free text with no keyword → create
		{args: []string{"just a goal"}, want: "create"},
	}
	for _, tc := range cases {
		got := cmd.parseArgs(tc.args)
		if got.kind != tc.want {
			t.Errorf("parseArgs(%v).kind = %q, want %q", tc.args, got.kind, tc.want)
		}
	}
}

func TestGoalCommand_parseArgs_EscapesReservedWords(t *testing.T) {
	// With colon nomenclature, reserved words live cleanly in the text arg:
	// /goal:new:pause the server → args=["new","pause the server"]
	cmd := &GoalCommand{}
	got := cmd.parseArgs([]string{"new", "pause the server"})
	if got.kind != "create" || got.objective != "pause the server" {
		t.Errorf("unexpected parse: %+v", got)
	}
}

func TestGoalCommand_CreateGoal(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	ctx := testContext()

	err := cmd.Run(ctx, []string{"fix tests"})
	if err != nil {
		t.Fatal(err)
	}
	if mode.GetGoal().Goal == nil {
		t.Fatal("goal should be created")
	}
	if mode.GetGoal().Goal.Objective != "fix tests" {
		t.Errorf("objective = %q", mode.GetGoal().Goal.Objective)
	}
}

func TestGoalCommand_CreateGoal_Duplicate_FirstOrLastPrompt(t *testing.T) {
	// With a goal already active, a second create no longer errors; it opens
	// the item-4 "first or last" selector. Choosing "last" queues it.
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))
	cmd := &GoalCommand{Mode: mode, Queue: queue}
	ctx := testContext()

	if err := cmd.Run(ctx, []string{"first"}); err != nil {
		t.Fatal(err)
	}

	ctx.SelectOptionFunc = func(_ string, _ []tui.SelectorItem, _ string, cb func(string, bool)) {
		cb("last", true) // queue it, keep active goal
	}
	if err := cmd.Run(ctx, []string{"second"}); err != nil {
		t.Fatal(err)
	}
	if mode.GetGoal().Goal.Objective != "first" {
		t.Errorf("active goal changed unexpectedly: %q", mode.GetGoal().Goal.Objective)
	}
	queued, _ := queue.Read()
	if len(queued) != 1 || queued[0].Objective != "second" {
		t.Errorf("expected second to be queued, got %v", queued)
	}
}

func TestGoalCommand_PauseResume(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	ctx := testContext()

	cmd.Run(ctx, []string{"fix tests"})
	if err := cmd.Run(ctx, []string{"pause"}); err != nil {
		t.Fatal(err)
	}
	if mode.GetGoal().Goal.Status != goal.GoalPaused {
		t.Errorf("status = %q", mode.GetGoal().Goal.Status)
	}
	if err := cmd.Run(ctx, []string{"resume"}); err != nil {
		t.Fatal(err)
	}
	if mode.GetGoal().Goal.Status != goal.GoalActive {
		t.Errorf("status = %q", mode.GetGoal().Goal.Status)
	}
}

func TestGoalCommand_Cancel(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	ctx := testContext()

	cmd.Run(ctx, []string{"fix tests"})
	if err := cmd.Run(ctx, []string{"cancel"}); err != nil {
		t.Fatal(err)
	}
	if mode.GetGoal().Goal != nil {
		t.Error("goal should be nil after cancel")
	}
}

func TestGoalCommand_Replace(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	ctx := testContext()

	cmd.Run(ctx, []string{"first"})

	// /goal:replace:second now asks for confirmation first.
	ctx.SelectOptionFunc = func(_ string, _ []tui.SelectorItem, _ string, cb func(string, bool)) {
		cb("replace", true)
	}
	if err := cmd.Run(ctx, []string{"replace", "second"}); err != nil {
		t.Fatal(err)
	}
	if mode.GetGoal().Goal.Objective != "second" {
		t.Errorf("objective = %q", mode.GetGoal().Goal.Objective)
	}
}

func TestGoalCommand_Replace_CancelKeepsOriginal(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	ctx := testContext()

	cmd.Run(ctx, []string{"first"})
	ctx.SelectOptionFunc = func(_ string, _ []tui.SelectorItem, _ string, cb func(string, bool)) {
		cb("cancel", true)
	}
	if err := cmd.Run(ctx, []string{"replace", "second"}); err != nil {
		t.Fatal(err)
	}
	if mode.GetGoal().Goal.Objective != "first" {
		t.Errorf("objective = %q, want first", mode.GetGoal().Goal.Objective)
	}
}

func TestGoalCommand_Replace_NoActiveGoal(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	ctx := testContext()

	err := cmd.Run(ctx, []string{"replace", "second"})
	if err == nil {
		t.Fatal("expected error when no active goal to replace")
	}
}

func TestGoalCommand_Status(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	ctx := testContext()

	cmd.Run(ctx, []string{"fix tests"})
	if err := cmd.Run(ctx, []string{"status"}); err != nil {
		t.Fatal(err)
	}
	out := ctx.OutputBuffer.String()
	if !strings.Contains(out, "fix tests") {
		t.Errorf("status output missing goal: %s", out)
	}
}

func TestGoalCommand_StatusEmpty(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	ctx := testContext()

	if err := cmd.Run(ctx, []string{"status"}); err != nil {
		t.Fatal(err)
	}
	out := ctx.OutputBuffer.String()
	if !strings.Contains(out, "No current goal") {
		t.Errorf("unexpected status output: %s", out)
	}
}

func TestGoalCommand_NextAndReorder(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "queue.json"))
	cmd := &GoalCommand{Mode: mode, Queue: queue}
	ctx := testContext()

	if err := cmd.Run(ctx, []string{"next", "first"}); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Run(ctx, []string{"next", "second"}); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Run(ctx, []string{"next", "third"}); err != nil {
		t.Fatal(err)
	}

	if err := cmd.Run(ctx, []string{"reorder", "1C,2A,3B"}); err != nil {
		t.Fatal(err)
	}
	goals, err := queue.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(goals) != 3 || goals[0].Objective != "third" {
		t.Errorf("reorder failed: %v", goals)
	}
}

func TestGoalCommand_ManageEmpty(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "queue.json"))
	cmd := &GoalCommand{Mode: mode, Queue: queue}
	ctx := testContext()

	if err := cmd.Run(ctx, []string{"manage"}); err != nil {
		t.Fatal(err)
	}
	out := ctx.OutputBuffer.String()
	if !strings.Contains(out, "No queued goals") {
		t.Errorf("unexpected output: %s", out)
	}
}

func TestGoalCommand_StartGoalPrompt(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore("")
	switcher := &testAutonomySwitcher{level: internal.AutonomyConfirm}
	cmd := &GoalCommand{Mode: mode, Queue: queue, AutonomySwitcher: switcher}
	ctx := testContext()

	called := false
	ctx.SelectOptionFunc = func(title string, items []tui.SelectorItem, initial string, cb func(string, bool)) {
		called = true
		cb("auto", true)
	}

	if err := cmd.Run(ctx, []string{"fix tests"}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("start permission dialog not shown")
	}
	if switcher.level != internal.AutonomyConfirm {
		t.Errorf("autonomy = %q", switcher.level)
	}
	if mode.GetGoal().Goal == nil {
		t.Error("goal should be created after approval")
	}
}

func TestGoalCommand_StartGoalYolo(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore("")
	switcher := &testAutonomySwitcher{level: internal.AutonomyYolo}
	cmd := &GoalCommand{Mode: mode, Queue: queue, AutonomySwitcher: switcher}
	ctx := testContext()

	called := false
	ctx.SelectOptionFunc = func(string, []tui.SelectorItem, string, func(string, bool)) {
		called = true
	}

	if err := cmd.Run(ctx, []string{"fix tests"}); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Error("dialog should not be shown in yolo")
	}
	if mode.GetGoal().Goal == nil {
		t.Error("goal should be created in yolo")
	}
}

func TestGoalCommand_StartGoalCancel(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore("")
	switcher := &testAutonomySwitcher{level: internal.AutonomyConfirm}
	cmd := &GoalCommand{Mode: mode, Queue: queue, AutonomySwitcher: switcher}
	ctx := testContext()

	flashed := make(chan string, 1)
	ctx.EventBus = event.MakeBus(1, 1, 1, 1)
	go func() {
		for ev := range ctx.EventBus.Chat {
			if ev.Flash != nil {
				flashed <- ev.Flash.Text
			}
		}
	}()
	ctx.SelectOptionFunc = func(_ string, _ []tui.SelectorItem, _ string, cb func(string, bool)) {
		cb("cancel", true)
	}

	if err := cmd.Run(ctx, []string{"fix tests"}); err != nil {
		t.Fatal(err)
	}
	if mode.GetGoal().Goal != nil {
		t.Error("goal should not be created after cancel")
	}
	select {
	case text := <-flashed:
		if text != "Goal start cancelled." {
			t.Errorf("flash = %q", text)
		}
	case <-time.After(time.Second):
		t.Error("expected flash")
	}
}

func TestGoalCommand_QueueManagerManage(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "queue.json"))
	cmd := &GoalCommand{Mode: mode, Queue: queue}
	ctx := testContext()

	cmd.Run(ctx, []string{"next", "first"})
	cmd.Run(ctx, []string{"next", "second"})

	var capturedItems []tui.SelectorItem
	ctx.SelectOptionFunc = func(title string, items []tui.SelectorItem, initial string, cb func(string, bool)) {
		capturedItems = items
		cb("__done__", true)
	}

	if err := cmd.Run(ctx, []string{"manage"}); err != nil {
		t.Fatal(err)
	}
	if len(capturedItems) != 3 {
		t.Errorf("items = %d", len(capturedItems))
	}
}

func TestGoalCommand_QueueManagerMoveAndDelete(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "queue.json"))
	cmd := &GoalCommand{Mode: mode, Queue: queue}
	ctx := testContext()

	cmd.Run(ctx, []string{"next", "first"})
	cmd.Run(ctx, []string{"next", "second"})

	var capturedID string
	var actionCB func(string, bool)
	ctx.SelectOptionFunc = func(title string, items []tui.SelectorItem, initial string, cb func(string, bool)) {
		if title == "Manage queued goals" {
			capturedID = items[0].Value
			cb(items[0].Value, true)
		} else if title == "Queue action" {
			actionCB = cb
		}
	}

	cmd.Run(ctx, []string{"manage"})
	actionCB("up", true)

	goals, _ := queue.Read()
	if len(goals) != 2 || goals[0].Objective != "first" {
		t.Errorf("after up: %v", goals)
	}

	cmd.Run(ctx, []string{"manage"})
	actionCB("delete", true)

	goals, _ = queue.Read()
	if len(goals) != 1 {
		t.Errorf("after delete: %d goals", len(goals))
	}
	_ = capturedID
}

func testContext() core.Context {
	return core.Context{
		OutputBuffer:     &strings.Builder{},
		SelectOptionFunc: func(string, []tui.SelectorItem, string, func(string, bool)) {},
		ShowInputFunc:    func(string, string, func(string, bool)) {},
		RequestMainInput: func(string, func(string)) {},
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

// --- Colon nomenclature + interactive flow regression tests ---

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

func TestGoalCommand_NextInteractive_Appends(t *testing.T) {
	// /goal:next (bare) → main input; typed text is queued.
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))
	cmd := &GoalCommand{Mode: mode, Queue: queue}
	ctx := testContext()

	ctx.RequestMainInput = func(_ string, cb func(string)) {
		cb("queued objective")
	}
	if err := cmd.Run(ctx, []string{"next"}); err != nil {
		t.Fatal(err)
	}
	queued, _ := queue.Read()
	if len(queued) != 1 || queued[0].Objective != "queued objective" {
		t.Errorf("queue = %v", queued)
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

func TestGoalCommand_StatusShowsName(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	ctx := testContext()

	cmd.Run(ctx, []string{"fix tests"})
	if err := cmd.Run(ctx, []string{"status"}); err != nil {
		t.Fatal(err)
	}
	out := ctx.OutputBuffer.String()
	g := mode.GetGoal().Goal
	if !strings.Contains(out, "["+g.Name+"]") {
		t.Errorf("status should show [name]: %s", out)
	}
}

func TestGoalCommand_CompleteArgs(t *testing.T) {
	cmd := &GoalCommand{}
	ctx := testContext()

	// Empty prefix → all subcommands.
	all := cmd.CompleteArgs(ctx, "")
	if len(all) < 9 {
		t.Errorf("expected at least 9 completions, got %d", len(all))
	}

	// Prefix filter.
	got := cmd.CompleteArgs(ctx, "re")
	values := make(map[string]bool)
	for _, c := range got {
		values[c.Value] = true
	}
	if !values["replace"] || !values["reorder"] || !values["resume"] {
		t.Errorf("prefix 're' should match replace/reorder/resume, got %v", values)
	}
	if values["new"] {
		t.Error("'new' should not match prefix 're'")
	}
}

func TestGoalCommand_ReplaceInteractive_AsksThenConfirms(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	ctx := testContext()

	cmd.Run(ctx, []string{"first"})

	step := 0
	ctx.RequestMainInput = func(_ string, cb func(string)) {
		step = 1
		cb("new objective")
	}
	ctx.SelectOptionFunc = func(_ string, _ []tui.SelectorItem, _ string, cb func(string, bool)) {
		if step != 1 {
			t.Fatal("confirm dialog appeared before objective was entered")
		}
		cb("replace", true)
	}
	if err := cmd.Run(ctx, []string{"replace"}); err != nil {
		t.Fatal(err)
	}
	if mode.GetGoal().Goal.Objective != "new objective" {
		t.Errorf("objective = %q", mode.GetGoal().Goal.Objective)
	}
}

func TestGoalCommand_ReplaceInteractive_NoActiveGoal(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	ctx := testContext()

	if err := cmd.Run(ctx, []string{"replace"}); err == nil {
		t.Fatal("expected error when no active goal")
	}
}
