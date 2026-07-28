// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/tuirender"
)

func TestGoalRenderer_Create(t *testing.T) {
	r := GoalRenderer{}
	call := r.RenderCall(map[string]any{"action": "create", "objective": "Fix tests"}, tuirender.RenderContext{})
	if !strings.Contains(call, "Started goal") {
		t.Errorf("call = %q", call)
	}
	res := r.RenderResult(`{"goal":{"objective":"Fix tests","status":"active","turnsUsed":0,"tokensUsed":0,"wallClockMs":0}}`, tuirender.RenderContext{})
	if !strings.Contains(res, "Fix tests") {
		t.Errorf("result = %q", res)
	}
	if r.PreviewLines() != 3 || r.HideResultWhenCollapsed() {
		t.Error("unexpected renderer meta")
	}
}

func TestGoalRenderer_Update(t *testing.T) {
	r := GoalRenderer{}
	for status, want := range map[string]string{
		"complete": "complete",
		"blocked":  "blocked",
		"paused":   "Paused",
		"active":   "Resumed",
		"unknown":  "Updated",
	} {
		call := r.RenderCall(map[string]any{"action": "update", "status": status}, tuirender.RenderContext{})
		if !strings.Contains(call, want) {
			t.Errorf("status %s call = %q", status, call)
		}
	}
}

func TestGoalRenderer_Get(t *testing.T) {
	r := GoalRenderer{}
	if !strings.Contains(r.RenderCall(map[string]any{"action": "get"}, tuirender.RenderContext{}), "Checked goal") {
		t.Error("unexpected call")
	}
	res := r.RenderResult(`{"goal":null}`, tuirender.RenderContext{})
	if !strings.Contains(res, "No current goal") {
		t.Errorf("result = %q", res)
	}
}

func TestGoalRenderer_SetBudget(t *testing.T) {
	r := GoalRenderer{}
	call := r.RenderCall(map[string]any{"action": "set_budget", "value": 5.0, "unit": "turns"}, tuirender.RenderContext{})
	if !strings.Contains(call, "Set goal budget") || !strings.Contains(call, "turns") {
		t.Errorf("call = %q", call)
	}
}

// TestGoalRenderer_BatchCreate pins the batch-create call header: the first
// objective is shown with a "+N more" suffix instead of an empty detail.
func TestGoalRenderer_BatchCreate(t *testing.T) {
	r := GoalRenderer{}
	call := r.RenderCall(map[string]any{"action": "create", "objectives": []any{"Fix tests", "Run suite", "Commit"}}, tuirender.RenderContext{})
	if !strings.Contains(call, "Started goal") || !strings.Contains(call, "Fix tests") || !strings.Contains(call, "+2 more") {
		t.Errorf("batch create call = %q", call)
	}
}

// TestGoalRenderer_UpdateBlockedShowsReason pins the blocked/paused headers
// carrying the (truncated) justification so the timeline shows WHY the goal
// yields.
func TestGoalRenderer_UpdateBlockedShowsReason(t *testing.T) {
	r := GoalRenderer{}
	call := r.RenderCall(map[string]any{"action": "update", "status": "blocked", "reason": "rate limited by provider"}, tuirender.RenderContext{})
	if !strings.Contains(call, "blocked") || !strings.Contains(call, "rate limited") {
		t.Errorf("blocked call = %q", call)
	}
}

// TestGoalRenderer_TodoActions is the regression for the status timeline
// showing "◆ Goal / No current goal" for every add_todo call: the call header
// must name the todo title and the {"todo":…} result must summarize the added
// item — never claim there is no goal.
func TestGoalRenderer_TodoActions(t *testing.T) {
	r := GoalRenderer{}
	call := r.RenderCall(map[string]any{"action": "add_todo", "todoTitle": "Write tests"}, tuirender.RenderContext{})
	if !strings.Contains(call, "Added todo") || !strings.Contains(call, "Write tests") {
		t.Errorf("add_todo call = %q", call)
	}
	res := r.RenderResult(`{"todo":{"id":"t1","title":"Write tests","status":"pending"}}`, tuirender.RenderContext{})
	if !strings.Contains(res, "t1") || !strings.Contains(res, "Write tests") {
		t.Errorf("add_todo result = %q", res)
	}
	if strings.Contains(res, "No current goal") {
		t.Errorf("add_todo result must not claim 'No current goal': %q", res)
	}

	call = r.RenderCall(map[string]any{"action": "update_todo", "todoId": "t1", "todoStatus": "done"}, tuirender.RenderContext{})
	if !strings.Contains(call, "Updated todo") || !strings.Contains(call, "t1") || !strings.Contains(call, "done") {
		t.Errorf("update_todo call = %q", call)
	}
	res = r.RenderResult(`{"goal":{"objective":"Fix tests","status":"active","todos":[{"id":"t1","title":"a","status":"done"},{"id":"t2","title":"b","status":"pending"}]}}`, tuirender.RenderContext{})
	if !strings.Contains(res, "todos 1/2") {
		t.Errorf("update_todo result should show todo progress, got %q", res)
	}
}

