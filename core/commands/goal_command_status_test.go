// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/tui"
)

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

func TestGoalCommand_Log(t *testing.T) {
	store := goal.NewFileEventStore(filepath.Join(t.TempDir(), "goal-events.jsonl"))
	mode := goal.NewGoalMode(store, nil, nil, nil)
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	ctx := testContext()

	if _, err := mode.CreateGoal(goal.CreateGoalInput{Objective: "fix tests"}, goal.GoalActorUser); err != nil {
		t.Fatal(err)
	}
	reason := "paused by user"
	if _, err := mode.PauseGoal(goal.GoalReasonInput{Reason: &reason}, goal.GoalActorUser); err != nil {
		t.Fatal(err)
	}

	if err := cmd.Run(ctx, []string{"log"}); err != nil {
		t.Fatal(err)
	}
	out := ctx.OutputBuffer.String()
	for _, want := range []string{"goal.create", "goal.update", "status=paused", "reason=paused by user"} {
		if !strings.Contains(out, want) {
			t.Errorf("log output missing %q:\n%s", want, out)
		}
	}
}

func TestGoalCommand_Log_Empty(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	ctx := testContext()
	if err := cmd.Run(ctx, []string{"log"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ctx.OutputBuffer.String(), "No goal events recorded.") {
		t.Errorf("unexpected output: %q", ctx.OutputBuffer.String())
	}
}

func TestGoalCommand_Verify(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	mode.SetVerifier(&fakeCommandVerifier{output: "all green\n", ok: true}, true)
	verifyCmd := "true"
	if _, err := mode.CreateGoal(goal.CreateGoalInput{Objective: "fix tests", VerifyCommand: &verifyCmd}, goal.GoalActorUser); err != nil {
		t.Fatal(err)
	}
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	ctx := testContext()

	if err := cmd.Run(ctx, []string{"verify"}); err != nil {
		t.Fatal(err)
	}
	out := ctx.OutputBuffer.String()
	if !strings.Contains(out, "all green") || !strings.Contains(out, "PASS") {
		t.Errorf("unexpected verify output: %q", out)
	}
}

func TestGoalCommand_Verify_Fail(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	mode.SetVerifier(&fakeCommandVerifier{output: "boom\n", ok: false}, true)
	verifyCmd := "false"
	if _, err := mode.CreateGoal(goal.CreateGoalInput{Objective: "fix tests", VerifyCommand: &verifyCmd}, goal.GoalActorUser); err != nil {
		t.Fatal(err)
	}
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	ctx := testContext()

	if err := cmd.Run(ctx, []string{"verify"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ctx.OutputBuffer.String(), "FAIL") {
		t.Errorf("expected FAIL in output: %q", ctx.OutputBuffer.String())
	}
}

func TestGoalCommand_Verify_NoCommand(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	if _, err := mode.CreateGoal(goal.CreateGoalInput{Objective: "fix tests"}, goal.GoalActorUser); err != nil {
		t.Fatal(err)
	}
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	ctx := testContext()
	if err := cmd.Run(ctx, []string{"verify"}); err == nil {
		t.Fatal("expected error when no verify command recorded")
	}
}

type fakeCommandVerifier struct {
	output string
	ok     bool
}

func (v *fakeCommandVerifier) Verify(_ context.Context, _ string) goal.VerifyOutcome {
	return goal.VerifyOutcome{Output: v.output, OK: v.ok, DurationMs: 5, TimeoutMs: 120000}
}

// /goal:list fixture objectives shared by setup and shape assertions.
const (
	listActiveObj = "fix the newline loss in the converter with a complete multi-line objective that must not be truncated"
	listSecondObj = "second goal: audit provider retry logic"
	listThirdObj  = "third goal: add json.load to gpython"
)

func TestGoalCommand_List(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))
	cmd := &GoalCommand{Mode: mode, Queue: queue}
	ctx := testContext()

	// Active goal via create, then two queued goals in order.
	if err := cmd.Run(ctx, []string{listActiveObj}); err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Append(listSecondObj); err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Append(listThirdObj); err != nil {
		t.Fatal(err)
	}

	if err := cmd.Run(ctx, []string{"list"}); err != nil {
		t.Fatal(err)
	}
	assertGoalListShape(t, ctx.OutputBuffer.String())
}

