// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/tui"
)

// newTodoTestCmd builds a TodoCommand over a goal with three seeded todos.
func newTodoTestCmd(t *testing.T) (*TodoCommand, *goal.GoalMode) {
	t.Helper()
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	if _, err := mode.CreateGoal(goal.CreateGoalInput{Objective: "x"}, goal.GoalActorUser); err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"first task", "second task", "third task"} {
		if _, err := mode.AddGoalTodo(title, goal.GoalActorUser); err != nil {
			t.Fatal(err)
		}
	}
	return &TodoCommand{Mode: mode}, mode
}

// TestTodoCommand_Parse covers the positional colon forms (Issue 5).
func TestTodoCommand_Parse(t *testing.T) {
	cmd := &TodoCommand{}
	cases := []struct {
		args []string
		kind string
		pos  int
		text string
	}{
		{args: []string{}, kind: "list"},
		{args: []string{"list"}, kind: "list"},
		{args: []string{"add"}, kind: "add-interactive"},
		{args: []string{"add", "write tests"}, kind: "add", text: "write tests"},
		{args: []string{"edit", "1"}, kind: "edit-interactive", pos: 1},
		{args: []string{"edit", "2", "new title"}, kind: "edit", pos: 2, text: "new title"},
		{args: []string{"done", "3"}, kind: "done", pos: 3},
		{args: []string{"undone", "1"}, kind: "undone", pos: 1},
		{args: []string{"delete", "2"}, kind: "delete", pos: 2},
		{args: []string{"rm", "2"}, kind: "delete", pos: 2},
		{args: []string{"done", "x"}, kind: "error"},  // non-numeric
		{args: []string{"frobnicate"}, kind: "error"}, // unknown subcommand
		{args: []string{"edit"}, kind: "error"},       // missing positional
		{args: []string{"delete"}, kind: "error"},     // missing positional
	}
	for _, tc := range cases {
		got := cmd.parseArgs(tc.args)
		if got.kind != tc.kind || got.position != tc.pos || got.text != tc.text {
			t.Errorf("parseArgs(%v) = {%s %d %q}, want {%s %d %q}",
				tc.args, got.kind, got.position, got.text, tc.kind, tc.pos, tc.text)
		}
	}
}

// TestTodoCommand_List renders the numbered list with status markers.
func TestTodoCommand_List(t *testing.T) {
	cmd, mode := newTodoTestCmd(t)
	if _, err := mode.UpdateGoalTodo("t2", goal.TodoInProgress, goal.GoalActorUser); err != nil {
		t.Fatal(err)
	}
	if _, err := mode.UpdateGoalTodo("t3", goal.TodoDone, goal.GoalActorUser); err != nil {
		t.Fatal(err)
	}
	ctx := testContext()
	if err := cmd.Run(ctx, []string{"list"}); err != nil {
		t.Fatal(err)
	}
	out := ctx.OutputBuffer.String()
	for _, want := range []string{"1.", "2.", "3.", "first task", "second task", "third task", "[ ]", "[>]", "[x]"} {
		if !strings.Contains(out, want) {
			t.Errorf("list missing %q:\n%s", want, out)
		}
	}
}

// TestTodoCommand_ListNoGoal: without an active goal every subcommand fails
// with a clear message.
func TestTodoCommand_ListNoGoal(t *testing.T) {
	cmd := &TodoCommand{Mode: goal.NewGoalMode(nil, nil, nil, nil)}
	ctx := testContext()
	for _, args := range [][]string{{"list"}, {"add", "x"}, {"done", "1"}, {"edit", "1"}, {"delete", "1"}} {
		if err := cmd.Run(ctx, args); err == nil || !strings.Contains(err.Error(), "no active goal") {
			t.Errorf("Run(%v) = %v, want no-active-goal error", args, err)
		}
	}
}

// TestTodoCommand_Add appends a todo.
func TestTodoCommand_Add(t *testing.T) {
	cmd, mode := newTodoTestCmd(t)
	ctx := testContext()
	if err := cmd.Run(ctx, []string{"add", "fourth task"}); err != nil {
		t.Fatal(err)
	}
	g := mode.GetGoal().Goal
	if len(g.Todos) != 4 || g.Todos[3].Title != "fourth task" {
		t.Errorf("todos = %+v", g.Todos)
	}
}

