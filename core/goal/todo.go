// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"fmt"
	"strings"
)

// GoalTodoItem is a single managed task within a goal's todo list. The
// framework surfaces the list to the model each turn so a multi-step goal
// self-tracks instead of relying on the model to remember remaining work.
type GoalTodoItem struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"` // pending | in_progress | done
}

// Goal todo statuses.
const (
	TodoPending    = "pending"
	TodoInProgress = "in_progress"
	TodoDone       = "done"
)

// normalizeTodoStatus clamps an arbitrary status string to a known value,
// defaulting to pending.
func normalizeTodoStatus(s string) string {
	switch strings.TrimSpace(strings.ToLower(s)) {
	case TodoInProgress:
		return TodoInProgress
	case TodoDone:
		return TodoDone
	default:
		return TodoPending
	}
}

// todoSummaryLine renders the todo list for the per-turn goal injection. It is
// deterministic and compact so the model sees remaining work at a glance.
// Returns "" when the list is empty (nothing to surface).
func todoSummaryLine(items []GoalTodoItem) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	done := 0
	for _, it := range items {
		if it.Status == TodoDone {
			done++
		}
	}
	fmt.Fprintf(&b, "Todo (%d/%d done):\n", done, len(items))
	for _, it := range items {
		var mark string
		switch it.Status {
		case TodoDone:
			mark = "[x]"
		case TodoInProgress:
			mark = "[~]"
		default:
			mark = "[ ]"
		}
		fmt.Fprintf(&b, "  %s %s (%s)\n", mark, it.Title, it.ID)
	}
	return strings.TrimRight(b.String(), "\n")
}

// nextTodoID returns the lowest unused numeric ID as a string, so IDs are
// stable and never reused within a goal.
func nextTodoID(items []GoalTodoItem) string {
	used := make(map[string]bool, len(items))
	for _, it := range items {
		used[it.ID] = true
	}
	for i := 1; ; i++ {
		id := fmt.Sprintf("t%d", i)
		if !used[id] {
			return id
		}
	}
}