// assertGoalListShape verifies the /goal:list markdown: header present,
// active goal before the queued ones in queue order, numbered statuses, and
// complete (untruncated) objectives.
func assertGoalListShape(t *testing.T, out string) {
	t.Helper()
	if !strings.Contains(out, "## Goals") {
		t.Errorf("expected markdown header in output:\n%s", out)
	}
	i1 := strings.Index(out, listActiveObj)
	i2 := strings.Index(out, listSecondObj)
	i3 := strings.Index(out, listThirdObj)
	if i1 < 0 || i2 < 0 || i3 < 0 {
		t.Fatalf("missing objectives in output:\n%s", out)
	}
	if !(i1 < i2 && i2 < i3) {
		t.Errorf("goals out of order (active=%d, second=%d, third=%d):\n%s", i1, i2, i3, out)
	}
	for _, want := range []string{"**1. [active]", "**2. [queued]", "**3. [queued]", "status active"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output:\n%s", want, out)
		}
	}
	// The long active objective must appear in full — no truncation marker.
	if strings.Contains(out, "must not be tru...") || !strings.Contains(out, "must not be truncated") {
		t.Errorf("active objective was truncated:\n%s", out)
	}
}

func TestGoalCommand_ListShowsAllInfo(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))
	cmd := &GoalCommand{Mode: mode, Queue: queue}
	ctx := testContext()

	// Current goal: reuse context, criterion, verify command, handover,
	// turn budget and a todo.
	criterion := "all tests pass"
	verify := "go test ./..."
	handover := "prior goal evidence"
	if _, err := mode.CreateGoal(goal.CreateGoalInput{
		Objective:           "current goal with full metadata",
		CompletionCriterion: &criterion,
		VerifyCommand:       &verify,
		FreshContext:        false,
		Handoff:             &handover,
	}, goal.GoalActorUser); err != nil {
		t.Fatal(err)
	}
	if _, err := mode.AddGoalTodo("write the tests", goal.GoalActorUser); err != nil {
		t.Fatal(err)
	}
	turnBudget := 7
	if _, err := mode.SetBudgetLimits(goal.GoalBudgetLimits{TurnBudget: &turnBudget}, goal.GoalActorUser); err != nil {
		t.Fatal(err)
	}

	// Queued goal: fresh context, criterion and verify command.
	queuedCriterion := "docs build without warnings"
	queuedVerify := "go run ./cmd/goa --help"
	if _, err := queue.AppendGoal(goal.UpcomingGoalInput{
		Objective:           "queued goal with full metadata",
		CompletionCriterion: &queuedCriterion,
		VerifyCommand:       &queuedVerify,
		FreshContext:        true,
	}); err != nil {
		t.Fatal(err)
	}

	if err := cmd.Run(ctx, []string{"list"}); err != nil {
		t.Fatal(err)
	}
	out := ctx.OutputBuffer.String()

	for _, want := range []string{
		"context reuse",                                       // current goal context run type
		"context fresh",                                       // queued goal context run type
		"- Completion criterion: all tests pass",              // current criterion
		"- Verify command: `go test ./...`",                   // current verify command
		"- Handover: attached",                                // current handover marker
		"budget turns 0/7",                                    // current budget used/limit
		"Todos (0/1 done):",                                   // current todos block
		"- [ ] write the tests",                               // current todo item
		"- Completion criterion: docs build without warnings", // queued criterion
		"- Verify command: `go run ./cmd/goa --help`",         // queued verify command
		"queued ", // queued timestamp label
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in list output:\n%s", want, out)
		}
	}

	// Paused goal: terminal state (status + reason) must render too.
	ctx.OutputBuffer.Reset()
	reason := "paused for review"
	if _, err := mode.PauseGoal(goal.GoalReasonInput{Reason: &reason}, goal.GoalActorUser); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Run(ctx, []string{"list"}); err != nil {
		t.Fatal(err)
	}
	out = ctx.OutputBuffer.String()
	for _, want := range []string{"status paused", "- Reason: paused for review"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in paused list output:\n%s", want, out)
		}
	}
}