// TestGoalRenderer_QueueActions pins the call headers and result summaries
// for the goal-list actions (list / cancel / reorder) and for creates that
// only enqueue: none of them may render as "No current goal".
func TestGoalRenderer_QueueActions(t *testing.T) {
	r := GoalRenderer{}
	ctx := tuirender.RenderContext{}

	if call := r.RenderCall(map[string]any{"action": "list"}, ctx); !strings.Contains(call, "Listed goals") {
		t.Errorf("list call = %q", call)
	}
	res := r.RenderResult(`{"active":{"objective":"Fix tests","status":"active"},"queued":[{"id":"g1","name":"happy.fox","objective":"A"},{"id":"g2","name":"calm.owl","objective":"B"}],"count":2}`, ctx)
	for _, want := range []string{"Fix tests", "2 queued", "happy.fox", "calm.owl"} {
		if !strings.Contains(res, want) {
			t.Errorf("list result missing %q: %q", want, res)
		}
	}
	if res := r.RenderResult(`{"active":null,"queued":[],"count":0}`, ctx); !strings.Contains(res, "No active goal") {
		t.Errorf("empty list result = %q", res)
	}

	call := r.RenderCall(map[string]any{"action": "cancel", "goalId": "happy.fox"}, ctx)
	if !strings.Contains(call, "Cancelled goal") || !strings.Contains(call, "happy.fox") {
		t.Errorf("cancel call = %q", call)
	}
	if res := r.RenderResult(`{"cancelled":{"id":"g1","name":"happy.fox","objective":"Write docs"}}`, ctx); !strings.Contains(res, "Cancelled") || !strings.Contains(res, "happy.fox") {
		t.Errorf("cancel result = %q", res)
	}

	call = r.RenderCall(map[string]any{"action": "reorder", "goalId": "calm.owl", "direction": "up"}, ctx)
	if !strings.Contains(call, "Reordered goal") || !strings.Contains(call, "calm.owl") || !strings.Contains(call, "up") {
		t.Errorf("reorder call = %q", call)
	}
	res = r.RenderResult(`{"queued":[{"name":"calm.owl","objective":"B"},{"name":"happy.fox","objective":"A"}]}`, ctx)
	if strings.Index(res, "calm.owl") > strings.Index(res, "happy.fox") {
		t.Errorf("reorder result should list the queue in its new order: %q", res)
	}

	// Create while another goal is active: everything lands in the queue.
	res = r.RenderResult(`{"queued":2}`, ctx)
	if !strings.Contains(res, "2 goals queued") || strings.Contains(res, "No current goal") {
		t.Errorf("queued-only create result = %q", res)
	}
	// Batch create with an activated goal reports the queue count too.
	res = r.RenderResult(`{"goal":{"objective":"Main","status":"active"},"queued":2}`, ctx)
	if !strings.Contains(res, "Main") || !strings.Contains(res, "2 queued") {
		t.Errorf("multi-create result = %q", res)
	}
}

// TestRenderGoalSummary_PlainTextPassthrough pins bugs.md Bug A: plain-text
// results (e.g. "Goal marked complete." + the verification evidence block,
// or "Goal blocked: …") must render as-is instead of disappearing behind
// the JSON parse failure.
func TestRenderGoalSummary_PlainTextPassthrough(t *testing.T) {
	out := "Goal marked complete.\n\nVerification passed in 12.3s (timeout 2m0s):\n$ go test ./...\nok  \tpkg\t0.3s"
	if got := renderGoalSummary(out); got != out {
		t.Errorf("plain-text result must pass through unchanged, got %q", got)
	}
	if got := renderGoalSummary("Goal blocked: rate limited"); got != "Goal blocked: rate limited" {
		t.Errorf("short plain-text result must pass through unchanged, got %q", got)
	}
}

func TestExtractArg(t *testing.T) {
	if got := extractArg(map[string]any{"k": "v"}, "k"); got != "v" {
		t.Errorf("string arg = %q", got)
	}
	if got := extractArg(map[string]any{"k": 3.5}, "k"); got != "3.5" {
		t.Errorf("float arg = %q", got)
	}
	if got := extractArg(map[string]any{"k": true}, "k"); got != "true" {
		t.Errorf("bool arg = %q", got)
	}
	if got := extractArg(map[string]any{}, "k"); got != "" {
		t.Errorf("missing arg = %q", got)
	}
}

func TestFormatTokens(t *testing.T) {
	cases := map[int]string{
		500:       "500",
		1500:      "1.5k",
		2_500_000: "2.5M",
	}
	for in, want := range cases {
		if got := formatTokens(in); got != want {
			t.Errorf("formatTokens(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatElapsed(t *testing.T) {
	if got := formatElapsed(65000); got != "1m05s" {
		t.Errorf("formatElapsed = %q", got)
	}
}
