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

// TestGoalTool_Create_FreshContext verifies the model-facing freshContext
// argument is threaded into CreateGoalInput and surfaced on the goal snapshot
// (bugs.md: per-goal clean-context flag).
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
// the goal's framework-managed todo list via the goal tool (bugs.md:
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

// fakeQueue is an in-memory GoalQueue for testing list semantics.
type fakeQueue struct {
	goals []goal.UpcomingGoal
	n     int
}

func (q *fakeQueue) Read() ([]goal.UpcomingGoal, error) {
	return append([]goal.UpcomingGoal(nil), q.goals...), nil
}

func (q *fakeQueue) AppendWithOptions(objective string, criterion *string, freshContext bool) ([]goal.UpcomingGoal, error) {
	q.n++
	q.goals = append(q.goals, goal.UpcomingGoal{
		ID:                  fmt.Sprintf("q%d", q.n),
		Objective:           objective,
		CompletionCriterion: criterion,
		FreshContext:        freshContext,
	})
	return q.Read()
}

func (q *fakeQueue) Remove(id string) ([]goal.UpcomingGoal, *goal.UpcomingGoal, error) {
	for i, g := range q.goals {
		if g.ID == id {
			removed := g
			q.goals = append(q.goals[:i], q.goals[i+1:]...)
			out, _ := q.Read()
			return out, &removed, nil
		}
	}
	return q.goals, nil, fmt.Errorf("queued goal %q not found", id)
}

func (q *fakeQueue) Move(id, direction string) ([]goal.UpcomingGoal, error) {
	for i, g := range q.goals {
		if g.ID == id {
			j := i - 1
			if direction == "down" {
				j = i + 1
			}
			if j < 0 || j >= len(q.goals) {
				return q.Read()
			}
			q.goals[i], q.goals[j] = q.goals[j], q.goals[i]
			return q.Read()
		}
	}
	return q.Read()
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
