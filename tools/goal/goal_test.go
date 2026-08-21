// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/pijalu/goa/core/goal"
)

// newGoalTool builds a GoalTool with a controllable create gate.
func newGoalTool(mode *goal.GoalMode, createAllowed func() bool) *GoalTool {
	return &GoalTool{Mode: mode, CreateAllowed: createAllowed}
}

func TestGoalTool_SchemaShape(t *testing.T) {
	tool := newGoalTool(goal.NewGoalMode(nil, nil, nil, nil), nil)
	s := tool.Schema()
	if s.Name != "goal" {
		t.Errorf("schema name = %q, want goal", s.Name)
	}
	if s.Description == "" {
		t.Error("schema description is empty")
	}
	props, _ := s.Schema["properties"].(map[string]any)
	if props == nil || props["action"] == nil {
		t.Error("schema missing action property")
	}
}

func TestGoalTool_CreateGatedOff_NoActiveGoal(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	tool := newGoalTool(mode, func() bool { return false })
	_, err := tool.Execute(`{"action":"create","objective":"do x"}`)
	if err == nil {
		t.Fatal("expected a gate error when create is disabled and no goal is active")
	}
	// State must be unchanged: still no goal.
	if mode.GetActiveGoal() != nil {
		t.Error("gate-off create must not create a goal")
	}
}

func TestGoalTool_CreateGatedOff_ActiveGoalAllows(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	// Seed an active goal (e.g. user started one via /goal).
	if _, err := mode.CreateGoal(goal.CreateGoalInput{Objective: "first"}, goal.GoalActorUser); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tool := newGoalTool(mode, func() bool { return mode.GetActiveGoal() != nil })
	out, err := tool.Execute(`{"action":"create","objective":"second","replace":true}`)
	if err != nil {
		t.Fatalf("create during an active goal must be allowed: %v", err)
	}
	if !strings.Contains(out, "second") {
		t.Errorf("output = %q", out)
	}
}

func TestGoalTool_CreateGateOn(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	tool := newGoalTool(mode, func() bool { return true })
	out, err := tool.Execute(`{"action":"create","objective":"build it"}`)
	if err != nil {
		t.Fatalf("create with gate on: %v", err)
	}
	var decoded struct {
		Goal struct {
			Objective string `json:"objective"`
		} `json:"goal"`
	}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("output not JSON: %v (%q)", err, out)
	}
	if decoded.Goal.Objective != "build it" {
		t.Errorf("objective = %q", decoded.Goal.Objective)
	}
}

func TestGoalTool_UpdateCompleteSetsStopTurn(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	if _, err := mode.CreateGoal(goal.CreateGoalInput{Objective: "x"}, goal.GoalActorUser); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tool := newGoalTool(mode, nil)
	res, err := tool.ExecuteWithResult(`{"action":"update","status":"complete"}`)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !res.StopTurn {
		t.Error("update complete must set StopTurn so the turn ends")
	}
	if mode.GetActiveGoal() != nil {
		t.Error("goal should no longer be active after complete")
	}
}