func TestGoalCommand_ListEmpty(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))}
	ctx := testContext()
	if err := cmd.Run(ctx, []string{"list"}); err != nil {
		t.Fatal(err)
	}
	out := ctx.OutputBuffer.String()
	if !strings.Contains(out, "No goals.") {
		t.Errorf("expected empty-state message, got:\n%s", out)
	}
}

func TestGoalCommand_ListCompletion(t *testing.T) {
	cmd := &GoalCommand{}
	comps := cmd.CompleteArgs(testContext(), "li")
	found := false
	for _, c := range comps {
		if c.Value == "list" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'list' in /goal:li completions, got %v", comps)
	}
}

func TestGoalCommand_ManageEdit(t *testing.T) {
	cases := []struct {
		name          string
		submitValue   string
		submitOK      bool
		wantObjective string
	}{
		{"edit applies", "edited objective", true, "edited objective"},
		{"edit trims whitespace", "  padded  ", true, "padded"},
		{"cancel keeps objective", "", false, "original objective"},
		{"blank keeps objective", "   ", true, "original objective"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runManageEditCase(t, tc.submitValue, tc.submitOK, tc.wantObjective)
		})
	}
}

func runManageEditCase(t *testing.T, submitValue string, submitOK bool, wantObjective string) {
	t.Helper()
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))
	cmd := &GoalCommand{Mode: mode, Queue: queue}

	goals, err := queue.Append("original objective")
	if err != nil {
		t.Fatal(err)
	}
	target := goals[0].ID

	ctx := testContext()
	var promptSeen, prefilled string
	ctx.ShowInputFunc = func(prompt, current string, onSubmit func(string, bool)) {
		promptSeen, prefilled = prompt, current
		onSubmit(submitValue, submitOK)
	}
	managerOpens := 0
	var firstItems []tui.SelectorItem
	ctx.SelectOptionFunc = func(_ string, items []tui.SelectorItem, _ string, cb func(string, bool)) {
		managerOpens++
		if managerOpens == 1 {
			firstItems = items
			cb("__edit__"+target, true) // simulate the 'e' hotkey
			return
		}
		cb("", false) // close the reopened manager
	}

	if err := cmd.showQueueManager(ctx); err != nil {
		t.Fatal(err)
	}

	updated, err := queue.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(updated) != 1 || updated[0].Objective != wantObjective {
		t.Fatalf("objective = %+v, want %q", updated, wantObjective)
	}
	assertManagerEditableItems(t, firstItems)
	// The manager must show the queue in execution order — the add-at-start
	// sentinel, the goal row, the add-at-end sentinel, Done — not an
	// alphabetical label sort.
	assertManagerLayout(t, firstItems, "__add_first__", target, "__add_last__", "__done__")
	if prefilled != "original objective" {
		t.Errorf("edit prompt %q pre-filled with %q, want the current objective", promptSeen, prefilled)
	}
	if managerOpens != 2 {
		t.Errorf("expected the manager to reopen after the edit prompt (2 opens), got %d", managerOpens)
	}
}

func assertManagerEditableItems(t *testing.T, items []tui.SelectorItem) {
	t.Helper()
	for _, it := range items {
		if strings.HasPrefix(it.Value, "__") {
			if it.Editable {
				t.Errorf("sentinel row %q must not be editable", it.Value)
			}
			continue
		}
		if !it.Editable {
			t.Errorf("goal item %q not marked editable", it.Value)
		}
	}
}

