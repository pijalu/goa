// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package todo

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/pijalu/goa/core/goal"
)

// TestTodoListTool_DeferredAndRetryable pins the on-demand loading contract:
// todo_list is deferred (schema fetched via tool_search) and never retried.
func TestTodoListTool_DeferredAndRetryable(t *testing.T) {
	tool := &TodoListTool{}
	if !tool.Deferred() {
		t.Error("Deferred() = false, want true (todo_list loads on demand)")
	}
	if tool.IsRetryable(nil) || tool.IsRetryable(&internalToolError{}) {
		t.Error("IsRetryable() = true, want false (todo calls are deterministic)")
	}
}

type internalToolError struct{}

func (internalToolError) Error() string { return "boom" }

// TestTodoListTool_SchemaShape pins the advertised action/status enums and the
// required action field, since models key their calls off this schema.
func TestTodoListTool_SchemaShape(t *testing.T) {
	schema := (&TodoListTool{}).Schema()
	if schema.Name != "todo_list" {
		t.Errorf("Name = %q, want todo_list", schema.Name)
	}
	if schema.Description == "" {
		t.Error("Description must not be empty")
	}
	props, ok := schema.Schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties missing or wrong type: %#v", schema.Schema["properties"])
	}
	assertEnum(t, props, "action", []string{"add", "update", "complete", "remove", "list", "clear"})
	assertEnum(t, props, "status", []string{"pending", "in_progress", "done"})
	for _, key := range []string{"id", "description", "status"} {
		if _, present := props[key]; !present {
			t.Errorf("properties missing %q", key)
		}
	}
	required, ok := schema.Schema["required"].([]string)
	if !ok || len(required) != 1 || required[0] != "action" {
		t.Errorf("required = %#v, want [action]", schema.Schema["required"])
	}
}

func assertEnum(t *testing.T, props map[string]any, key string, want []string) {
	t.Helper()
	field, ok := props[key].(map[string]any)
	if !ok {
		t.Fatalf("property %q missing or wrong type", key)
	}
	got, ok := field["enum"].([]string)
	if !ok {
		t.Fatalf("property %q has no enum", key)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("%s enum = %v, want %v", key, got, want)
	}
}

// TestTodoListTool_DocsAndExamples pins that the embedded docs load and that
// every advertised example is valid JSON carrying an action.
func TestTodoListTool_DocsAndExamples(t *testing.T) {
	tool := &TodoListTool{}
	short, long := tool.ShortDoc(), tool.LongDoc()
	if strings.TrimSpace(short) == "" || strings.TrimSpace(long) == "" {
		t.Fatalf("embedded docs empty: short=%q long=%q", short, long)
	}
	// ShortDoc is the one-line picker summary; LongDoc is the full markdown
	// document (already longer than the summary).
	if trimmed := strings.TrimSpace(short); strings.Contains(trimmed, "\n") {
		t.Errorf("ShortDoc must be a single line: %q", trimmed)
	}
	if len(long) <= len(short) {
		t.Errorf("LongDoc (%d bytes) must be more detailed than ShortDoc (%d bytes)", len(long), len(short))
	}
	examples := tool.Examples()
	if len(examples) == 0 {
		t.Fatal("Examples() returned none")
	}
	for _, ex := range examples {
		var parsed map[string]any
		if err := json.Unmarshal([]byte(ex), &parsed); err != nil {
			t.Errorf("example is not valid JSON (%v): %s", err, ex)
			continue
		}
		if action, _ := parsed["action"].(string); action == "" {
			t.Errorf("example lacks an action: %s", ex)
		}
	}
}

// TestTodoListTool_InvalidInputs pins both parse-level and action-level
// rejections.
func TestTodoListTool_InvalidInputs(t *testing.T) {
	tool := &TodoListTool{}
	if _, err := tool.Execute(`not-json`); err == nil {
		t.Error("malformed JSON must be rejected")
	}
	if _, err := tool.Execute(`{"action":"explode"}`); err == nil {
		t.Error("unknown action must be rejected")
	}
}