// TestGoalTool_CompleteRemindsOpenTodos pins the requirement: when a
// goal is ACHIEVED with pending todos, the framework reminds the model of the
// open items (todos die with the goal, but unfinished work must not vanish
// silently — the model can schedule a follow-up goal if it is still needed).
func TestGoalTool_CompleteRemindsOpenTodos(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	tool := newGoalTool(mode, func() bool { return true })
	createAndAddTodos(t, tool)
	if _, err := tool.Execute(`{"action":"update_todo","todoId":"t1","todoStatus":"done"}`); err != nil {
		t.Fatal(err)
	}
	out, err := tool.Execute(`{"action":"update","status":"complete","reason":"main work delivered"}`)
	if err != nil {
		t.Fatal(err)
	}
	assertCompletionReminder(t, out, true)
	mode2 := goal.NewGoalMode(nil, nil, nil, nil)
	tool2 := newGoalTool(mode2, func() bool { return true })
	if _, err := tool2.Execute(`{"action":"create","objective":"clean run"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := tool2.Execute(`{"action":"add_todo","todoTitle":"only task"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := tool2.Execute(`{"action":"update_todo","todoId":"t1","todoStatus":"done"}`); err != nil {
		t.Fatal(err)
	}
	out, err = tool2.Execute(`{"action":"update","status":"complete","reason":"all done"}`)
	if err != nil {
		t.Fatal(err)
	}
	assertCompletionReminder(t, out, false)
}

func createAndAddTodos(t *testing.T, tool *GoalTool) {
	if _, err := tool.Execute(`{"action":"create","objective":"ship it"}`); err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"done part", "unfinished part"} {
		if _, err := tool.Execute(`{"action":"add_todo","todoTitle":"` + title + `"}`); err != nil {
			t.Fatal(err)
		}
	}
}

func assertCompletionReminder(t *testing.T, out string, wantReminder bool) {
	if !strings.Contains(out, "Goal marked complete.") {
		t.Errorf("completion output = %q", out)
	}
	if strings.Contains(out, "unfinished part") != wantReminder {
		t.Errorf("unfinished reminder mismatch: %q", out)
	}
	if wantReminder && !strings.Contains(out, "1 todo") && !strings.Contains(out, "1 open") {
		t.Errorf("reminder count missing: %q", out)
	}
}

func TestGoalTool_GetReturnsSnapshot(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	tool := newGoalTool(mode, nil)
	out, err := tool.Execute(`{"action":"get"}`)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !strings.Contains(out, `"goal":null`) {
		t.Errorf("get with no goal = %q", out)
	}
}

func TestGoalTool_SetBudget(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	if _, err := mode.CreateGoal(goal.CreateGoalInput{Objective: "x"}, goal.GoalActorUser); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tool := newGoalTool(mode, nil)
	out, err := tool.Execute(`{"action":"set_budget","value":10,"unit":"turns"}`)
	if err != nil {
		t.Fatalf("set_budget: %v", err)
	}
	if !strings.Contains(out, "10 turns") {
		t.Errorf("output = %q", out)
	}
}

func TestGoalTool_ActionFieldMismatch(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	tool := newGoalTool(mode, func() bool { return true })
	if _, err := tool.Execute(`{"action":"create"}`); err == nil {
		t.Error("create without objective must error")
	}
	if _, err := tool.Execute(`{"action":"update"}`); err == nil {
		t.Error("update without status must error")
	}
	if _, err := tool.Execute(`{"action":"set_budget","value":5}`); err == nil {
		t.Error("set_budget without unit must error")
	}
}

// TestGoalTool_ActionInferredWhenOmitted pins the "Goal management
// tool issue" fix: models frequently omit `action` and pass only the payload
// fields (export goa-export-20260729-102137.zip: {"status":"blocked",...} →
// invalid goal action ""). The tool must infer the intended action from the
// fields instead of erroring.
func TestGoalTool_ActionInferredWhenOmitted(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*testing.T)
	}{
		{"status implies update", testInferStatus}, {"objective implies create", testInferObjective},
		{"todoTitle implies add_todo", testInferTodoTitle}, {"todoId+todoStatus imply update_todo", testInferTodoUpdate},
		{"goalId+direction implies reorder", testInferReorder}, {"goalId alone implies cancel", testInferCancel},
		{"value+unit imply set_budget", testInferBudget}, {"empty object implies get", testInferGet},
	} {
		t.Run(tc.name, tc.run)
	}
}