func TestGoalCommand_ManageDeleteHotkey(t *testing.T) {
	cmd, queue := newManagerCommand(t, "")
	ids := appendQueued(t, queue, "first queued goal", "second queued goal")

	ctx := testContext()
	getFlashes := collectFlashes(&ctx)

	opens := 0
	var confirmTitle, confirmCursor string
	ctx.SelectOptionFunc = func(title string, _ []tui.SelectorItem, current string, cb func(string, bool)) {
		opens++
		switch opens {
		case 1: // manager
			cb("__delete__"+ids[1], true) // simulate the Delete hotkey
		case 2: // delete confirmation
			confirmTitle, confirmCursor = title, current
			cb("yes", true)
		default: // reopened manager
			cb("", false)
		}
	}

	if err := cmd.showQueueManager(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(confirmTitle, "Delete goal ") {
		t.Errorf("expected a delete confirmation, got selector %q", confirmTitle)
	}
	if confirmCursor != "no" {
		t.Errorf("confirm cursor = %q, want the safe default on \"no\"", confirmCursor)
	}
	assertQueueObjectives(t, queue, []string{"first queued goal"})
	if opens != 3 {
		t.Errorf("expected manager→confirm→manager (3 selector invocations), got %d", opens)
	}
	for _, f := range getFlashes() {
		if strings.Contains(f, "not found") {
			t.Errorf("regression: mangled-id error surfaced: %q", f)
		}
	}
}

func TestGoalCommand_ManageDeleteConfirmNo(t *testing.T) {
	cases := []struct {
		name     string
		answer   string
		answerOK bool
	}{
		{"no keeps the goal", "no", true},
		{"escape keeps the goal", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runManageDeleteDeclineCase(t, tc.answer, tc.answerOK)
		})
	}
}

func runManageDeleteDeclineCase(t *testing.T, answer string, answerOK bool) {
	t.Helper()
	cmd, queue := newManagerCommand(t, "")
	ids := appendQueued(t, queue, "keep me")

	ctx := testContext()
	opens := 0
	var cursor string
	ctx.SelectOptionFunc = func(_ string, _ []tui.SelectorItem, current string, cb func(string, bool)) {
		opens++
		cursor = current
		switch opens {
		case 1: // manager
			cb("__delete__"+ids[0], true)
		case 2: // confirmation → decline
			cb(answer, answerOK)
		default: // reopened manager
			cb("", false)
		}
	}

	if err := cmd.showQueueManager(ctx); err != nil {
		t.Fatal(err)
	}
	assertQueueObjectives(t, queue, []string{"keep me"})
	expectManagerCursor(t, opens, 3, cursor, ids[0])
}

func collectFlashes(ctx *core.Context) func() []string {
	ctx.EventBus = event.MakeBus(8, 8, 8, 8)
	var mu sync.Mutex
	var flashes []string
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range ctx.EventBus.Chat {
			if ev.Flash != nil {
				mu.Lock()
				flashes = append(flashes, ev.Flash.Text)
				mu.Unlock()
			}
		}
	}()
	return func() []string {
		close(ctx.EventBus.Chat)
		<-done
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), flashes...)
	}
}

func newManagerCommand(t *testing.T, activeObjective string) (*GoalCommand, *core.GoalQueueStore) {
	t.Helper()
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	if activeObjective != "" {
		if _, err := mode.CreateGoal(goal.CreateGoalInput{Objective: activeObjective}, goal.GoalActorUser); err != nil {
			t.Fatal(err)
		}
	}
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))
	return &GoalCommand{Mode: mode, Queue: queue}, queue
}

func appendQueued(t *testing.T, queue *core.GoalQueueStore, objectives ...string) []string {
	t.Helper()
	ids := make([]string, 0, len(objectives))
	for _, obj := range objectives {
		goals, err := queue.Append(obj)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, goals[len(goals)-1].ID)
	}
	return ids
}

func queueObjectives(t *testing.T, queue *core.GoalQueueStore) []string {
	t.Helper()
	got, err := queue.Read()
	if err != nil {
		t.Fatal(err)
	}
	objs := make([]string, 0, len(got))
	for _, g := range got {
		objs = append(objs, g.Objective)
	}
	return objs
}

func assertQueueObjectives(t *testing.T, queue *core.GoalQueueStore, want []string) {
	t.Helper()
	if got := queueObjectives(t, queue); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("queue = %v, want %v", got, want)
	}
}

func assertQueueIDs(t *testing.T, queue *core.GoalQueueStore, ids []string, wantOrder []int) {
	t.Helper()
	got, err := queue.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(wantOrder) {
		t.Fatalf("queue = %+v, want %d goals", got, len(wantOrder))
	}
	for pos, orig := range wantOrder {
		if got[pos].ID != ids[orig] {
			t.Errorf("queue[%d] = %q, want original #%d (queue=%+v)", pos, got[pos].ID, orig, got)
		}
	}
}

