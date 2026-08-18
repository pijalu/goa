// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/tui"
)

func TestGoalCommand_ManageReorderKeyedRealSelector(t *testing.T) {
	cases := []struct {
		name      string
		key       string // "+" or "-"
		selectIdx int    // which queued goal to highlight (0-based)
		wantOrder []int  // expected queue order as indices into original ids
	}{
		{"plus moves second up", "+", 1, []int{1, 0, 2}},
		{"minus moves first down", "-", 0, []int{1, 0, 2}},
		{"plus on first is a no-op", "+", 0, []int{0, 1, 2}},
		{"minus on last is a no-op", "-", 2, []int{0, 1, 2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, queue := newManagerCommand(t, "")
			ids := appendQueued(t, queue, "g0", "g1", "g2")

			ctx := testContext()
			opens := 0
			// Mimic the production host (internal/app wireInteractiveCallbacks):
			// build a real selector, apply the keyed bindings, then drive it.
			ctx.SelectOptionKeyedFunc = func(_ string, items []tui.SelectorItem, current string, keys tui.SelectorKeymap, cb func(string, bool)) {
				opens++
				if opens > 1 {
					cb("", false) // close the reopened manager
					return
				}
				result := make(chan string, 1)
				sel := tui.NewSelector("Goal manager — execution order", items, current, result)
				sel.SetKeymap(keys)
				if !keys.ReorderMode {
					t.Error("manager must open the selector in ReorderMode")
				}
				// Highlight the target queued goal. Manager rows are:
				// [__add_first__, (no active), g0, g1, g2, __add_last__, __done__].
				// Row 0 is the add-at-start sentinel, so goal selectIdx sits at row
				// selectIdx+1 → press Down selectIdx+1 times. The TUI decodes
				// terminal bytes into named keys (tui.KeyDown) and passes printable
				// runes ("+"/"-") through unchanged.
				for i := 0; i <= tc.selectIdx; i++ {
					sel.HandleInput(tui.KeyDown)
				}
				sel.HandleInput(tc.key) // the real '+'/'-' printable key
				select {
				case v := <-result:
					cb(v, v != "")
				default:
					t.Errorf("'%s' on a goal row produced no emit — reorder hotkey not firing", tc.key)
					cb("", false)
				}
			}

			if err := cmd.showQueueManager(ctx); err != nil {
				t.Fatal(err)
			}
			assertQueueIDs(t, queue, ids, tc.wantOrder)
		})
	}
}

func TestGoalCommand_ManageActiveRowRejected(t *testing.T) {
	cases := []struct {
		name string
		emit string
	}{
		{"move up on active", "__moveup____active__"},
		{"move down on active", "__movedown____active__"},
		{"delete on active", "__delete____active__"},
		{"enter on active", "__active__"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runManageActiveRowCase(t, tc.emit)
		})
	}
}

func runManageActiveRowCase(t *testing.T, emit string) {
	t.Helper()
	cmd, queue := newManagerCommand(t, "running")
	appendQueued(t, queue, "queued one")

	ctx := testContext()
	getFlashes := collectFlashes(&ctx)
	opens := 0
	var cursor string
	ctx.SelectOptionFunc = func(_ string, _ []tui.SelectorItem, current string, cb func(string, bool)) {
		opens++
		cursor = current
		if opens == 1 {
			cb(emit, true)
			return
		}
		cb("", false)
	}

	if err := cmd.showQueueManager(ctx); err != nil {
		t.Fatal(err)
	}
	assertQueueObjectives(t, queue, []string{"queued one"})
	if g := cmd.Mode.GetGoal().Goal; g == nil || g.Objective != "running" {
		t.Errorf("active goal must still be running, got %+v", g)
	}
	expectManagerCursor(t, opens, 2, cursor, "__active__")
	flashes := getFlashes()
	if len(flashes) == 0 || !strings.Contains(flashes[0], "active goal") {
		t.Errorf("expected a rejection flash mentioning the active goal, got %v", flashes)
	}
}

func TestGoalCommand_ManageAddRows(t *testing.T) {
	cases := []manageAddRowCase{
		{"add at start with active goal", "__add_first__", true,
			[]string{"new goal", "queued one"}, "running", "run next"},
		{"add at end with active goal", "__add_last__", true,
			[]string{"queued one", "new goal"}, "running", "end of the queue"},
		{"add at start with no active goal", "__add_first__", false,
			[]string{"queued one"}, "new goal", "Set new goal objective"},
		{"add at end with no active goal", "__add_last__", false,
			[]string{"queued one"}, "new goal", "Set new goal objective"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runManageAddRowCase(t, tc)
		})
	}
}

