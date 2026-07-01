// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package todo

import (
	"strings"
	"testing"
)

func TestTodoListAddAndList(t *testing.T) {
	tool := &TodoListTool{}
	if _, err := tool.Execute(`{"action":"add","description":"fix bug"}`); err != nil {
		t.Fatalf("add: %v", err)
	}
	out, err := tool.Execute(`{"action":"list"}`)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out, "fix bug") {
		t.Errorf("list output missing bug: %q", out)
	}
}

func TestTodoListComplete(t *testing.T) {
	tool := &TodoListTool{}
	tool.Execute(`{"action":"add","description":"fix bug"}`)
	out, err := tool.Execute(`{"action":"complete","id":"todo-1"}`)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if !strings.Contains(out, "done") {
		t.Errorf("complete output missing done: %q", out)
	}
}

func TestTodoListMissingDescription(t *testing.T) {
	tool := &TodoListTool{}
	_, err := tool.Execute(`{"action":"add"}`)
	if err == nil {
		t.Fatal("expected error for missing description")
	}
}

func TestTodoListClear(t *testing.T) {
	tool := &TodoListTool{}
	tool.Execute(`{"action":"add","description":"a"}`)
	if _, err := tool.Execute(`{"action":"clear"}`); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if len(tool.Items()) != 0 {
		t.Error("expected empty after clear")
	}
}