func newInferenceTool(t *testing.T) (*GoalTool, *fakeQueue) {
	t.Helper()
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	q := &fakeQueue{}
	tool := &GoalTool{Mode: mode, CreateAllowed: func() bool { return true }, Queue: q}
	if _, err := tool.Execute(`{"action":"create","objective":"seed"}`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return tool, q
}
func testInferStatus(t *testing.T) {
	tool, _ := newInferenceTool(t)
	out, err := tool.Execute(`{"status":"paused","reason":"waiting on CI"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "paused") {
		t.Errorf("output = %q", out)
	}
}
func testInferObjective(t *testing.T) {
	tool, q := newInferenceTool(t)
	if _, err := tool.Execute(`{"objective":"second goal"}`); err != nil {
		t.Fatal(err)
	}
	read, _ := q.Read()
	if len(read) != 1 || read[0].Objective != "second goal" {
		t.Errorf("queue = %+v", read)
	}
}
func testInferTodoTitle(t *testing.T) {
	tool, _ := newInferenceTool(t)
	out, err := tool.Execute(`{"todoTitle":"write tests"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "write tests") {
		t.Errorf("output = %q", out)
	}
}
func testInferTodoUpdate(t *testing.T) {
	tool, _ := newInferenceTool(t)
	if _, err := tool.Execute(`{"action":"add_todo","todoTitle":"task"}`); err != nil {
		t.Fatal(err)
	}
	out, err := tool.Execute(`{"todoId":"t1","todoStatus":"done"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"status":"done"`) {
		t.Errorf("output = %q", out)
	}
}
func testInferReorder(t *testing.T) {
	tool, q := newInferenceTool(t)
	for _, obj := range []string{"g2", "g3"} {
		if _, err := tool.Execute(`{"action":"create","objective":"` + obj + `"}`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tool.Execute(`{"goalId":"q2","direction":"up"}`); err != nil {
		t.Fatal(err)
	}
	read, _ := q.Read()
	if len(read) != 2 || read[0].Objective != "g3" {
		t.Errorf("queue = %+v", read)
	}
}
func testInferCancel(t *testing.T) {
	tool, q := newInferenceTool(t)
	if _, err := tool.Execute(`{"action":"create","objective":"g2"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(`{"goalId":"q1"}`); err != nil {
		t.Fatal(err)
	}
	read, _ := q.Read()
	if len(read) != 0 {
		t.Errorf("queue = %+v", read)
	}
}
func testInferBudget(t *testing.T) {
	tool, _ := newInferenceTool(t)
	out, err := tool.Execute(`{"value":10,"unit":"turns"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "10 turns") {
		t.Errorf("output = %q", out)
	}
}
func testInferGet(t *testing.T) {
	tool, _ := newInferenceTool(t)
	out, err := tool.Execute(`{}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "seed") {
		t.Errorf("output = %q", out)
	}
}

// TestGoalTool_CreateQueuesBehindPausedOrBlockedGoal pins the second half of
// the Goal management tool issue: with a PAUSED or BLOCKED goal,
// create must ADD to the queue (todo-list semantics) instead of failing with
// "a goal already exists" (GetActiveGoal filters status==active while
// CreateGoal rejects any state — the trap the model hit in the export).
func TestGoalTool_CreateQueuesBehindPausedOrBlockedGoal(t *testing.T) {
	t.Run("paused", func(t *testing.T) { assertQueuedBehind(t, goal.GoalPaused, "hold", "", "paused") })
	t.Run("blocked", func(t *testing.T) { assertQueuedBehind(t, goal.GoalBlocked, "stuck", "user input", "blocked") })
}

func assertQueuedBehind(t *testing.T, status goal.GoalStatus, reason, expectation, label string) {
	t.Helper()
	tool, queue := newQueueingGoalTool(t)
	if _, err := tool.Execute(`{"action":"create","objective":"first"}`); err != nil {
		t.Fatal(err)
	}
	update := fmt.Sprintf(`{"action":"update","status":%q,"reason":%q`, status, reason)
	if expectation != "" {
		update += fmt.Sprintf(`,"expectation":%q`, expectation)
	}
	update += "}"
	if _, err := tool.Execute(update); err != nil {
		t.Fatal(err)
	}
	out, err := tool.Execute(`{"action":"create","objective":"second"}`)
	if err != nil {
		t.Fatalf("create behind a %s goal must enqueue: %v", label, err)
	}
	if !strings.Contains(out, `"queued":1`) {
		t.Errorf("output = %q", out)
	}
	queued, _ := queue.Read()
	if len(queued) != 1 || queued[0].Objective != "second" {
		t.Errorf("queue = %+v", queued)
	}
	if current := tool.Mode.GetGoal().Goal; current == nil || current.Objective != "first" || current.Status != status {
		t.Errorf("goal = %+v", current)
	}
}

func newQueueingGoalTool(t *testing.T) (*GoalTool, *fakeQueue) {
	t.Helper()
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := &fakeQueue{}
	return &GoalTool{Mode: mode, CreateAllowed: func() bool { return true }, Queue: queue, AutoUnblock: func() bool { return false }}, queue
}

// TestGoalTool_Postpone pins the Goal scheduling:feature: the
// model's deprioritize primitive — demote the active goal to the BACK of the
// queue so the next scheduled goal starts (the clear event drives the app's
// auto-promotion, exactly as after a completion).
func TestGoalTool_Postpone(t *testing.T) {
	t.Run("demotes active to back and stops the turn", func(t *testing.T) { assertPostponeWithQueuedGoal(t) })
	t.Run("empty queue parks the goal", func(t *testing.T) { assertPostponeEmptyQueue(t) })
	t.Run("no active goal errors", func(t *testing.T) { assertPostponeWithoutActive(t) })
}

func newSchedulingTool(t *testing.T) (*goal.GoalMode, *fakeQueue, *GoalTool) {
	t.Helper()
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := &fakeQueue{}
	return mode, queue, &GoalTool{Mode: mode, CreateAllowed: func() bool { return true }, Queue: queue}
}

func assertPostponeWithQueuedGoal(t *testing.T) {
	mode, queue, tool := newSchedulingTool(t)
	createGoal(t, tool, `{"action":"create","objective":"current work"}`)
	createGoal(t, tool, `{"action":"create","objective":"scheduled next"}`)
	result, err := tool.ExecuteWithResult(`{"action":"postpone"}`)
	if err != nil {
		t.Fatalf("postpone: %v", err)
	}
	if !result.StopTurn {
		t.Error("postpone must stop the turn")
	}
	if mode.GetGoal().Goal != nil {
		t.Errorf("active goal not cleared")
	}
	read, _ := queue.Read()
	if len(read) != 2 || read[0].Objective != "scheduled next" || read[1].Objective != "current work" {
		t.Errorf("queue = %+v", read)
	}
	if !strings.Contains(result.Output, "current work") {
		t.Errorf("output = %q", result.Output)
	}
}

func assertPostponeEmptyQueue(t *testing.T) {
	_, queue, tool := newSchedulingTool(t)
	createGoal(t, tool, `{"action":"create","objective":"only goal"}`)
	if _, err := tool.Execute(`{"action":"postpone"}`); err != nil {
		t.Fatalf("postpone: %v", err)
	}
	read, _ := queue.Read()
	if len(read) != 1 || read[0].Objective != "only goal" {
		t.Errorf("queue = %+v", read)
	}
}

func assertPostponeWithoutActive(t *testing.T) {
	_, _, tool := newSchedulingTool(t)
	if _, err := tool.Execute(`{"action":"postpone"}`); err == nil {
		t.Error("postpone with no active goal must error")
	}
}

func createGoal(t *testing.T, tool *GoalTool, input string) {
	t.Helper()
	if _, err := tool.Execute(input); err != nil {
		t.Fatal(err)
	}
}

// TestGoalTool_Promote pins the Goal scheduling:feature: the
// model's prioritize primitive — activate a queued goal NOW; the current goal
// is demoted to the FRONT of the queue so it resumes right after.
func TestGoalTool_Promote(t *testing.T) {
	t.Run("activates queued goal and demotes current to front", func(t *testing.T) { assertPromoteQueued(t) })
	t.Run("promote with no active goal just activates", func(t *testing.T) { assertPromoteWithoutActive(t) })
	t.Run("unknown goal errors", func(t *testing.T) { assertPromoteError(t, `{"action":"promote","goalId":"nope"}`) })
	t.Run("missing goalId errors", func(t *testing.T) { assertPromoteError(t, `{"action":"promote"}`) })
}

func assertPromoteQueued(t *testing.T) {
	mode, queue, tool := newSchedulingTool(t)
	createGoal(t, tool, `{"action":"create","objective":"current work"}`)
	createGoal(t, tool, `{"action":"create","objectives":["queued x","scheduled y"]}`)
	result, err := tool.ExecuteWithResult(`{"action":"promote","goalId":"q2"}`)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if !result.StopTurn {
		t.Error("promote must stop the turn")
	}
	if active := mode.GetActiveGoal(); active == nil || active.Objective != "scheduled y" {
		t.Errorf("active after promote = %+v", active)
	}
	read, _ := queue.Read()
	if len(read) != 2 || read[0].Objective != "current work" || read[1].Objective != "queued x" {
		t.Errorf("queue = %+v", read)
	}
}

func assertPromoteWithoutActive(t *testing.T) {
	mode, queue, tool := newSchedulingTool(t)
	if _, err := queue.AppendGoal(goal.UpcomingGoalInput{Objective: "queued x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(`{"action":"promote","goalId":"q1"}`); err != nil {
		t.Fatalf("promote: %v", err)
	}
	if active := mode.GetActiveGoal(); active == nil || active.Objective != "queued x" {
		t.Errorf("active = %+v", active)
	}
	if read, _ := queue.Read(); len(read) != 0 {
		t.Errorf("queue = %+v", read)
	}
}

func assertPromoteError(t *testing.T, input string) {
	_, _, tool := newSchedulingTool(t)
	if _, err := tool.Execute(input); err == nil {
		t.Error("promote must reject invalid goal")
	}
}

// TestGoalTool_SchemaListsSchedulingActions keeps the scheduling actions
// discoverable in the tool schema.
func TestGoalTool_SchemaListsSchedulingActions(t *testing.T) {
	tool := newGoalTool(goal.NewGoalMode(nil, nil, nil, nil), nil)
	s := tool.Schema()
	props, _ := s.Schema["properties"].(map[string]any)
	action, _ := props["action"].(map[string]any)
	enum, _ := action["enum"].([]string)
	joined := strings.Join(enum, ",")
	for _, want := range []string{"postpone", "promote"} {
		if !strings.Contains(joined, want) {
			t.Errorf("action enum missing %q: %v", want, enum)
		}
	}
}

// TestGoalTool_Create_FreshContext verifies the model-facing freshContext
// argument is threaded into CreateGoalInput and surfaced on the goal snapshot
// (per-goal clean-context flag).
func TestGoalTool_Create_FreshContext(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	tool := newGoalTool(mode, func() bool { return true })
	out, err := tool.Execute(`{"action":"create","objective":"self-contained","freshContext":true}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"freshContext":true`) {
		t.Errorf("output missing freshContext:true: %s", out)
	}
	if g := mode.GetGoal().Goal; g == nil || !g.FreshContext {
		t.Errorf("mode FreshContext not set: %+v", g)
	}
}

// TestGoalTool_TodoActions verifies the model can add and check off items in
// the goal's framework-managed todo list via the goal tool
// framework-managed todo list for goals).
func TestGoalTool_TodoActions(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	tool := newGoalTool(mode, func() bool { return true })
	if _, err := tool.Execute(`{"action":"create","objective":"multi-step"}`); err != nil {
		t.Fatal(err)
	}
	out, err := tool.Execute(`{"action":"add_todo","todoTitle":"write tests"}`)
	if err != nil {
		t.Fatalf("add_todo: %v", err)
	}
	if !strings.Contains(out, "write tests") {
		t.Errorf("add_todo output = %q", out)
	}
	// The added item gets an ID (t1); mark it done.
	if _, err := tool.Execute(`{"action":"update_todo","todoId":"t1","todoStatus":"done"}`); err != nil {
		t.Fatalf("update_todo: %v", err)
	}
	g := mode.GetGoal().Goal
	if g == nil || len(g.Todos) != 1 || g.Todos[0].Status != "done" {
		t.Errorf("goal todos = %+v", g)
	}
	// get reflects the todo list.
	getOut, err := tool.Execute(`{"action":"get"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(getOut, "write tests") {
		t.Errorf("get output missing todo: %q", getOut)
	}
}

// TestGoalTool_CreateAppendsWhenActive: with a goal active, create must ADD to
// the queue (todo-like), NOT fail or replace.
func TestGoalTool_CreateAppendsWhenActive(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	q := &fakeQueue{}
	tool := &GoalTool{Mode: mode, CreateAllowed: func() bool { return true }, Queue: q}
	if _, err := tool.Execute(`{"action":"create","objective":"first"}`); err != nil {
		t.Fatal(err)
	}
	// Second create while first is active must enqueue, not error.
	out, err := tool.Execute(`{"action":"create","objective":"second"}`)
	if err != nil {
		t.Fatalf("create while active should append, got error: %v", err)
	}
	if !strings.Contains(out, `"queued":1`) {
		t.Errorf("expected queued:1, got %q", out)
	}
	read, _ := q.Read()
	if len(read) != 1 || read[0].Objective != "second" {
		t.Errorf("queue = %+v", read)
	}
	// Active goal must still be "first" (not replaced).
	if g := mode.GetActiveGoal(); g == nil || g.Objective != "first" {
		t.Errorf("active goal = %+v", g)
	}
}

// TestGoalTool_CreateBatch: objectives array activates the first and queues the rest.
func TestGoalTool_CreateBatch(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	q := &fakeQueue{}
	tool := &GoalTool{Mode: mode, CreateAllowed: func() bool { return true }, Queue: q}
	out, err := tool.Execute(`{"action":"create","objectives":["a","b","c"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"queued":2`) {
		t.Errorf("expected queued:2, got %q", out)
	}
	if g := mode.GetActiveGoal(); g == nil || g.Objective != "a" {
		t.Errorf("active = %+v", g)
	}
	read, _ := q.Read()
	if len(read) != 2 || read[0].Objective != "b" || read[1].Objective != "c" {
		t.Errorf("queue = %+v", read)
	}
}

// TestGoalTool_CreateReplaceStillWorks: explicit replace replaces the active goal.
func TestGoalTool_CreateReplaceStillWorks(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	q := &fakeQueue{}
	tool := &GoalTool{Mode: mode, CreateAllowed: func() bool { return true }, Queue: q}
	tool.Execute(`{"action":"create","objective":"old"}`)
	if _, err := tool.Execute(`{"action":"create","objective":"new","replace":true}`); err != nil {
		t.Fatal(err)
	}
	if g := mode.GetActiveGoal(); g == nil || g.Objective != "new" {
		t.Errorf("active after replace = %+v", g)
	}
	if read, _ := q.Read(); len(read) != 0 {
		t.Errorf("replace must not enqueue, queue = %+v", read)
	}
}

// TestGoalTool_ListCancelReorder: full todo-like list management.
func TestGoalTool_ListCancelReorder(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	q := &fakeQueue{}
	tool := &GoalTool{Mode: mode, CreateAllowed: func() bool { return true }, Queue: q}
	tool.Execute(`{"action":"create","objectives":["g1","g2","g3"]}`)
	// g1 is active; g2->q1, g3->q2 are queued.

	// list shows active + queued.
	out, err := tool.Execute(`{"action":"list"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "g1") || !strings.Contains(out, "g2") || !strings.Contains(out, "g3") {
		t.Errorf("list output = %q", out)
	}

	// reorder g3 (q2) up above g2 (q1).
	if _, err := tool.Execute(`{"action":"reorder","goalId":"q2","direction":"up"}`); err != nil {
		t.Fatalf("reorder: %v", err)
	}
	read, _ := q.Read()
	if len(read) != 2 || read[0].Objective != "g3" {
		t.Errorf("after reorder up, queue = %+v", read)
	}

	// cancel g2 (q1) by ID.
	if _, err := tool.Execute(`{"action":"cancel","goalId":"q1"}`); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	read, _ = q.Read()
	if len(read) != 1 || read[0].Objective != "g3" {
		t.Errorf("after cancel, queue = %+v", read)
	}
}

// newCancelSeededTool builds a tool with one ACTIVE goal and two queued
// (g2→q1, g3→q2) — the fixture shared by the cancel current/all tests.
func newCancelSeededTool(t *testing.T) (*GoalTool, *goal.GoalMode, *fakeQueue) {
	t.Helper()
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	q := &fakeQueue{}
	tool := &GoalTool{Mode: mode, CreateAllowed: func() bool { return true }, Queue: q}
	if _, err := tool.Execute(`{"action":"create","objectives":["g1","g2","g3"]}`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return tool, mode, q
}

// TestGoalTool_CancelCancelsActiveGoal: cancel with NO goalId targets the
// ACTIVE goal (mirrors /goal:cancel) and stops the turn; the message warns
// that a queued successor is promoted PAUSED, never auto-started.
func TestGoalTool_CancelCancelsActiveGoal(t *testing.T) {
	tool, mode, q := newCancelSeededTool(t)

	res, err := tool.ExecuteWithResult(`{"action":"cancel"}`)
	if err != nil {
		t.Fatalf("cancel without goalId: %v", err)
	}
	if mode.GetGoal().Goal != nil {
		t.Error("active goal should be gone after cancel")
	}
	if !res.StopTurn {
		t.Error("cancelling the active goal must stop the turn")
	}
	if !strings.Contains(res.Output, "PAUSED") {
		t.Errorf("output must warn the successor promotes paused: %q", res.Output)
	}
	if !strings.Contains(res.Output, "g1") {
		t.Errorf("output must name the cancelled goal: %q", res.Output)
	}
	// The queue itself is untouched.
	if read, _ := q.Read(); len(read) != 2 {
		t.Errorf("queue = %+v", read)
	}
}

// TestGoalTool_CancelCurrentToken: goalId "current" is the explicit form of
// the bare cancel (and is case-insensitive).
func TestGoalTool_CancelCurrentToken(t *testing.T) {
	tool, mode, _ := newCancelSeededTool(t)

	res, err := tool.ExecuteWithResult(`{"action":"cancel","goalId":"Current"}`)
	if err != nil {
		t.Fatalf("cancel current: %v", err)
	}
	if mode.GetGoal().Goal != nil {
		t.Error("active goal should be gone after cancel current")
	}
	if !res.StopTurn {
		t.Error("cancel current must stop the turn")
	}
}

// TestGoalTool_CancelAllWipesQueueAndGoal: goalId "all" clears every queued
// goal AND cancels the active goal.
func TestGoalTool_CancelAllWipesQueueAndGoal(t *testing.T) {
	tool, mode, q := newCancelSeededTool(t)

	res, err := tool.ExecuteWithResult(`{"action":"cancel","goalId":"all"}`)
	if err != nil {
		t.Fatalf("cancel all: %v", err)
	}
	if mode.GetGoal().Goal != nil {
		t.Error("active goal should be gone after cancel all")
	}
	if read, _ := q.Read(); len(read) != 0 {
		t.Errorf("queue should be empty after cancel all: %+v", read)
	}
	if !res.StopTurn {
		t.Error("cancel all with an active goal must stop the turn")
	}
	if !strings.Contains(res.Output, `"queueCleared":2`) {
		t.Errorf("output must report the cleared queue: %q", res.Output)
	}
}

// TestGoalTool_CancelAllQueueOnly: with no active goal, "all" still wipes the
// queue — and does not stop the turn (nothing was running).
func TestGoalTool_CancelAllQueueOnly(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	q := &fakeQueue{}
	tool := &GoalTool{Mode: mode, CreateAllowed: func() bool { return true }, Queue: q}
	if _, err := q.AppendGoal(goal.UpcomingGoalInput{Objective: "queued"}); err != nil {
		t.Fatal(err)
	}

	res, err := tool.ExecuteWithResult(`{"action":"cancel","goalId":"all"}`)
	if err != nil {
		t.Fatalf("cancel all: %v", err)
	}
	if read, _ := q.Read(); len(read) != 0 {
		t.Errorf("queue should be empty: %+v", read)
	}
	if res.StopTurn {
		t.Error("no active goal existed — the turn must not stop")
	}
	if !strings.Contains(res.Output, `"queueCleared":1`) {
		t.Errorf("output = %q", res.Output)
	}
}

// TestGoalTool_CancelNoActiveGoal: cancel with no goalId and no active goal
// is a tool error.
func TestGoalTool_CancelNoActiveGoal(t *testing.T) {
	tool := &GoalTool{Mode: goal.NewGoalMode(nil, nil, nil, nil), Queue: &fakeQueue{}}
	if _, err := tool.Execute(`{"action":"cancel"}`); err == nil {
		t.Fatal("expected error when no active goal to cancel")
	} else if !strings.Contains(err.Error(), "no active goal to cancel") {
		t.Errorf("error = %v", err)
	}
}

// TestGoalTool_SchemaHandover verifies the goal tool exposes the first-class
// `handover` create param with the structured-content hint (spec section 4:
// the description must tell the model that a handover is what makes clean
// context sufficient).
func TestGoalTool_SchemaHandover(t *testing.T) {
	tool := newGoalTool(goal.NewGoalMode(nil, nil, nil, nil), nil)
	s := tool.Schema()
	props, _ := s.Schema["properties"].(map[string]any)
	handover, ok := props["handover"].(map[string]any)
	if !ok {
		t.Fatal("schema missing handover property")
	}
	if handover["type"] != "string" {
		t.Errorf("handover type = %v, want string", handover["type"])
	}
	desc, _ := handover["description"].(string)
	if !strings.Contains(desc, "clean context sufficient") || !strings.Contains(desc, "untrusted") {
		t.Errorf("handover description missing clean-context hint: %q", desc)
	}
	if !strings.Contains(s.Description, "handover") {
		t.Error("goal tool description must mention the handover param")
	}
}

// TestGoalTool_CreateHandover verifies the caller's explicit `handover` param
// lands on the active goal and is exposed by get and the create result
// (spec: round-trip get/create expose the stored handover).
func TestGoalTool_CreateHandover(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	tool := newGoalTool(mode, func() bool { return true })
	out, err := tool.Execute(`{"action":"create","objective":"successor","handover":"State: shipped. Next: verify."}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"handover":"State: shipped. Next: verify."`) {
		t.Errorf("create result must expose the handover: %q", out)
	}
	if g := mode.GetGoal().Goal; g == nil || g.Handoff == nil || *g.Handoff != "State: shipped. Next: verify." {
		t.Errorf("active goal handover = %+v", g.Handoff)
	}
	getOut, err := tool.Execute(`{"action":"get"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(getOut, `"handover":"State: shipped. Next: verify."`) {
		t.Errorf("get result must expose the stored handover: %q", getOut)
	}
	// The surface key is handover, never handoff.
	if strings.Contains(getOut, `"handoff"`) {
		t.Errorf("get result must use the handover surface: %q", getOut)
	}
}

// TestGoalTool_CreateHandover_Queued verifies a queued create carries the
// explicit handover into the durable queue. The handover is full detail, so
// the compact `list` view no longer exposes it (list is now bounded); the
// durable handover is verified by promoting the queued goal and reading it
// back via `get`, the full-detail path.
func TestGoalTool_CreateHandover_Queued(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	q := &fakeQueue{}
	tool := &GoalTool{Mode: mode, CreateAllowed: func() bool { return true }, Queue: q}
	if _, err := tool.Execute(`{"action":"create","objective":"first"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(`{"action":"create","objective":"second","handover":"queued continuity note"}`); err != nil {
		t.Fatal(err)
	}
	read, _ := q.Read()
	if len(read) != 1 || read[0].Handoff == nil || *read[0].Handoff != "queued continuity note" {
		t.Errorf("queued goal handover = %+v", read)
	}
	// list is compact and must NOT leak the full handover.
	out, err := tool.Execute(`{"action":"list"}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "queued continuity note") {
		t.Errorf("compact list must not expose full handover text: %q", out)
	}
	// The handover survives promotion and is readable in full via `get`.
	if _, err := tool.Execute(`{"action":"promote","goalId":"` + read[0].ID + `"}`); err != nil {
		t.Fatal(err)
	}
	getOut, err := tool.Execute(`{"action":"get"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(getOut, `"handover":"queued continuity note"`) {
		t.Errorf("get must expose the promoted goal's stored handover: %q", getOut)
	}
}

// TestGoalTool_CreateHandover_Cap verifies over-long handover is rejected
// through the tool surface with a clear error.
func TestGoalTool_CreateHandover_Cap(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	tool := newGoalTool(mode, func() bool { return true })
	big := strings.Repeat("h", goal.MaxHandoverLength+1)
	if _, err := tool.Execute(`{"action":"create","objective":"x","handover":"` + big + `"}`); err == nil {
		t.Fatal("expected cap error for over-long handover")
	}
}

// TestGoalTool_PostponeCarriesHandover verifies postpone carries the active
// goal's stored handover forward into the queue (fixes the tools/goal gap:
// previously the demoted goal lost its handover).
func TestGoalTool_PostponeCarriesHandover(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	q := &fakeQueue{}
	tool := &GoalTool{Mode: mode, CreateAllowed: func() bool { return true }, Queue: q}
	if _, err := tool.Execute(`{"action":"create","objective":"active goal","handover":"carry me"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(`{"action":"postpone"}`); err != nil {
		t.Fatal(err)
	}
	read, _ := q.Read()
	if len(read) != 1 || read[0].Handoff == nil || *read[0].Handoff != "carry me" {
		t.Errorf("postponed goal must carry its handover: %+v", read)
	}
}

// TestGoalTool_PromoteCarriesHandover verifies promote carries handovers both
// ways: the promoted queued goal's stored handover becomes the new active
// goal's handover, and the demoted current goal keeps its own stored handover
// at the front of the queue.
func TestGoalTool_PromoteCarriesHandover(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	q := &fakeQueue{}
	tool := &GoalTool{Mode: mode, CreateAllowed: func() bool { return true }, Queue: q}
	// Active goal with its own handover.
	if _, err := tool.Execute(`{"action":"create","objective":"current","handover":"current handover"}`); err != nil {
		t.Fatal(err)
	}
	// Queued goal with its own handover.
	if _, err := tool.Execute(`{"action":"create","objective":"queued","handover":"queued handover"}`); err != nil {
		t.Fatal(err)
	}
	read, _ := q.Read()
	if len(read) != 1 {
		t.Fatalf("queue = %+v", read)
	}
	if _, err := tool.Execute(`{"action":"promote","goalId":"` + read[0].ID + `"}`); err != nil {
		t.Fatal(err)
	}
	// Promoted goal is active with its stored handover.
	if g := mode.GetActiveGoal(); g == nil || g.Handoff == nil || *g.Handoff != "queued handover" {
		t.Errorf("promoted active handover = %+v", g.Handoff)
	}
	// Demoted current goal waits at the front of the queue with ITS handover.
	read, _ = q.Read()
	if len(read) != 1 || read[0].Objective != "current" || read[0].Handoff == nil || *read[0].Handoff != "current handover" {
		t.Errorf("demoted goal must keep its handover: %+v", read)
	}
}
