// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
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

// TestGoalRenderer_ListAction pins the list call header and the
// {"active":…, "queued":[…]} result summary — it must describe the goal list,
// never claim "No current goal".
func TestGoalRenderer_ListAction(t *testing.T) {
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
}

// TestGoalRenderer_CancelAction pins the cancel call header and the
// {"cancelled":{…}} result summary.
func TestGoalRenderer_CancelAction(t *testing.T) {
	r := GoalRenderer{}
	ctx := tuirender.RenderContext{}
	call := r.RenderCall(map[string]any{"action": "cancel", "goalId": "happy.fox"}, ctx)
	if !strings.Contains(call, "Cancelled goal") || !strings.Contains(call, "happy.fox") {
		t.Errorf("cancel call = %q", call)
	}
	if res := r.RenderResult(`{"cancelled":{"id":"g1","name":"happy.fox","objective":"Write docs"}}`, ctx); !strings.Contains(res, "Cancelled") || !strings.Contains(res, "happy.fox") {
		t.Errorf("cancel result = %q", res)
	}
}

// TestGoalRenderer_ReorderAction pins the reorder call header and the
// {"queued":[…]} result listing the queue in its new order.
func TestGoalRenderer_ReorderAction(t *testing.T) {
	r := GoalRenderer{}
	ctx := tuirender.RenderContext{}
	call := r.RenderCall(map[string]any{"action": "reorder", "goalId": "calm.owl", "direction": "up"}, ctx)
	if !strings.Contains(call, "Reordered goal") || !strings.Contains(call, "calm.owl") || !strings.Contains(call, "up") {
		t.Errorf("reorder call = %q", call)
	}
	res := r.RenderResult(`{"queued":[{"name":"calm.owl","objective":"B"},{"name":"happy.fox","objective":"A"}]}`, ctx)
	if strings.Index(res, "calm.owl") > strings.Index(res, "happy.fox") {
		t.Errorf("reorder result should list the queue in its new order: %q", res)
	}
}

// TestGoalRenderer_QueuedCreateResults pins creates that only enqueue: a
// bare {"queued":n} result must report the queued count (never "No current
// goal"), and a multi-create with an activated goal reports both.
func TestGoalRenderer_QueuedCreateResults(t *testing.T) {
	r := GoalRenderer{}
	ctx := tuirender.RenderContext{}
	res := r.RenderResult(`{"queued":2}`, ctx)
	if !strings.Contains(res, "2 goals queued") || strings.Contains(res, "No current goal") {
		t.Errorf("queued-only create result = %q", res)
	}
	res = r.RenderResult(`{"goal":{"objective":"Main","status":"active"},"queued":2}`, ctx)
	if !strings.Contains(res, "Main") || !strings.Contains(res, "2 queued") {
		t.Errorf("multi-create result = %q", res)
	}
}

// TestRenderGoalSummary_PlainTextPassthrough pins Bug A: plain-text
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

// TestGoalRenderer_SnapshotUsesShortName guards the timeline-flooding fix
// a goal snapshot must render the friendly short name, never the
// raw unbounded objective; the stats suffix stays intact.
func TestGoalRenderer_SnapshotUsesShortName(t *testing.T) {
	r := GoalRenderer{}
	longObjective := "Wire the go-lemon generated TCL parser into tcl2go as a drop-in replacement for the hand-written tcl.ParseCommands parser, complete with every constraint rule"
	tests := []struct {
		name         string
		payload      string
		wantContains []string
		wantMissing  []string
	}{
		{
			name:    "named snapshot shows short name not objective",
			payload: `{"goal":{"name":"honest.zebra","objective":"` + longObjective + `","status":"active","turnsUsed":3,"tokensUsed":1200,"wallClockMs":65000}}`,
			wantContains: []string{
				"honest.zebra",
				"3 turns", "1.2k tokens", "1m05s",
			},
			wantMissing: []string{"Wire the go-lemon", "tcl.ParseCommands"},
		},
		{
			name:    "unnamed snapshot falls back to truncated objective",
			payload: `{"goal":{"objective":"` + longObjective + `","status":"active","turnsUsed":1,"tokensUsed":10,"wallClockMs":1000}}`,
			wantContains: []string{
				"Wire the go-lemon generated TCL parser", // truncated prefix
				"…",
			},
			wantMissing: []string{"tcl.ParseCommands parser, complete"},
		},
		{
			name:         "todo stats suffix intact with name",
			payload:      `{"goal":{"name":"fair.puma","objective":"` + longObjective + `","status":"active","turnsUsed":2,"tokensUsed":50,"wallClockMs":2000,"todos":[{"status":"done"},{"status":"pending"}]}}`,
			wantContains: []string{"fair.puma", "todos 1/2"},
			wantMissing:  []string{"Wire the go-lemon"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := r.RenderResult(tt.payload, tuirender.RenderContext{})
			for _, want := range tt.wantContains {
				if !strings.Contains(res, want) {
					t.Errorf("result %q missing %q", res, want)
				}
			}
			for _, banned := range tt.wantMissing {
				if strings.Contains(res, banned) {
					t.Errorf("result %q must not contain %q (objective flooding)", res, banned)
				}
			}
		})
	}
}