func expectManagerCursor(t *testing.T, opens, wantOpens int, cursor, want string) {
	t.Helper()
	if opens != wantOpens || cursor != want {
		t.Errorf("selector opens=%d cursor=%q, want %d opens with the cursor on %q",
			opens, cursor, wantOpens, want)
	}
}

func assertManagerLayout(t *testing.T, items []tui.SelectorItem, wantValues ...string) {
	t.Helper()
	if len(items) != len(wantValues) {
		t.Fatalf("manager items = %+v, want %d rows", items, len(wantValues))
	}
	for i, w := range wantValues {
		if items[i].Value != w {
			t.Errorf("items[%d].Value = %q, want %q (all=%+v)", i, items[i].Value, w, items)
		}
	}
}

func assertAllPreserveOrder(t *testing.T, items []tui.SelectorItem) {
	t.Helper()
	for i, it := range items {
		if !it.PreserveOrder {
			t.Errorf("items[%d] (%q) must set PreserveOrder to keep execution order", i, it.Value)
		}
	}
}

func TestGoalCommand_ManageListExecutionOrder(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	if _, err := mode.CreateGoal(goal.CreateGoalInput{Objective: "running goal"}, goal.GoalActorUser); err != nil {
		t.Fatal(err)
	}
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))
	cmd := &GoalCommand{Mode: mode, Queue: queue}
	if _, err := queue.Append("first queued"); err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Append("second queued"); err != nil {
		t.Fatal(err)
	}
	queued, err := queue.Read()
	if err != nil {
		t.Fatal(err)
	}

	ctx := testContext()
	var items []tui.SelectorItem
	var keys tui.SelectorKeymap
	keyedUsed := false
	ctx.SelectOptionKeyedFunc = func(_ string, it []tui.SelectorItem, _ string, k tui.SelectorKeymap, cb func(string, bool)) {
		keyedUsed = true
		keys = k
		items = it
		cb("__done__", true)
	}

	if err := cmd.showQueueManager(ctx); err != nil {
		t.Fatal(err)
	}
	if !keyedUsed || !keys.ReorderMode {
		t.Errorf("manager must request the reorder keymap (keyedUsed=%v, keys=%+v)", keyedUsed, keys)
	}
	assertManagerLayout(t, items,
		"__add_first__", "__active__", queued[0].ID, queued[1].ID, "__add_last__", "__done__")
	assertAllPreserveOrder(t, items)
	if !strings.HasPrefix(items[1].Label, "[active] ") {
		t.Errorf("active row label = %q, want an [active] marker", items[1].Label)
	}
	assertManagerEditableItems(t, items)
}

func TestGoalCommand_ManageMoveHotkeys(t *testing.T) {
	cases := []manageMoveCase{
		{"move second up", "__moveup__", 1, []int{1, 0, 2}, 1},
		{"move first down", "__movedown__", 0, []int{1, 0, 2}, 0},
		{"first cannot move up", "__moveup__", 0, []int{0, 1, 2}, 0},
		{"last cannot move down", "__movedown__", 2, []int{0, 1, 2}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runManageMoveCase(t, tc)
		})
	}
}

// manageMoveCase describes one reorder-hotkey scenario: emit prefix+ids[idx]
// on the first manager open, expect the queue reordered per wantOrder
// (indices into the original ids) and the cursor on ids[wantCursor].
type manageMoveCase struct {
	name       string
	prefix     string
	idx        int
	wantOrder  []int
	wantCursor int
}

func runManageMoveCase(t *testing.T, tc manageMoveCase) {
	t.Helper()
	cmd, queue := newManagerCommand(t, "")
	ids := appendQueued(t, queue, "g0", "g1", "g2")

	ctx := testContext()
	opens := 0
	var cursor string
	ctx.SelectOptionFunc = func(_ string, _ []tui.SelectorItem, current string, cb func(string, bool)) {
		opens++
		cursor = current
		if opens == 1 {
			cb(tc.prefix+ids[tc.idx], true) // simulate the hotkey
			return
		}
		cb("", false) // close the reopened manager
	}

	if err := cmd.showQueueManager(ctx); err != nil {
		t.Fatal(err)
	}
	assertQueueIDs(t, queue, ids, tc.wantOrder)
	expectManagerCursor(t, opens, 2, cursor, ids[tc.wantCursor])
}