// manageAddRowCase describes one add-row scenario: Enter on the sentinel row
// must open the create-goal flow (prompt contains wantPromptSub), and after
// submitting "new goal" the queue holds wantQueue and the active goal's
// objective is wantActive.
type manageAddRowCase struct {
	name          string
	sentinel      string
	active        bool
	wantQueue     []string
	wantActive    string
	wantPromptSub string
}

func runManageAddRowCase(t *testing.T, tc manageAddRowCase) {
	t.Helper()
	activeObjective := ""
	if tc.active {
		activeObjective = "running"
	}
	cmd, queue := newManagerCommand(t, activeObjective)
	appendQueued(t, queue, "queued one")

	ctx := testContext()
	var prompt string
	ctx.RequestMainInput = func(p string, onSubmit func(string)) {
		prompt = p
		onSubmit("new goal")
	}
	ctx.SelectOptionFunc = func(_ string, _ []tui.SelectorItem, _ string, cb func(string, bool)) {
		cb(tc.sentinel, true) // Enter on the add row
	}

	if err := cmd.showQueueManager(ctx); err != nil {
		t.Fatal(err)
	}
	if prompt == "" {
		t.Fatal("the add row did not open the create-goal flow")
	}
	if !strings.Contains(prompt, tc.wantPromptSub) {
		t.Errorf("create prompt = %q, want substring %q", prompt, tc.wantPromptSub)
	}
	assertQueueObjectives(t, queue, tc.wantQueue)
	g := cmd.Mode.GetGoal().Goal
	if g == nil || g.Objective != tc.wantActive {
		t.Errorf("active goal = %+v, want objective %q", g, tc.wantActive)
	}
}

func TestGoalCommand_ManageGenericAddEmit(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	if _, err := mode.CreateGoal(goal.CreateGoalInput{Objective: "running"}, goal.GoalActorUser); err != nil {
		t.Fatal(err)
	}
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))
	cmd := &GoalCommand{Mode: mode, Queue: queue}

	ctx := testContext()
	getFlashes := collectFlashes(&ctx)
	ctx.RequestMainInput = func(_ string, onSubmit func(string)) {
		onSubmit("added goal")
	}
	ctx.SelectOptionFunc = func(_ string, _ []tui.SelectorItem, _ string, cb func(string, bool)) {
		cb("__add__", true)
	}

	if err := cmd.showQueueManager(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := queue.Read()
	if len(got) != 1 || got[0].Objective != "added goal" {
		t.Errorf("queue = %+v, want the added goal queued behind the active one", got)
	}
	for _, f := range getFlashes() {
		if strings.Contains(f, "not found") {
			t.Errorf("regression: __add__ leaked into a goal lookup: %q", f)
		}
	}
}

func TestGoalCommand_ManageEnterOnGoalRow(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))
	cmd := &GoalCommand{Mode: mode, Queue: queue}
	goals, err := queue.Append("some goal")
	if err != nil {
		t.Fatal(err)
	}
	id := goals[0].ID

	ctx := testContext()
	getFlashes := collectFlashes(&ctx)
	opens := 0
	var cursor string
	ctx.SelectOptionFunc = func(_ string, _ []tui.SelectorItem, current string, cb func(string, bool)) {
		opens++
		cursor = current
		if opens == 1 {
			cb(id, true) // Enter on the goal row
			return
		}
		cb("", false)
	}

	if err := cmd.showQueueManager(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := queue.Read()
	if len(got) != 1 {
		t.Errorf("queue must be unchanged, got %+v", got)
	}
	if opens != 2 || cursor != id {
		t.Errorf("manager must reopen on the same row (opens=%d, cursor=%q)", opens, cursor)
	}
	flashes := getFlashes()
	if len(flashes) == 0 || !strings.Contains(flashes[0], "move up") {
		t.Errorf("expected a hotkey cheat-sheet flash, got %v", flashes)
	}
}