// TestGoalRenderer_ListActiveUsesShortName: the list head line had the same
// objective-flooding bug.
func TestGoalRenderer_ListActiveUsesShortName(t *testing.T) {
	r := GoalRenderer{}
	payload := `{"active":{"name":"kind.fox","objective":"Wire the go-lemon generated TCL parser into tcl2go as a drop-in replacement for the hand-written tcl.ParseCommands parser","status":"active"},"queued":[],"count":1}`
	res := r.RenderResult(payload, tuirender.RenderContext{})
	if !strings.Contains(res, "kind.fox") {
		t.Errorf("result %q missing short name", res)
	}
	if strings.Contains(res, "Wire the go-lemon") {
		t.Errorf("result %q must not contain the raw objective", res)
	}
}

// TestGoalRenderer_CreateResultShowsQueueTotals pins the "total number of
// goals" requirement: a create result carries queued (new this call) and
// totalQueued (queue depth after the call); the summary must show both when
// the queue already held goals, and stay unchanged when it did not.
func TestGoalRenderer_CreateResultShowsQueueTotals(t *testing.T) {
	r := GoalRenderer{}
	ctx := tuirender.RenderContext{}

	res := r.RenderResult(`{"goal":{"objective":"Main","status":"active"},"queued":2,"totalQueued":5}`, ctx)
	if !strings.Contains(res, "2 queued (5 total)") {
		t.Errorf("multi-create result must show total queue depth: %q", res)
	}

	res = r.RenderResult(`{"queued":2,"totalQueued":5}`, ctx)
	if !strings.Contains(res, "2 goals queued (5 total)") {
		t.Errorf("queued-only create result must show total queue depth: %q", res)
	}

	// No pre-existing queue: total equals the new count → classic summary.
	res = r.RenderResult(`{"goal":{"objective":"Main","status":"active"},"queued":2,"totalQueued":2}`, ctx)
	if !strings.Contains(res, "2 queued") || strings.Contains(res, "total") {
		t.Errorf("create without pre-existing queue must not add a total: %q", res)
	}
}

// TestGoalRenderer_FreshGoalOmitsZeroStats pins the elapsed-summary cleanup:
// a freshly created goal (no turns/tokens/elapsed yet) must not render the
// all-zero "· 0 turns · 0 tokens · 0s" noise; a goal that has run keeps its
// elapsed stats.
func TestGoalRenderer_FreshGoalOmitsZeroStats(t *testing.T) {
	r := GoalRenderer{}
	ctx := tuirender.RenderContext{}

	res := r.RenderResult(`{"goal":{"name":"honest.zebra","objective":"Fix tests","status":"active","turnsUsed":0,"tokensUsed":0,"wallClockMs":0}}`, ctx)
	if !strings.Contains(res, "honest.zebra") {
		t.Errorf("fresh goal result = %q", res)
	}
	if strings.Contains(res, "0 turns") || strings.Contains(res, "0 tokens") {
		t.Errorf("fresh goal must not show zero stats: %q", res)
	}

	res = r.RenderResult(`{"goal":{"name":"honest.zebra","objective":"Fix tests","status":"active","turnsUsed":3,"tokensUsed":1200,"wallClockMs":65000}}`, ctx)
	for _, want := range []string{"3 turns", "1.2k tokens", "1m05s"} {
		if !strings.Contains(res, want) {
			t.Errorf("running goal must keep stat %q: %q", want, res)
		}
	}
}

// TestGoalRenderer_RenderPartial pins the streaming-progress preview: while a
// goal call's arguments are still streaming, the body shows the objectives
// received so far (numbered for batch creates) so progress is visible like
// other streaming tools.
func TestGoalRenderer_RenderPartial(t *testing.T) {
	r := GoalRenderer{}
	ctx := tuirender.RenderContext{IsPartial: true}
	strip := func(args map[string]any) string {
		return ansi.Strip(r.RenderPartial(args, ctx))
	}

	if got := strip(map[string]any{}); got != "" {
		t.Errorf("no args yet must render nothing, got %q", got)
	}
	if got := strip(map[string]any{"action": "create"}); got != "" {
		t.Errorf("create without objective must render nothing, got %q", got)
	}
	if got := strip(map[string]any{"action": "create", "objective": "Fix tests"}); got != "Fix tests" {
		t.Errorf("single objective partial = %q", got)
	}
	got := strip(map[string]any{"action": "create", "objectives": []any{"Fix tests", "Run suite"}})
	if !strings.Contains(got, "1. Fix tests") || !strings.Contains(got, "2. Run suite") {
		t.Errorf("batch partial must number objectives: %q", got)
	}
	if got := strip(map[string]any{"action": "update", "status": "complete"}); !strings.Contains(got, "complete") {
		t.Errorf("update partial = %q", got)
	}
}