// TestTodoCommand_AddInteractive prompts on the input line when no title is
// given.
func TestTodoCommand_AddInteractive(t *testing.T) {
	cmd, mode := newTodoTestCmd(t)
	ctx := testContext()
	var gotPrompt string
	ctx.RequestMainInput = func(prompt string, onSubmit func(string)) {
		gotPrompt = prompt
		onSubmit("prompted task")
	}
	if err := cmd.Run(ctx, []string{"add"}); err != nil {
		t.Fatal(err)
	}
	if gotPrompt == "" {
		t.Fatal("add without title did not prompt on the input line")
	}
	g := mode.GetGoal().Goal
	if len(g.Todos) != 4 || g.Todos[3].Title != "prompted task" {
		t.Errorf("todos = %+v", g.Todos)
	}
}

// TestTodoCommand_Done marks a todo done by position; undone flips it back.
func TestTodoCommand_Done(t *testing.T) {
	cmd, mode := newTodoTestCmd(t)
	ctx := testContext()
	if err := cmd.Run(ctx, []string{"done", "2"}); err != nil {
		t.Fatal(err)
	}
	g := mode.GetGoal().Goal
	if g.Todos[1].Status != goal.TodoDone {
		t.Errorf("todo 2 status = %q", g.Todos[1].Status)
	}
	if err := cmd.Run(ctx, []string{"undone", "2"}); err != nil {
		t.Fatal(err)
	}
	if g := mode.GetGoal().Goal; g.Todos[1].Status != goal.TodoPending {
		t.Errorf("todo 2 status after undone = %q", g.Todos[1].Status)
	}
}

// TestTodoCommand_EditDirect renames by position with inline text.
func TestTodoCommand_EditDirect(t *testing.T) {
	cmd, mode := newTodoTestCmd(t)
	ctx := testContext()
	if err := cmd.Run(ctx, []string{"edit", "1", "renamed task"}); err != nil {
		t.Fatal(err)
	}
	if g := mode.GetGoal().Goal; g.Todos[0].Title != "renamed task" {
		t.Errorf("todo 1 title = %q", g.Todos[0].Title)
	}
}

// TestTodoCommand_EditInteractive opens the input line PREFILLED with the
// todo's current title and a prompt naming the todo (Issue 5: "the
// edit should use the inputline with a title matching the todo being worked
// on").
func TestTodoCommand_EditInteractive(t *testing.T) {
	cmd, mode := newTodoTestCmd(t)
	ctx := testContext()
	var gotPrompt, gotPrefill string
	ctx.ShowInputFunc = func(prompt, current string, onSubmit func(string, bool)) {
		gotPrompt, gotPrefill = prompt, current
		onSubmit("edited via prompt", true)
	}
	if err := cmd.Run(ctx, []string{"edit", "2"}); err != nil {
		t.Fatal(err)
	}
	if gotPrefill != "second task" {
		t.Errorf("prefill = %q, want the todo's current title %q", gotPrefill, "second task")
	}
	if !strings.Contains(gotPrompt, "second task") || !strings.Contains(gotPrompt, "2") {
		t.Errorf("prompt %q must name the todo being edited (#2, current title)", gotPrompt)
	}
	if g := mode.GetGoal().Goal; g.Todos[1].Title != "edited via prompt" {
		t.Errorf("todo 2 title = %q", g.Todos[1].Title)
	}
}

// TestTodoCommand_Delete removes by position; survivors keep their titles.
func TestTodoCommand_Delete(t *testing.T) {
	cmd, mode := newTodoTestCmd(t)
	ctx := testContext()
	if err := cmd.Run(ctx, []string{"delete", "2"}); err != nil {
		t.Fatal(err)
	}
	g := mode.GetGoal().Goal
	if len(g.Todos) != 2 || g.Todos[0].Title != "first task" || g.Todos[1].Title != "third task" {
		t.Errorf("todos = %+v", g.Todos)
	}
}

// TestTodoCommand_OutOfRange: a positional beyond the list length is a clear
// error.
func TestTodoCommand_OutOfRange(t *testing.T) {
	cmd, _ := newTodoTestCmd(t)
	ctx := testContext()
	for _, args := range [][]string{{"done", "9"}, {"edit", "0"}, {"delete", "-1"}} {
		if err := cmd.Run(ctx, args); err == nil {
			t.Errorf("Run(%v): want out-of-range error", args)
		}
	}
}

// TestTodoCommand_HelpRegistered: the command exposes help like the others.
func TestTodoCommand_HelpRegistered(t *testing.T) {
	cmd := &TodoCommand{}
	if cmd.Name() != "todo" {
		t.Errorf("Name = %q", cmd.Name())
	}
	if cmd.ShortHelp() == "" {
		t.Error("ShortHelp empty")
	}
	_ = tui.NewEditor() // keep tui import usage honest with sibling tests
}