func TestGoalCommand_ParseContextToken(t *testing.T) {
	cmd := &GoalCommand{}
	cases := []struct {
		args        []string
		kind        string
		objective   string
		contextMode string
	}{
		{[]string{"new", "fresh fix the bug"}, "create", "fix the bug", "fresh"},
		{[]string{"new", "fresh"}, "create-interactive", "", "fresh"},
		{[]string{"new", "reuse investigate"}, "create", "investigate", "reuse"},
		{[]string{"new", "fix the bug"}, "create", "fix the bug", ""},
		{[]string{"next", "fresh audit"}, "next-add", "audit", "fresh"},
		{[]string{"next", "reuse audit"}, "next-add", "audit", "reuse"},
		{[]string{"next", "audit"}, "next-add", "audit", ""},
	}
	for _, tc := range cases {
		got := cmd.parseArgs(tc.args)
		if got.kind != tc.kind || got.objective != tc.objective || got.contextMode != tc.contextMode {
			t.Errorf("parseArgs(%v) = {kind:%q objective:%q contextMode:%q}, want {%q %q %q}",
				tc.args, got.kind, got.objective, got.contextMode, tc.kind, tc.objective, tc.contextMode)
		}
	}
}

func TestGoalCommand_Create_DefaultFreshOn(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	if err := cmd.Run(testContext(), []string{"new", "fix tests"}); err != nil {
		t.Fatal(err)
	}
	if g := mode.GetGoal().Goal; g == nil || !g.FreshContext {
		t.Errorf("expected FreshContext=true by default, got %+v", g)
	}
}

func TestGoalCommand_Create_ReuseToken(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	if err := cmd.Run(testContext(), []string{"new", "reuse", "fix tests"}); err != nil {
		t.Fatal(err)
	}
	if g := mode.GetGoal().Goal; g == nil || g.FreshContext {
		t.Errorf("expected FreshContext=false for /goal:new:reuse, got %+v", g)
	}
}

func TestGoalCommand_Create_ConfigDefault(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	cmd.FreshContextDefault = func() bool { return false } // config: reuse

	if err := cmd.Run(testContext(), []string{"new", "first objective"}); err != nil {
		t.Fatal(err)
	}
	if g := mode.GetGoal().Goal; g == nil || g.FreshContext {
		t.Errorf("expected FreshContext=false from config default, got %+v", g)
	}

	// The fresh token overrides the config-off default. A goal is already
	// active, so the first-or-last prompt fires; choose "first" (replace).
	ctx := testContext()
	ctx.SelectOptionFunc = func(_ string, _ []tui.SelectorItem, _ string, cb func(string, bool)) {
		cb("first", true)
	}
	if err := cmd.Run(ctx, []string{"new", "fresh", "second objective"}); err != nil {
		t.Fatal(err)
	}
	if g := mode.GetGoal().Goal; g == nil || !g.FreshContext {
		t.Errorf("expected FreshContext=true from the fresh token over config default, got %+v", g)
	}
}

func TestGoalCommand_QueueNext_FreshContextStored(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))
	cmd := &GoalCommand{Mode: mode, Queue: queue}
	ctx := testContext()

	if err := cmd.Run(ctx, []string{"next", "fresh", "audit retry logic"}); err != nil {
		t.Fatal(err)
	}
	queued, err := queue.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 1 || !queued[0].FreshContext {
		t.Errorf("expected queued goal FreshContext=true, got %+v", queued)
	}
}

func TestGoalCommand_Current(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	ctx := testContext()

	// No goal → clear message.
	if err := cmd.Run(ctx, []string{"current"}); err != nil {
		t.Fatal(err)
	}
	if got := ctx.OutputBuffer.String(); !strings.Contains(got, "No current goal") {
		t.Fatalf("/goal:current with no goal = %q", got)
	}
	ctx.OutputBuffer.Reset()

	// Active goal with todos, criterion and verify command.
	criterion := "tests pass"
	verify := "go test ./..."
	if _, err := mode.CreateGoal(goal.CreateGoalInput{
		Objective: "fix the parser", Name: "calm.fox",
		CompletionCriterion: &criterion, VerifyCommand: &verify,
	}, goal.GoalActorUser); err != nil {
		t.Fatal(err)
	}
	if _, err := mode.AddGoalTodo("write failing test", goal.GoalActorUser); err != nil {
		t.Fatal(err)
	}
	if _, err := mode.AddGoalTodo("make it pass", goal.GoalActorUser); err != nil {
		t.Fatal(err)
	}

	if err := cmd.Run(ctx, []string{"current"}); err != nil {
		t.Fatal(err)
	}
	out := ctx.OutputBuffer.String()
	for _, want := range []string{
		"calm.fox", "fix the parser", "active",
		"tests pass", "go test ./...",
		"write failing test", "make it pass", "[ ]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("/goal:current output missing %q:\n%s", want, out)
		}
	}
}