// TestTodoListTool_UpdateSemantics pins update branches: a missing status keeps
// the current status, a missing id is rejected, and an unknown id is not_found.
func TestTodoListTool_UpdateSemantics(t *testing.T) {
	tool := &TodoListTool{}
	tool.Execute(`{"action":"add","description":"keep me pending"}`)

	out, err := tool.Execute(`{"action":"update","id":"todo-1"}`)
	if err != nil {
		t.Fatalf("update without status: %v", err)
	}
	if !strings.Contains(out, statusPending) {
		t.Errorf("update without status changed it: %q", out)
	}
	if got := tool.Items()[0].Status; got != statusPending {
		t.Errorf("status = %q, want %q", got, statusPending)
	}
	if _, err := tool.Execute(`{"action":"update","status":"done"}`); err == nil {
		t.Error("update without id must be rejected")
	}
	if _, err := tool.Execute(`{"action":"update","id":"todo-99","status":"done"}`); err == nil {
		t.Error("update of unknown id must be rejected")
	}

	if _, err := tool.Execute(`{"action":"remove","id":"todo-99"}`); err == nil {
		t.Error("remove of unknown id must be rejected")
	}
	if _, err := tool.Execute(`{"action":"remove"}`); err == nil {
		t.Error("remove without id must be rejected")
	}
	if out, err := tool.Execute(`{"action":"remove","id":"todo-1"}`); err != nil {
		t.Fatalf("remove: %v", err)
	} else if !strings.Contains(out, "Removed todo-1") {
		t.Errorf("remove output = %q", out)
	}
	if out, err := tool.Execute(`{"action":"list"}`); err != nil {
		t.Fatalf("list: %v", err)
	} else if !strings.Contains(out, "No todos") {
		t.Errorf("list after removal = %q, want empty notice", out)
	}
}

// TestTodoListTool_GoalListRendering pins the goal-linked list rendering: a
// blank list reports its blankness, a populated one renders status/id/title.
func TestTodoListTool_GoalListRendering(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	tool := &TodoListTool{Mode: mode}
	if _, err := mode.CreateGoal(goal.CreateGoalInput{Objective: "goal work"}, goal.GoalActorUser); err != nil {
		t.Fatal(err)
	}

	out := executeTodoRequest(t, tool, `{"action":"list"}`)
	if !strings.Contains(out, "goal-linked list is blank") {
		t.Errorf("blank goal list output = %q", out)
	}

	executeTodoRequest(t, tool, `{"action":"add","description":"first goal task"}`)
	out = executeTodoRequest(t, tool, `{"action":"list"}`)
	if !strings.Contains(out, "Todos (linked to goal):") || !strings.Contains(out, "first goal task") {
		t.Errorf("populated goal list output = %q", out)
	}
	if strings.HasSuffix(out, "\n") {
		t.Errorf("list output must not end with a newline: %q", out)
	}

	// An update with no status defaults to in_progress for goal todos.
	id := mode.GetActiveGoal().Todos[0].ID
	out = executeTodoRequest(t, tool, `{"action":"update","id":"`+id+`"}`)
	if !strings.Contains(out, statusInProgress) {
		t.Errorf("goal update without status = %q, want in_progress default", out)
	}
}

// TestTodoListTool_NilModeUsesSessionList pins that without a goal mode the
// tool never touches goal state.
func TestTodoListTool_NilModeUsesSessionList(t *testing.T) {
	tool := &TodoListTool{}
	if tool.goalActive() {
		t.Fatal("goalActive() = true with nil Mode")
	}
	out := executeTodoRequest(t, tool, `{"action":"add","description":"solo"}`)
	if !strings.Contains(out, "Added todo-1: solo") {
		t.Errorf("add output = %q", out)
	}
	if len(tool.Items()) != 1 {
		t.Errorf("session items = %d, want 1", len(tool.Items()))
	}
}
