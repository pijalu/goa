// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package todo

import (
	"strings"
	"testing"
)

func TestTodoList_NoDuplicateIDAfterRemove(t *testing.T) {
	tool := &TodoListTool{}
	add := func() string {
		out, err := tool.Execute(`{"action":"add","description":"task"}`)
		if err != nil {
			t.Fatalf("add: %v", err)
		}
		i := strings.Index(out, "todo-")
		j := strings.Index(out[i:], ":")
		if i < 0 || j < 0 {
			t.Fatalf("cannot parse id from %q", out)
		}
		return out[i : i+j]
	}

	id1 := add()
	id2 := add()
	if _, err := tool.Execute(`{"action":"remove","id":"` + id1 + `"}`); err != nil {
		t.Fatalf("remove: %v", err)
	}
	id3 := add()

	if id3 == "todo-2" {
		t.Errorf("id collision: new add produced %s (should be todo-3)", id3)
	}
	if id3 != "todo-3" {
		t.Errorf("expected todo-3, got %s", id3)
	}

	seen := map[string]int{}
	for _, it := range tool.Items() {
		seen[it.ID]++
	}
	for id, n := range seen {
		if n > 1 {
			t.Errorf("duplicate live id %s appears %d times", id, n)
		}
	}
	if id1 == id2 {
		t.Errorf("first two ids collided: %s", id1)
	}
}
