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
	"time"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/tui"
)

func TestGoalCommand_parseArgs(t *testing.T) {
	cmd := &GoalCommand{}
	cases := []struct {
		args []string
		want string
	}{
		// Bare /goal → interactive create flow (item 2)
		{args: []string{}, want: "create-interactive"},
		// Colon subcommands
		{args: []string{"status"}, want: "status"},
		{args: []string{"pause"}, want: "pause"},
		{args: []string{"resume"}, want: "resume"},
		{args: []string{"cancel"}, want: "cancel"},
		// /goal:cancel scope variants — bare and :current cancel the active
		// goal, :all additionally wipes the queue, anything else is a hint.
		{args: []string{"cancel", "current"}, want: "cancel"},
		{args: []string{"cancel", "all"}, want: "cancel-all"},
		{args: []string{"cancel", "ALL"}, want: "cancel-all"},
		{args: []string{"cancel", "bogus"}, want: "error"},
		// /goal:pause scope variants — bare and :current pause the active
		// goal now, :next arms the pause-after-completion one-shot,
		// :next:off disarms it, anything else is a hint.
		{args: []string{"pause", "current"}, want: "pause"},
		{args: []string{"pause", "next"}, want: "pause-next"},
		{args: []string{"pause", "NEXT"}, want: "pause-next"},
		{args: []string{"pause", "next", "off"}, want: "pause-next-off"},
		{args: []string{"pause", "bogus"}, want: "error"},
		{args: []string{"manage"}, want: "manage"},
		// /goal:new — bare and with text
		{args: []string{"new"}, want: "create-interactive"},
		{args: []string{"new", "fix tests"}, want: "create"},
		// /goal:next — bare and with text
		{args: []string{"next"}, want: "next-interactive"},
		{args: []string{"next", "fix tests"}, want: "next-add"},
		// /goal:replace — bare and with text
		{args: []string{"replace"}, want: "replace-interactive"},
		{args: []string{"replace", "new goal"}, want: "replace"},
		// /goal:reorder
		{args: []string{"reorder", "1B,2A"}, want: "reorder"},
		{args: []string{"reorder"}, want: "error"},
		// Free text with no keyword → create
		{args: []string{"just a goal"}, want: "create"},
	}
	for _, tc := range cases {
		got := cmd.parseArgs(tc.args)
		if got.kind != tc.want {
			t.Errorf("parseArgs(%v).kind = %q, want %q", tc.args, got.kind, tc.want)
		}
	}
}

func TestGoalCommand_parseArgs_EscapesReservedWords(t *testing.T) {
	// With colon nomenclature, reserved words live cleanly in the text arg:
	// /goal:new:pause the server → args=["new","pause the server"]
	cmd := &GoalCommand{}
	got := cmd.parseArgs([]string{"new", "pause the server"})
	if got.kind != "create" || got.objective != "pause the server" {
		t.Errorf("unexpected parse: %+v", got)
	}
}

// TestGoalCommand_parseNextArgs verifies /goal:next argument parsing: the
// optional leading placement token (first|last, default first) and context
// token (fresh|reuse) are consumed in any order; an empty remainder falls
// back to the interactive flow carrying the parsed placement.
func TestGoalCommand_parseNextArgs(t *testing.T) {
	cmd := &GoalCommand{}
	cases := []struct {
		name      string
		args      []string
		kind      string
		placement goalPlacement
		mode      string
		objective string
	}{
		{name: "bare", args: []string{"next"},
			kind: "next-interactive", placement: placementNext},
		{name: "text only", args: []string{"next", "fix tests"},
			kind: "next-add", placement: placementNext, objective: "fix tests"},
		{name: "first explicit", args: []string{"next", "first", "fix tests"},
			kind: "next-add", placement: placementNext, objective: "fix tests"},
		{name: "first bare", args: []string{"next", "first"},
			kind: "next-interactive", placement: placementNext},
		{name: "last", args: []string{"next", "last", "fix tests"},
			kind: "next-add", placement: placementLast, objective: "fix tests"},
		{name: "last bare", args: []string{"next", "last"},
			kind: "next-interactive", placement: placementLast},
		{name: "fresh default first", args: []string{"next", "fresh", "audit"},
			kind: "next-add", placement: placementNext, mode: "fresh", objective: "audit"},
		{name: "last then fresh", args: []string{"next", "last", "fresh", "audit"},
			kind: "next-add", placement: placementLast, mode: "fresh", objective: "audit"},
		{name: "fresh then last", args: []string{"next", "fresh", "last", "audit"},
			kind: "next-add", placement: placementLast, mode: "fresh", objective: "audit"},
		{name: "reuse then first", args: []string{"next", "reuse", "first", "audit"},
			kind: "next-add", placement: placementNext, mode: "reuse", objective: "audit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := cmd.parseArgs(tc.args)
			if got.kind != tc.kind || got.placement != tc.placement ||
				got.contextMode != tc.mode || got.objective != tc.objective {
				t.Errorf("parseArgs(%v) = %+v, want kind=%q placement=%v mode=%q objective=%q",
					tc.args, got, tc.kind, tc.placement, tc.mode, tc.objective)
			}
		})
	}
}

func TestGoalCommand_CreateGoal(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	ctx := testContext()

	err := cmd.Run(ctx, []string{"fix tests"})
	if err != nil {
		t.Fatal(err)
	}
	if mode.GetGoal().Goal == nil {
		t.Fatal("goal should be created")
	}
	if mode.GetGoal().Goal.Objective != "fix tests" {
		t.Errorf("objective = %q", mode.GetGoal().Goal.Objective)
	}
}

func TestGoalCommand_CreateGoal_Duplicate_FirstOrLastPrompt(t *testing.T) {
	// With a goal already active, a second create no longer errors; it opens
	// the item-4 "first or last" selector. Choosing "last" queues it.
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))
	cmd := &GoalCommand{Mode: mode, Queue: queue}
	ctx := testContext()

	if err := cmd.Run(ctx, []string{"first"}); err != nil {
		t.Fatal(err)
	}

	ctx.SelectOptionFunc = func(_ string, _ []tui.SelectorItem, _ string, cb func(string, bool)) {
		cb("last", true) // queue it, keep active goal
	}
	if err := cmd.Run(ctx, []string{"second"}); err != nil {
		t.Fatal(err)
	}
	if mode.GetGoal().Goal.Objective != "first" {
		t.Errorf("active goal changed unexpectedly: %q", mode.GetGoal().Goal.Objective)
	}
	queued, _ := queue.Read()
	if len(queued) != 1 || queued[0].Objective != "second" {
		t.Errorf("expected second to be queued, got %v", queued)
	}
}

func TestGoalCommand_PauseResume(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	ctx := testContext()

	cmd.Run(ctx, []string{"fix tests"})
	if err := cmd.Run(ctx, []string{"pause"}); err != nil {
		t.Fatal(err)
	}
	if mode.GetGoal().Goal.Status != goal.GoalPaused {
		t.Errorf("status = %q", mode.GetGoal().Goal.Status)
	}
	if err := cmd.Run(ctx, []string{"resume"}); err != nil {
		t.Fatal(err)
	}
	if mode.GetGoal().Goal.Status != goal.GoalActive {
		t.Errorf("status = %q", mode.GetGoal().Goal.Status)
	}
}

// /goal:pause:current is an explicit alias for the bare /goal:pause — it
// pauses the active goal immediately.
func TestGoalCommand_PauseCurrent(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	ctx := testContext()

	cmd.Run(ctx, []string{"fix tests"})
	if err := cmd.Run(ctx, []string{"pause", "current"}); err != nil {
		t.Fatal(err)
	}
	if mode.GetGoal().Goal.Status != goal.GoalPaused {
		t.Errorf("status = %q, want paused", mode.GetGoal().Goal.Status)
	}
}

// /goal:pause:next arms the pause-after-completion one-shot WITHOUT pausing
// the active goal; :next:off disarms it again.
func TestGoalCommand_PauseNext(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	ctx := testContext()

	cmd.Run(ctx, []string{"fix tests"})

	if err := cmd.Run(ctx, []string{"pause", "next"}); err != nil {
		t.Fatal(err)
	}
	g := mode.GetGoal().Goal
	if !g.PauseAfterComplete {
		t.Error("PauseAfterComplete = false after /goal:pause:next, want armed")
	}
	if g.Status != goal.GoalActive {
		t.Errorf("status = %q after /goal:pause:next, want still active", g.Status)
	}
	if out := ctx.OutputBuffer.String(); !strings.Contains(out, "armed") {
		t.Errorf("arm output should mention the armed one-shot: %q", out)
	}

	// Re-arming is idempotent (no duplicate event, no error).
	if err := cmd.Run(ctx, []string{"pause", "next"}); err != nil {
		t.Fatal(err)
	}
	if out := ctx.OutputBuffer.String(); !strings.Contains(out, "already armed") {
		t.Errorf("re-arm output should say already armed: %q", out)
	}

	if err := cmd.Run(ctx, []string{"pause", "next", "off"}); err != nil {
		t.Fatal(err)
	}
	if mode.GetGoal().Goal.PauseAfterComplete {
		t.Error("PauseAfterComplete = true after /goal:pause:next:off, want disarmed")
	}
}

// /goal:pause:next without a goal is an error, not a panic.
func TestGoalCommand_PauseNext_NoGoal(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	ctx := testContext()

	if err := cmd.Run(ctx, []string{"pause", "next"}); err == nil {
		t.Fatal("expected error when there is no current goal")
	}
	if err := cmd.Run(ctx, []string{"pause", "next", "off"}); err == nil {
		t.Fatal("expected error when there is no current goal")
	}
}

// CompleteArgs must offer the nested /goal:pause:<scope> variants once the
// user typed "pause:".
func TestGoalCommand_CompleteArgs_PauseScopes(t *testing.T) {
	cmd := &GoalCommand{}
	ctx := testContext()

	// Level-2: /goal:pause:<tab> lists the scopes, fully spelled out.
	vals := cmd.CompleteArgs(ctx, "pause:")
	if len(vals) != 3 {
		t.Fatalf("CompleteArgs(pause:) = %+v, want 3 entries", vals)
	}
	want := []string{"pause:current", "pause:next", "pause:next:off"}
	for i, w := range want {
		if vals[i].Value != w {
			t.Errorf("pause scope values = %+v, want %v", vals, want)
			break
		}
	}

	// Prefix filtering at the nested level.
	vals = cmd.CompleteArgs(ctx, "pause:next:")
	if len(vals) != 1 || vals[0].Value != "pause:next:off" {
		t.Errorf("CompleteArgs(pause:next:) = %+v, want pause:next:off", vals)
	}

	// Top-level behavior is untouched: /goal:pau still completes "pause".
	vals = cmd.CompleteArgs(ctx, "pau")
	if len(vals) != 1 || vals[0].Value != "pause" {
		t.Errorf("CompleteArgs(pau) = %+v, want pause", vals)
	}
}

func TestGoalCommand_Cancel(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	ctx := testContext()

	cmd.Run(ctx, []string{"fix tests"})
	if err := cmd.Run(ctx, []string{"cancel"}); err != nil {
		t.Fatal(err)
	}
	if mode.GetGoal().Goal != nil {
		t.Error("goal should be nil after cancel")
	}
}

// /goal:cancel:current is an explicit alias for the bare /goal:cancel.
func TestGoalCommand_CancelCurrent(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	ctx := testContext()

	cmd.Run(ctx, []string{"fix tests"})
	if err := cmd.Run(ctx, []string{"cancel", "current"}); err != nil {
		t.Fatal(err)
	}
	if mode.GetGoal().Goal != nil {
		t.Error("goal should be nil after /goal:cancel:current")
	}
}

// A cancel with queued goals must tell the user the successor waits paused —
// it is promoted but never auto-started.
func TestGoalCommand_Cancel_NotesPausedSuccessor(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "queue.json"))
	cmd := &GoalCommand{Mode: mode, Queue: queue}
	ctx := testContext()

	cmd.Run(ctx, []string{"first"})
	if _, err := queue.AppendGoal(goal.UpcomingGoalInput{Objective: "second"}); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Run(ctx, []string{"cancel"}); err != nil {
		t.Fatal(err)
	}
	out := ctx.OutputBuffer.String()
	if !strings.Contains(out, "Goal cancelled.") {
		t.Errorf("cancel output = %q", out)
	}
	if !strings.Contains(out, "paused") {
		t.Errorf("cancel with queued goals must mention the paused successor: %q", out)
	}
	// The queue still holds the successor: promotion is the app's job, and it
	// is PAUSED — the command must not have consumed it.
	queued, _ := queue.Read()
	if len(queued) != 1 || queued[0].Objective != "second" {
		t.Errorf("queue after cancel = %+v", queued)
	}
}

// /goal:cancel:all discards the active goal AND wipes the queue. The queue is
// cleared first so the async successor promotion no-ops.
func TestGoalCommand_CancelAll(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "queue.json"))
	cmd := &GoalCommand{Mode: mode, Queue: queue}
	ctx := testContext()

	cmd.Run(ctx, []string{"first"})
	if _, err := queue.AppendGoal(goal.UpcomingGoalInput{Objective: "second"}); err != nil {
		t.Fatal(err)
	}
	if _, err := queue.AppendGoal(goal.UpcomingGoalInput{Objective: "third"}); err != nil {
		t.Fatal(err)
	}

	if err := cmd.Run(ctx, []string{"cancel", "all"}); err != nil {
		t.Fatal(err)
	}
	if mode.GetGoal().Goal != nil {
		t.Error("goal should be nil after /goal:cancel:all")
	}
	queued, _ := queue.Read()
	if len(queued) != 0 {
		t.Errorf("queue should be empty after /goal:cancel:all, got %+v", queued)
	}
	out := ctx.OutputBuffer.String()
	if !strings.Contains(out, "2 queued goal(s) cleared") {
		t.Errorf("cancel-all output = %q", out)
	}
}

// /goal:cancel:all with no active goal still clears the queue.
func TestGoalCommand_CancelAll_QueueOnly(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "queue.json"))
	cmd := &GoalCommand{Mode: mode, Queue: queue}
	ctx := testContext()

	if _, err := queue.AppendGoal(goal.UpcomingGoalInput{Objective: "queued"}); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Run(ctx, []string{"cancel", "all"}); err != nil {
		t.Fatal(err)
	}
	queued, _ := queue.Read()
	if len(queued) != 0 {
		t.Errorf("queue should be empty, got %+v", queued)
	}
	if out := ctx.OutputBuffer.String(); !strings.Contains(out, "Cleared 1 queued goal(s).") {
		t.Errorf("output = %q", out)
	}
}

// /goal:cancel:all with neither an active goal nor queued goals errors.
func TestGoalCommand_CancelAll_NothingToCancel(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "queue.json"))
	cmd := &GoalCommand{Mode: mode, Queue: queue}
	ctx := testContext()

	if err := cmd.Run(ctx, []string{"cancel", "all"}); err == nil {
		t.Fatal("expected error when there is nothing to cancel")
	}
}

// CompleteArgs must offer the nested /goal:cancel:<scope> variants once the
// user typed "cancel:", and keep the top-level behavior for plain prefixes.
func TestGoalCommand_CompleteArgs_CancelScopes(t *testing.T) {
	cmd := &GoalCommand{}
	ctx := testContext()

	// Level-2: /goal:cancel:<tab> lists both scopes, fully spelled out.
	vals := cmd.CompleteArgs(ctx, "cancel:")
	if len(vals) != 2 {
		t.Fatalf("CompleteArgs(cancel:) = %+v, want 2 entries", vals)
	}
	if vals[0].Value != "cancel:current" || vals[1].Value != "cancel:all" {
		t.Errorf("cancel scope values = %+v", vals)
	}

	// Prefix filtering at the nested level.
	vals = cmd.CompleteArgs(ctx, "cancel:a")
	if len(vals) != 1 || vals[0].Value != "cancel:all" {
		t.Errorf("CompleteArgs(cancel:a) = %+v, want cancel:all only", vals)
	}
	vals = cmd.CompleteArgs(ctx, "cancel:cu")
	if len(vals) != 1 || vals[0].Value != "cancel:current" {
		t.Errorf("CompleteArgs(cancel:cu) = %+v, want cancel:current only", vals)
	}

	// Unknown scope → nothing.
	if vals := cmd.CompleteArgs(ctx, "cancel:zzz"); len(vals) != 0 {
		t.Errorf("CompleteArgs(cancel:zzz) = %+v, want none", vals)
	}

	// Top-level behavior is untouched: /goal:can still completes "cancel".
	vals = cmd.CompleteArgs(ctx, "can")
	if len(vals) != 1 || vals[0].Value != "cancel" {
		t.Errorf("CompleteArgs(can) = %+v, want cancel", vals)
	}
}

func TestGoalCommand_CompleteArgs_NextOptions(t *testing.T) {
	cmd := &GoalCommand{}
	ctx := testContext()

	// Level-2: /goal:next:<tab> lists the placement and context options,
	// fully spelled out (mirrors the /goal:cancel: scopes).
	vals := cmd.CompleteArgs(ctx, "next:")
	if len(vals) != 4 {
		t.Fatalf("CompleteArgs(next:) = %+v, want 4 entries", vals)
	}
	want := []string{"next:first", "next:last", "next:fresh", "next:reuse"}
	for i, w := range want {
		if vals[i].Value != w {
			t.Errorf("CompleteArgs(next:)[%d] = %q, want %q", i, vals[i].Value, w)
		}
	}

	// Prefix filtering at the nested level.
	if vals := cmd.CompleteArgs(ctx, "next:l"); len(vals) != 1 || vals[0].Value != "next:last" {
		t.Errorf("CompleteArgs(next:l) = %+v, want next:last only", vals)
	}
	if vals := cmd.CompleteArgs(ctx, "next:f"); len(vals) != 2 ||
		vals[0].Value != "next:first" || vals[1].Value != "next:fresh" {
		t.Errorf("CompleteArgs(next:f) = %+v, want next:first and next:fresh", vals)
	}

	// Unknown option → nothing.
	if vals := cmd.CompleteArgs(ctx, "next:zzz"); len(vals) != 0 {
		t.Errorf("CompleteArgs(next:zzz) = %+v, want none", vals)
	}

	// Top-level behavior untouched: /goal:nex still completes "next".
	if vals := cmd.CompleteArgs(ctx, "nex"); len(vals) != 1 || vals[0].Value != "next" {
		t.Errorf("CompleteArgs(nex) = %+v, want next", vals)
	}
}

func TestGoalCommand_Replace(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	ctx := testContext()

	cmd.Run(ctx, []string{"first"})

	// /goal:replace:second now asks for confirmation first.
	ctx.SelectOptionFunc = func(_ string, _ []tui.SelectorItem, _ string, cb func(string, bool)) {
		cb("replace", true)
	}
	if err := cmd.Run(ctx, []string{"replace", "second"}); err != nil {
		t.Fatal(err)
	}
	if mode.GetGoal().Goal.Objective != "second" {
		t.Errorf("objective = %q", mode.GetGoal().Goal.Objective)
	}
}

func TestGoalCommand_Replace_CancelKeepsOriginal(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	ctx := testContext()

	cmd.Run(ctx, []string{"first"})
	ctx.SelectOptionFunc = func(_ string, _ []tui.SelectorItem, _ string, cb func(string, bool)) {
		cb("cancel", true)
	}
	if err := cmd.Run(ctx, []string{"replace", "second"}); err != nil {
		t.Fatal(err)
	}
	if mode.GetGoal().Goal.Objective != "first" {
		t.Errorf("objective = %q, want first", mode.GetGoal().Goal.Objective)
	}
}

func TestGoalCommand_Replace_NoActiveGoal(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	ctx := testContext()

	err := cmd.Run(ctx, []string{"replace", "second"})
	if err == nil {
		t.Fatal("expected error when no active goal to replace")
	}
}

func TestGoalCommand_Status(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	ctx := testContext()

	cmd.Run(ctx, []string{"fix tests"})
	if err := cmd.Run(ctx, []string{"status"}); err != nil {
		t.Fatal(err)
	}
	out := ctx.OutputBuffer.String()
	if !strings.Contains(out, "fix tests") {
		t.Errorf("status output missing goal: %s", out)
	}
}

func TestGoalCommand_StatusEmpty(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	ctx := testContext()

	if err := cmd.Run(ctx, []string{"status"}); err != nil {
		t.Fatal(err)
	}
	out := ctx.OutputBuffer.String()
	if !strings.Contains(out, "No current goal") {
		t.Errorf("unexpected status output: %s", out)
	}
}

func TestGoalCommand_NextAndReorder(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "queue.json"))
	cmd := &GoalCommand{Mode: mode, Queue: queue}
	ctx := testContext()

	// /goal:next inserts at the FRONT (commit 7d476cb): queue is
	// [gamma, beta, alpha], so letter A=gamma, B=beta, C=alpha.
	// Note: "first"/"last"/"fresh"/"reuse" are reserved placement/context
	// tokens for /goal:next and cannot be used as objectives.
	if err := cmd.Run(ctx, []string{"next", "alpha"}); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Run(ctx, []string{"next", "beta"}); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Run(ctx, []string{"next", "gamma"}); err != nil {
		t.Fatal(err)
	}

	if err := cmd.Run(ctx, []string{"reorder", "1C,2A,3B"}); err != nil {
		t.Fatal(err)
	}
	goals, err := queue.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(goals) != 3 || goals[0].Objective != "alpha" {
		t.Errorf("reorder failed: %v", goals)
	}
}

// TestGoalCommand_ManageEmpty: with no active goal and an empty queue the
// manager still opens — framed by the add-at-start/add-at-end sentinels and
// the Done row — so goals can be created from it (bugs.md goal manager: the
// manager previously just printed "No queued goals.").
func TestGoalCommand_ManageEmpty(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "queue.json"))
	cmd := &GoalCommand{Mode: mode, Queue: queue}
	ctx := testContext()

	var items []tui.SelectorItem
	ctx.SelectOptionFunc = func(_ string, it []tui.SelectorItem, _ string, cb func(string, bool)) {
		items = it
		cb("", false)
	}

	if err := cmd.Run(ctx, []string{"manage"}); err != nil {
		t.Fatal(err)
	}
	assertManagerLayout(t, items, "__add_first__", "__add_last__", "__done__")
}

func TestGoalCommand_StartGoalPrompt(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore("")
	switcher := &testAutonomySwitcher{level: internal.AutonomyConfirm}
	cmd := &GoalCommand{Mode: mode, Queue: queue, AutonomySwitcher: switcher}
	ctx := testContext()

	called := false
	ctx.SelectOptionFunc = func(title string, items []tui.SelectorItem, initial string, cb func(string, bool)) {
		called = true
		cb("auto", true)
	}

	if err := cmd.Run(ctx, []string{"fix tests"}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("start permission dialog not shown")
	}
	if switcher.level != internal.AutonomyConfirm {
		t.Errorf("autonomy = %q", switcher.level)
	}
	if mode.GetGoal().Goal == nil {
		t.Error("goal should be created after approval")
	}
}

func TestGoalCommand_StartGoalYolo(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore("")
	switcher := &testAutonomySwitcher{level: internal.AutonomyYolo}
	cmd := &GoalCommand{Mode: mode, Queue: queue, AutonomySwitcher: switcher}
	ctx := testContext()

	called := false
	ctx.SelectOptionFunc = func(string, []tui.SelectorItem, string, func(string, bool)) {
		called = true
	}

	if err := cmd.Run(ctx, []string{"fix tests"}); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Error("dialog should not be shown in yolo")
	}
	if mode.GetGoal().Goal == nil {
		t.Error("goal should be created in yolo")
	}
}

func TestGoalCommand_StartGoalCancel(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore("")
	switcher := &testAutonomySwitcher{level: internal.AutonomyConfirm}
	cmd := &GoalCommand{Mode: mode, Queue: queue, AutonomySwitcher: switcher}
	ctx := testContext()

	flashed := make(chan string, 1)
	ctx.EventBus = event.MakeBus(1, 1, 1, 1)
	go func() {
		for ev := range ctx.EventBus.Chat {
			if ev.Flash != nil {
				flashed <- ev.Flash.Text
			}
		}
	}()
	ctx.SelectOptionFunc = func(_ string, _ []tui.SelectorItem, _ string, cb func(string, bool)) {
		cb("cancel", true)
	}

	if err := cmd.Run(ctx, []string{"fix tests"}); err != nil {
		t.Fatal(err)
	}
	if mode.GetGoal().Goal != nil {
		t.Error("goal should not be created after cancel")
	}
	select {
	case text := <-flashed:
		if text != "Goal start cancelled." {
			t.Errorf("flash = %q", text)
		}
	case <-time.After(time.Second):
		t.Error("expected flash")
	}
}

func TestGoalCommand_QueueManagerManage(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "queue.json"))
	cmd := &GoalCommand{Mode: mode, Queue: queue}
	ctx := testContext()

	cmd.Run(ctx, []string{"next", "alpha"})
	cmd.Run(ctx, []string{"next", "beta"})

	var capturedItems []tui.SelectorItem
	ctx.SelectOptionFunc = func(title string, items []tui.SelectorItem, initial string, cb func(string, bool)) {
		capturedItems = items
		cb("__done__", true)
	}

	if err := cmd.Run(ctx, []string{"manage"}); err != nil {
		t.Fatal(err)
	}
	// /goal:next prepends: queue is [beta, alpha]; the manager frames it with
	// the add-at-start/add-at-end sentinels and the Done row.
	queued, err := queue.Read()
	if err != nil {
		t.Fatal(err)
	}
	assertManagerLayout(t, capturedItems,
		"__add_first__", queued[0].ID, queued[1].ID, "__add_last__", "__done__")
}

// TestGoalCommand_QueueManagerMoveAndDelete drives the manager's hotkey
// emits end to end: "__moveup__"+id reorders the queue directly (no action
// submenu) and reopens with the cursor on the moved goal; "__delete__"+id
// asks for confirmation and only "yes" removes the goal.
func TestGoalCommand_QueueManagerMoveAndDelete(t *testing.T) {
	cmd, queue := newManagerCommand(t, "")
	ids := appendQueued(t, queue, "alpha", "beta")

	// Phase 1: move the second goal (beta) up via the '+' emit.
	ctx := testContext()
	opens := 0
	var cursor string
	ctx.SelectOptionFunc = func(_ string, _ []tui.SelectorItem, current string, cb func(string, bool)) {
		opens++
		cursor = current
		if opens == 1 {
			cb("__moveup__"+ids[1], true) // simulate '+' on beta
			return
		}
		cb("", false) // close the reopened manager
	}

	if err := cmd.Run(ctx, []string{"manage"}); err != nil {
		t.Fatal(err)
	}
	assertQueueObjectives(t, queue, []string{"beta", "alpha"})
	expectManagerCursor(t, opens, 2, cursor, ids[1])

	// Phase 2: delete the front goal via the Delete emit + confirmation.
	opens = 0
	var confirmTitle string
	ctx.SelectOptionFunc = func(title string, _ []tui.SelectorItem, _ string, cb func(string, bool)) {
		opens++
		switch opens {
		case 1:
			cb("__delete__"+ids[1], true) // simulate Delete on beta
		case 2:
			confirmTitle = title
			cb("yes", true)
		default:
			cb("", false) // close the reopened manager
		}
	}

	if err := cmd.Run(ctx, []string{"manage"}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(confirmTitle, "Delete goal ") {
		t.Errorf("expected a delete confirmation, got selector %q", confirmTitle)
	}
	assertQueueObjectives(t, queue, []string{"alpha"})
	if opens != 3 {
		t.Errorf("expected manager→confirm→manager (3 opens), got %d", opens)
	}
}

func testContext() core.Context {
	return core.Context{
		OutputBuffer:     &strings.Builder{},
		SelectOptionFunc: func(string, []tui.SelectorItem, string, func(string, bool)) {},
		ShowInputFunc:    func(string, string, func(string, bool)) {},
		RequestMainInput: func(string, func(string)) {},
	}
}

type testAutonomySwitcher struct {
	level internal.AutonomyLevel
}

func (s *testAutonomySwitcher) Current() internal.AutonomyLevel { return s.level }
func (s *testAutonomySwitcher) SetAutonomy(l internal.AutonomyLevel) error {
	s.level = l
	return nil
}

type fakeAgentThatCompletesGoal struct {
	mode *goal.GoalMode
	done chan struct{}
}

func (a *fakeAgentThatCompletesGoal) Run(ctx context.Context, prompt string) error {
	if a.done != nil {
		close(a.done)
	}
	_, _ = a.mode.MarkComplete(goal.GoalReasonInput{}, goal.GoalActorModel)
	return nil
}

func TestGoalCommand_CreateGoal_StartsDriver(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore("")
	done := make(chan struct{})
	driver := &core.GoalDriver{Mode: mode, Agent: &fakeAgentThatCompletesGoal{mode: mode, done: done}}
	switcher := &testAutonomySwitcher{level: internal.AutonomyYolo}
	cmd := &GoalCommand{Mode: mode, Queue: queue, Driver: driver, AutonomySwitcher: switcher}
	ctx := testContext()

	if err := cmd.Run(ctx, []string{"fix tests"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("driver did not start after goal creation")
	}
}

// fakeAgentSignalsRun records each Run invocation on a buffered channel so a
// test can tell a continuation turn happened without driving the goal to
// completion (which would clear it).
type fakeAgentSignalsRun struct {
	ran chan struct{}
}

func (a *fakeAgentSignalsRun) Run(ctx context.Context, prompt string) error {
	select {
	case a.ran <- struct{}{}:
	default:
	}
	return nil
}

// Regression for bugs.md: /goal:resume must re-activate a paused goal AND
// schedule a continuation turn with no user message. Previously resume only
// flipped state to active; the goal sat idle until the user typed something.
func TestGoalCommand_Resume_StartsDriver(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore("")
	ran := make(chan struct{}, 4)
	driver := &core.GoalDriver{Mode: mode, Agent: &fakeAgentSignalsRun{ran: ran}}
	switcher := &testAutonomySwitcher{level: internal.AutonomyYolo}
	cmd := &GoalCommand{Mode: mode, Queue: queue, Driver: driver, AutonomySwitcher: switcher}
	ctx := testContext()

	if err := cmd.Run(ctx, []string{"fix tests"}); err != nil {
		t.Fatal(err)
	}
	// Drain the continuation turn triggered by goal creation.
	waitForRun(t, ran, "creation continuation")

	if err := cmd.Run(ctx, []string{"pause"}); err != nil {
		t.Fatal(err)
	}
	if mode.GetGoal().Goal.Status != goal.GoalPaused {
		t.Fatalf("status after pause = %q", mode.GetGoal().Goal.Status)
	}
	// The pause may race a queued continuation; give any in-flight turn a
	// moment, then drain so the resume assertion is not polluted.
	drainRuns(ran)

	if err := cmd.Run(ctx, []string{"resume"}); err != nil {
		t.Fatal(err)
	}
	if mode.GetGoal().Goal.Status != goal.GoalActive {
		t.Fatalf("status after resume = %q", mode.GetGoal().Goal.Status)
	}
	// The whole point: resume alone must schedule a new continuation turn.
	waitForRun(t, ran, "resume continuation")
}

// fakeAgentBlocksUntilCancel simulates an in-flight continuation turn: Run
// blocks until its context is cancelled, then returns the ctx error — the
// same unwinding a real agent performs on interrupt.
type fakeAgentBlocksUntilCancel struct {
	started chan struct{}
	done    chan error
}

func (a *fakeAgentBlocksUntilCancel) Run(ctx context.Context, _ string) error {
	select {
	case a.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	err := ctx.Err()
	select {
	case a.done <- err:
	default:
	}
	return err
}

// Regression: /goal:cancel must stop the goal's drive loop — the in-flight
// continuation turn is interrupted and no further turns launch. Previously
// cancel only cleared the goal state: the current turn ran to completion,
// leaving the "Answering..." spinner alive with nothing left to execute.
func TestGoalCommand_Cancel_StopsDriveLoop(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore("")
	fake := &fakeAgentBlocksUntilCancel{started: make(chan struct{}, 1), done: make(chan error, 1)}
	driver := &core.GoalDriver{Mode: mode, Agent: fake}
	switcher := &testAutonomySwitcher{level: internal.AutonomyYolo}
	cmd := &GoalCommand{Mode: mode, Queue: queue, Driver: driver, AutonomySwitcher: switcher}
	ctx := testContext()

	if err := cmd.Run(ctx, []string{"fix tests"}); err != nil {
		t.Fatal(err)
	}
	// Wait until the drive loop is inside the (blocking) continuation turn.
	select {
	case <-fake.started:
	case <-time.After(time.Second):
		t.Fatal("drive loop did not start a continuation turn")
	}

	if err := cmd.Run(ctx, []string{"cancel"}); err != nil {
		t.Fatal(err)
	}

	// The cancel must interrupt the in-flight turn via the loop's context.
	select {
	case err := <-fake.done:
		if err != context.Canceled {
			t.Fatalf("turn ended with %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not interrupt the in-flight continuation turn")
	}
}

func waitForRun(t *testing.T, ran chan struct{}, what string) {
	t.Helper()
	select {
	case <-ran:
	case <-time.After(2 * time.Second):
		t.Fatalf("driver did not run %s without a user message", what)
	}
}

func drainRuns(ran chan struct{}) {
	for {
		select {
		case <-ran:
		case <-time.After(50 * time.Millisecond):
			return
		}
	}
}

func TestGoalCommand_Meta(t *testing.T) {
	cmd := &GoalCommand{}
	if cmd.Name() != "goal" {
		t.Errorf("name = %q", cmd.Name())
	}
	if cmd.Aliases() != nil {
		t.Error("aliases should be nil")
	}
	if cmd.ShortHelp() == "" {
		t.Error("short help empty")
	}
	if cmd.LongHelp() == "" {
		t.Error("long help empty")
	}
}

func TestPermissionOptions(t *testing.T) {
	cmd := &GoalCommand{}
	manual := cmd.permissionOptions(internal.AutonomyConfirm)
	if len(manual) != 4 {
		t.Errorf("manual options = %d", len(manual))
	}
	yolo := cmd.permissionOptions(internal.AutonomyYolo)
	if len(yolo) != 3 {
		t.Errorf("yolo options = %d", len(yolo))
	}
}

// --- Colon nomenclature + interactive flow regression tests ---

func TestGoalCommand_BareGoal_InteractiveCreate(t *testing.T) {
	// /goal (no args) → main input prompt; typing text creates the goal.
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	ctx := testContext()

	called := false
	ctx.RequestMainInput = func(prompt string, cb func(string)) {
		called = true
		if !strings.Contains(strings.ToLower(prompt), "goal") {
			t.Errorf("prompt should mention goal: %q", prompt)
		}
		cb("typed objective")
	}
	if err := cmd.Run(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("RequestMainInput not invoked for bare /goal")
	}
	if mode.GetGoal().Goal == nil || mode.GetGoal().Goal.Objective != "typed objective" {
		t.Errorf("goal not created from interactive input")
	}
}

func TestGoalCommand_BareGoal_InteractiveEmptyCancels(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	ctx := testContext()

	ctx.RequestMainInput = func(_ string, cb func(string)) {
		cb("   ") // whitespace-only aborts
	}
	if err := cmd.Run(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if mode.GetGoal().Goal != nil {
		t.Error("empty interactive input should not create a goal")
	}
}

func TestGoalCommand_FirstOrLast_FirstReplaces(t *testing.T) {
	// Create while a goal is active, choose "first" → replaces active goal.
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	ctx := testContext()

	cmd.Run(ctx, []string{"first goal"})

	ctx.SelectOptionFunc = func(_ string, _ []tui.SelectorItem, _ string, cb func(string, bool)) {
		cb("first", true)
	}
	if err := cmd.Run(ctx, []string{"new", "second goal"}); err != nil {
		t.Fatal(err)
	}
	if mode.GetGoal().Goal.Objective != "second goal" {
		t.Errorf("active goal = %q, want second goal", mode.GetGoal().Goal.Objective)
	}
}

func TestGoalCommand_FirstOrLast_ShowsActiveGoalDetails(t *testing.T) {
	// When a goal is already active, creating a new one must FIRST print the
	// active goal's details before asking where the new goal should go.
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	ctx := testContext()

	if err := cmd.Run(ctx, []string{"first goal"}); err != nil {
		t.Fatal(err)
	}
	active := mode.GetGoal().Goal

	ctx.OutputBuffer.Reset()
	var selectTitle string
	ctx.SelectOptionFunc = func(title string, _ []tui.SelectorItem, _ string, cb func(string, bool)) {
		selectTitle = title
		cb("cancel", true)
	}
	if err := cmd.Run(ctx, []string{"new", "second goal"}); err != nil {
		t.Fatal(err)
	}

	out := ctx.OutputBuffer.String()
	if !strings.Contains(out, active.Name) {
		t.Errorf("active goal details should include name %q: %s", active.Name, out)
	}
	if !strings.Contains(out, "first goal") {
		t.Errorf("active goal details should include objective: %s", out)
	}
	if !strings.Contains(out, "Active goal") {
		t.Errorf("details should be labelled 'Active goal': %s", out)
	}
	if !strings.Contains(selectTitle, "already active") {
		t.Errorf("picker title should mention active goal: %q", selectTitle)
	}
}

func TestGoalCommand_PromptsSayCtrlC(t *testing.T) {
	// The main-input prompt title must say 'ctrl-c', not 'empty to cancel'.
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))
	cmd := &GoalCommand{Mode: mode, Queue: queue}
	ctx := testContext()

	seen := map[string]string{}
	ctx.RequestMainInput = func(prompt string, cb func(string)) {
		seen[prompt] = prompt
		cb("") // cancel
	}

	_ = cmd.Run(ctx, nil)              // bare /goal → create prompt
	_ = cmd.Run(ctx, []string{"next"}) // queue prompt
	// Set up an active goal so replace-interactive is reachable.
	ctx.RequestMainInput = nil
	_ = cmd.Run(ctx, []string{"active"})
	ctx.RequestMainInput = func(prompt string, cb func(string)) {
		seen[prompt] = prompt
		cb("")
	}
	_ = cmd.Run(ctx, []string{"replace"}) // replace prompt

	for prompt := range seen {
		if strings.Contains(strings.ToLower(prompt), "empty to cancel") {
			t.Errorf("prompt should not say 'empty to cancel': %q", prompt)
		}
		if !strings.Contains(strings.ToLower(prompt), "ctrl-c") {
			t.Errorf("prompt should say 'ctrl-c': %q", prompt)
		}
	}
}

func TestGoalCommand_FirstOrLast_CancelDoesNothing(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	ctx := testContext()

	cmd.Run(ctx, []string{"first goal"})

	ctx.SelectOptionFunc = func(_ string, _ []tui.SelectorItem, _ string, cb func(string, bool)) {
		cb("cancel", true)
	}
	if err := cmd.Run(ctx, []string{"new", "second goal"}); err != nil {
		t.Fatal(err)
	}
	if mode.GetGoal().Goal.Objective != "first goal" {
		t.Errorf("active goal changed after cancel: %q", mode.GetGoal().Goal.Objective)
	}
}

func TestGoalCommand_NextInteractive_PrependsToQueue(t *testing.T) {
	// /goal:next (bare) → main input; the typed goal runs NEXT: inserted at
	// the FRONT of the queue, ahead of already-queued goals (default :first).
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))
	cmd := &GoalCommand{Mode: mode, Queue: queue}
	ctx := testContext()

	if _, err := queue.AppendGoal(goal.UpcomingGoalInput{Objective: "existing"}); err != nil {
		t.Fatal(err)
	}
	ctx.RequestMainInput = func(_ string, cb func(string)) {
		cb("queued objective")
	}
	if err := cmd.Run(ctx, []string{"next"}); err != nil {
		t.Fatal(err)
	}
	queued, _ := queue.Read()
	if len(queued) != 2 {
		t.Fatalf("queue = %v, want 2 goals", queued)
	}
	if queued[0].Objective != "queued objective" || queued[1].Objective != "existing" {
		t.Errorf("queue order = [%q %q], want [queued objective existing]",
			queued[0].Objective, queued[1].Objective)
	}
}

func TestGoalCommand_NextAdd_PrependsToQueue(t *testing.T) {
	// /goal:next:<text> queues the goal to run NEXT (the default :first):
	// FRONT of the queue, ahead of already-queued goals.
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))
	cmd := &GoalCommand{Mode: mode, Queue: queue}
	ctx := testContext()

	if _, err := queue.AppendGoal(goal.UpcomingGoalInput{Objective: "existing"}); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Run(ctx, []string{"next", "urgent fix"}); err != nil {
		t.Fatal(err)
	}
	queued, _ := queue.Read()
	if len(queued) != 2 || queued[0].Objective != "urgent fix" || queued[1].Objective != "existing" {
		t.Errorf("queue = %v, want [urgent fix existing]", queued)
	}
	if out := ctx.OutputBuffer.String(); !strings.Contains(out, "run next") {
		t.Errorf("output should say the goal runs next: %q", out)
	}
}

func TestGoalCommand_NextAdd_ExplicitFirstPrependsToQueue(t *testing.T) {
	// /goal:next:first:<text> is the explicit form of the default.
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))
	cmd := &GoalCommand{Mode: mode, Queue: queue}
	ctx := testContext()

	if _, err := queue.AppendGoal(goal.UpcomingGoalInput{Objective: "existing"}); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Run(ctx, []string{"next", "first", "urgent fix"}); err != nil {
		t.Fatal(err)
	}
	queued, _ := queue.Read()
	if len(queued) != 2 || queued[0].Objective != "urgent fix" || queued[1].Objective != "existing" {
		t.Errorf("queue = %v, want [urgent fix existing]", queued)
	}
}

func TestGoalCommand_NextAdd_LastAppendsToQueue(t *testing.T) {
	// /goal:next:last:<text> appends the goal at the END of the queue.
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))
	cmd := &GoalCommand{Mode: mode, Queue: queue}
	ctx := testContext()

	if _, err := queue.AppendGoal(goal.UpcomingGoalInput{Objective: "existing"}); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Run(ctx, []string{"next", "last", "tidy docs"}); err != nil {
		t.Fatal(err)
	}
	queued, _ := queue.Read()
	if len(queued) != 2 || queued[0].Objective != "existing" || queued[1].Objective != "tidy docs" {
		t.Errorf("queue = %v, want [existing tidy docs]", queued)
	}
}

func TestGoalCommand_FirstOrLast_AppendsToEnd(t *testing.T) {
	// The "Queue it for later" choice of the first-or-last prompt must keep
	// APPENDING to the end of the queue (it is not /goal:next).
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))
	cmd := &GoalCommand{Mode: mode, Queue: queue}
	ctx := testContext()

	if err := cmd.Run(ctx, []string{"active goal"}); err != nil {
		t.Fatal(err)
	}
	if _, err := queue.AppendGoal(goal.UpcomingGoalInput{Objective: "existing"}); err != nil {
		t.Fatal(err)
	}
	ctx.SelectOptionFunc = func(_ string, _ []tui.SelectorItem, _ string, cb func(string, bool)) {
		cb("last", true)
	}
	if err := cmd.Run(ctx, []string{"new", "second goal"}); err != nil {
		t.Fatal(err)
	}
	queued, _ := queue.Read()
	if len(queued) != 2 || queued[0].Objective != "existing" || queued[1].Objective != "second goal" {
		t.Errorf("queue = %v, want [existing second goal]", queued)
	}
}

func TestGoalCommand_GoalGetsFriendlyName(t *testing.T) {
	// Newly created goals get a non-empty friendly name in the snapshot.
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	ctx := testContext()

	if err := cmd.Run(ctx, []string{"fix tests"}); err != nil {
		t.Fatal(err)
	}
	g := mode.GetGoal().Goal
	if g == nil || !internal.SplitFriendlyName(g.Name) {
		t.Errorf("goal name not a friendly adjective.noun: %+v", g)
	}
	out := ctx.OutputBuffer.String()
	if !strings.Contains(out, g.Name) {
		t.Errorf("started output should include friendly name %q: %s", g.Name, out)
	}
}

func TestGoalCommand_QueueGoalsGetUniqueFriendlyNames(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))
	mode.SetNamePool(queue) // active-goal generator should avoid queue names
	cmd := &GoalCommand{Mode: mode, Queue: queue}
	ctx := testContext()

	for i := 0; i < 5; i++ {
		if err := cmd.Run(ctx, []string{"next", fmt.Sprintf("obj-%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	queued, _ := queue.Read()
	seen := make(map[string]bool, len(queued))
	for _, g := range queued {
		if !internal.SplitFriendlyName(g.Name) {
			t.Errorf("queued goal name not friendly: %q", g.Name)
		}
		if seen[g.Name] {
			t.Errorf("duplicate queued name: %q", g.Name)
		}
		seen[g.Name] = true
	}
}

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

// TestGoalCommand_Log exercises /goal:log: the durable event records render
// with time, type, actor, status, and reason — most recent last.
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

// TestGoalCommand_Verify exercises /goal:verify: the recorded verify command
// runs on demand and prints PASS/FAIL; without a verifier or command it
// errors cleanly.
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

func TestGoalCommand_List(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))
	cmd := &GoalCommand{Mode: mode, Queue: queue}
	ctx := testContext()

	// Active goal via create, then two queued goals in order.
	if err := cmd.Run(ctx, []string{"fix the newline loss in the converter with a complete multi-line objective that must not be truncated"}); err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Append("second goal: audit provider retry logic"); err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Append("third goal: add json.load to gpython"); err != nil {
		t.Fatal(err)
	}

	if err := cmd.Run(ctx, []string{"list"}); err != nil {
		t.Fatal(err)
	}
	out := ctx.OutputBuffer.String()

	if !strings.Contains(out, "## Goals") {
		t.Errorf("expected markdown header in output:\n%s", out)
	}
	// Order: active first, then queue order.
	i1 := strings.Index(out, "fix the newline loss")
	i2 := strings.Index(out, "second goal: audit provider retry logic")
	i3 := strings.Index(out, "third goal: add json.load to gpython")
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
	// Complete (untruncated) objectives: the long active objective must appear
	// in full — no truncation marker.
	if strings.Contains(out, "must not be tru...") || !strings.Contains(out, "must not be truncated") {
		t.Errorf("active objective was truncated:\n%s", out)
	}
}

// TestGoalCommand_ListShowsAllInfo verifies /goal:list renders ALL recorded
// goal information — context run type (fresh/reuse), completion criterion,
// verify command, handover, budget, terminal state and todos — for both the
// current and the queued goals (bugs.md: goal list shows all information).
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

// TestGoalCommand_ManageEdit covers the 'e' hotkey in /goal:manage: the
// selector emits "__edit__"+id; the manager must prompt with the current
// objective pre-filled, persist the edited description via Queue.Update, and
// reopen the manager. Cancel and blank submissions leave the objective
// untouched (and still return to the manager).
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

// runManageEditCase drives one /goal:manage edit scenario: simulate the 'e'
// hotkey on the single queued goal, answer the edit prompt with
// (submitValue, submitOK), then verify the stored objective, the pre-filled
// prompt, and the manager reopen.
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

// assertManagerEditableItems verifies goal rows opt into the 'e' edit hotkey
// (SelectorItem.Editable) and the sentinel action rows do not.
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

// TestGoalCommand_ManageDeleteHotkey covers the Delete hotkey in /goal:manage
// (bugs.md goal manager): the "__delete__"+id emit must open a confirmation
// selector and only "yes" removes the goal. Previously deletion fired
// immediately; and before that, the sentinel-prefixed string reached
// Queue.Remove and failed with "queued goal \"__delete__…\" not found"
// (bugs.md Issue 23).
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

// TestGoalCommand_ManageDeleteConfirmNo verifies the delete confirmation's
// negative paths: "no" (and Escape) leave the goal queued and return to the
// manager with the cursor back on it.
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

// runManageDeleteDeclineCase drives one declined-confirmation scenario: the
// Delete emit opens the confirmation, the (answer, answerOK) response
// declines, and the manager reopens with the cursor on the kept goal.
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

// collectFlashes routes ctx.Flash messages into a slice; the returned
// function must be called exactly once (it closes the bus) to collect them.
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

// newManagerCommand builds a GoalCommand backed by a temp-dir queue and,
// when activeObjective is non-empty, a running goal with that objective.
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

// appendQueued appends the objectives to the queue and returns their IDs in
// queue order.
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

// queueObjectives returns the queued objectives in queue order.
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

// assertQueueObjectives checks the queue holds exactly want, in order.
func assertQueueObjectives(t *testing.T, queue *core.GoalQueueStore, want []string) {
	t.Helper()
	if got := queueObjectives(t, queue); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("queue = %v, want %v", got, want)
	}
}

// assertQueueIDs checks the queue holds exactly wantOrder (indices into ids)
// in order.
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

// expectManagerCursor asserts the selector was invoked wantOpens times and
// the last invocation carried the cursor on want.
func expectManagerCursor(t *testing.T, opens, wantOpens int, cursor, want string) {
	t.Helper()
	if opens != wantOpens || cursor != want {
		t.Errorf("selector opens=%d cursor=%q, want %d opens with the cursor on %q",
			opens, cursor, wantOpens, want)
	}
}

// assertManagerLayout checks the manager rows' values, in order.
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

// assertAllPreserveOrder checks every manager row opts out of the
// alphabetical sort (the manager's execution order is caller order).
func assertAllPreserveOrder(t *testing.T, items []tui.SelectorItem) {
	t.Helper()
	for i, it := range items {
		if !it.PreserveOrder {
			t.Errorf("items[%d] (%q) must set PreserveOrder to keep execution order", i, it.Value)
		}
	}
}

// TestGoalCommand_ManageListExecutionOrder pins the manager's list layout
// (bugs.md goal manager): the add-at-start sentinel, the ACTIVE goal
// (marked, not movable), the queued goals in run order, the add-at-end
// sentinel and Done — every row PreserveOrder — and the manager requests
// the reorder keymap ('+'/'-' = move up/down).
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

// TestGoalCommand_ManageMoveHotkeys covers the '+/-' reorder emits
// (table-driven): each move calls Queue.Move and reopens the manager with
// the cursor on the moved goal; boundary moves are no-ops that keep the
// cursor on the goal.
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

// TestGoalCommand_ManageActiveRowRejected: move/delete emits and plain Enter
// on the ACTIVE goal row are rejected with a flash — the queue and the
// running goal are untouched and the manager reopens on the active row.
func TestGoalCommand_ManageActiveRowRejected(t *testing.T) {
	cases := []struct {
		name string
		emit string
	}{
		{"move up on active", "__moveup____active__"},
		{"move down on active", "__movedown____active__"},
		{"delete on active", "__delete____active__"},
		{"enter on active", "__active__"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runManageActiveRowCase(t, tc.emit)
		})
	}
}

// runManageActiveRowCase drives one active-row rejection: the emit must not
// touch the queue or the running goal, must flash a rejection, and must
// reopen the manager on the active row.
func runManageActiveRowCase(t *testing.T, emit string) {
	t.Helper()
	cmd, queue := newManagerCommand(t, "running")
	appendQueued(t, queue, "queued one")

	ctx := testContext()
	getFlashes := collectFlashes(&ctx)
	opens := 0
	var cursor string
	ctx.SelectOptionFunc = func(_ string, _ []tui.SelectorItem, current string, cb func(string, bool)) {
		opens++
		cursor = current
		if opens == 1 {
			cb(emit, true)
			return
		}
		cb("", false)
	}

	if err := cmd.showQueueManager(ctx); err != nil {
		t.Fatal(err)
	}
	assertQueueObjectives(t, queue, []string{"queued one"})
	if g := cmd.Mode.GetGoal().Goal; g == nil || g.Objective != "running" {
		t.Errorf("active goal must still be running, got %+v", g)
	}
	expectManagerCursor(t, opens, 2, cursor, "__active__")
	flashes := getFlashes()
	if len(flashes) == 0 || !strings.Contains(flashes[0], "active goal") {
		t.Errorf("expected a rejection flash mentioning the active goal, got %v", flashes)
	}
}

// TestGoalCommand_ManageAddRows drives the add sentinels (table-driven):
// with an active goal, "-- add at start --" prepends to the queue (the goal
// runs next) and "-- add at end --" appends — neither touches the running
// goal; with no active goal the new goal starts immediately.
func TestGoalCommand_ManageAddRows(t *testing.T) {
	cases := []manageAddRowCase{
		{"add at start with active goal", "__add_first__", true,
			[]string{"new goal", "queued one"}, "running", "run next"},
		{"add at end with active goal", "__add_last__", true,
			[]string{"queued one", "new goal"}, "running", "end of the queue"},
		{"add at start with no active goal", "__add_first__", false,
			[]string{"queued one"}, "new goal", "Set new goal objective"},
		{"add at end with no active goal", "__add_last__", false,
			[]string{"queued one"}, "new goal", "Set new goal objective"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runManageAddRowCase(t, tc)
		})
	}
}

// manageAddRowCase describes one add-row scenario: Enter on the sentinel row
// must open the create-goal flow (prompt contains wantPromptSub), and after
// submitting "new goal" the queue holds wantQueue and the active goal's
// objective is wantActive.
type manageAddRowCase struct {
	name          string
	sentinel      string
	active        bool
	wantQueue     []string
	wantActive    string
	wantPromptSub string
}

func runManageAddRowCase(t *testing.T, tc manageAddRowCase) {
	t.Helper()
	activeObjective := ""
	if tc.active {
		activeObjective = "running"
	}
	cmd, queue := newManagerCommand(t, activeObjective)
	appendQueued(t, queue, "queued one")

	ctx := testContext()
	var prompt string
	ctx.RequestMainInput = func(p string, onSubmit func(string)) {
		prompt = p
		onSubmit("new goal")
	}
	ctx.SelectOptionFunc = func(_ string, _ []tui.SelectorItem, _ string, cb func(string, bool)) {
		cb(tc.sentinel, true) // Enter on the add row
	}

	if err := cmd.showQueueManager(ctx); err != nil {
		t.Fatal(err)
	}
	if prompt == "" {
		t.Fatal("the add row did not open the create-goal flow")
	}
	if !strings.Contains(prompt, tc.wantPromptSub) {
		t.Errorf("create prompt = %q, want substring %q", prompt, tc.wantPromptSub)
	}
	assertQueueObjectives(t, queue, tc.wantQueue)
	g := cmd.Mode.GetGoal().Goal
	if g == nil || g.Objective != tc.wantActive {
		t.Errorf("active goal = %+v, want objective %q", g, tc.wantActive)
	}
}

// TestGoalCommand_ManageGenericAddEmit is the regression for the generic
// '__add__' emit reaching the queue-action menu and failing with
// "queued goal … not found" (bugs.md goal manager item 4): it must open the
// create-goal flow instead. Reachable via a host without the reorder keymap
// (SelectOptionKeyed fallback).
func TestGoalCommand_ManageGenericAddEmit(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	if _, err := mode.CreateGoal(goal.CreateGoalInput{Objective: "running"}, goal.GoalActorUser); err != nil {
		t.Fatal(err)
	}
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))
	cmd := &GoalCommand{Mode: mode, Queue: queue}

	ctx := testContext()
	getFlashes := collectFlashes(&ctx)
	ctx.RequestMainInput = func(_ string, onSubmit func(string)) {
		onSubmit("added goal")
	}
	ctx.SelectOptionFunc = func(_ string, _ []tui.SelectorItem, _ string, cb func(string, bool)) {
		cb("__add__", true)
	}

	if err := cmd.showQueueManager(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := queue.Read()
	if len(got) != 1 || got[0].Objective != "added goal" {
		t.Errorf("queue = %+v, want the added goal queued behind the active one", got)
	}
	for _, f := range getFlashes() {
		if strings.Contains(f, "not found") {
			t.Errorf("regression: __add__ leaked into a goal lookup: %q", f)
		}
	}
}

// TestGoalCommand_ManageEnterOnGoalRow: plain Enter on a queued goal row no
// longer opens the two-step action menu — reorder and delete are
// hotkey-driven; the manager flashes the cheat sheet and reopens with the
// cursor on the row.
func TestGoalCommand_ManageEnterOnGoalRow(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))
	cmd := &GoalCommand{Mode: mode, Queue: queue}
	goals, err := queue.Append("some goal")
	if err != nil {
		t.Fatal(err)
	}
	id := goals[0].ID

	ctx := testContext()
	getFlashes := collectFlashes(&ctx)
	opens := 0
	var cursor string
	ctx.SelectOptionFunc = func(_ string, _ []tui.SelectorItem, current string, cb func(string, bool)) {
		opens++
		cursor = current
		if opens == 1 {
			cb(id, true) // Enter on the goal row
			return
		}
		cb("", false)
	}

	if err := cmd.showQueueManager(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := queue.Read()
	if len(got) != 1 {
		t.Errorf("queue must be unchanged, got %+v", got)
	}
	if opens != 2 || cursor != id {
		t.Errorf("manager must reopen on the same row (opens=%d, cursor=%q)", opens, cursor)
	}
	flashes := getFlashes()
	if len(flashes) == 0 || !strings.Contains(flashes[0], "move up") {
		t.Errorf("expected a hotkey cheat-sheet flash, got %v", flashes)
	}
}

// TestGoalCommand_ParseContextToken covers /goal:new:fresh|reuse parsing
// (bugs.md Issue 24): the leading token selects the context mode, and /goal:new
// without a token defers to the configured default.
func TestGoalCommand_ParseContextToken(t *testing.T) {
	cmd := &GoalCommand{}
	cases := []struct {
		args        []string
		kind        string
		objective   string
		contextMode string
	}{
		{[]string{"new", "fresh fix the bug"}, "create", "fix the bug", "fresh"},
		{[]string{"new", "fresh"}, "create-interactive", "", "fresh"},
		{[]string{"new", "reuse investigate"}, "create", "investigate", "reuse"},
		{[]string{"new", "fix the bug"}, "create", "fix the bug", ""},
		{[]string{"next", "fresh audit"}, "next-add", "audit", "fresh"},
		{[]string{"next", "reuse audit"}, "next-add", "audit", "reuse"},
		{[]string{"next", "audit"}, "next-add", "audit", ""},
	}
	for _, tc := range cases {
		got := cmd.parseArgs(tc.args)
		if got.kind != tc.kind || got.objective != tc.objective || got.contextMode != tc.contextMode {
			t.Errorf("parseArgs(%v) = {kind:%q objective:%q contextMode:%q}, want {%q %q %q}",
				tc.args, got.kind, got.objective, got.contextMode, tc.kind, tc.objective, tc.contextMode)
		}
	}
}

// TestGoalCommand_Create_DefaultFreshOn verifies /goal:new defaults to a clean
// context when no resolver is set (nil = true).
func TestGoalCommand_Create_DefaultFreshOn(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	if err := cmd.Run(testContext(), []string{"new", "fix tests"}); err != nil {
		t.Fatal(err)
	}
	if g := mode.GetGoal().Goal; g == nil || !g.FreshContext {
		t.Errorf("expected FreshContext=true by default, got %+v", g)
	}
}

// TestGoalCommand_Create_ReuseToken verifies /goal:new:reuse keeps the
// conversation context even with the fresh default on.
func TestGoalCommand_Create_ReuseToken(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	if err := cmd.Run(testContext(), []string{"new", "reuse", "fix tests"}); err != nil {
		t.Fatal(err)
	}
	if g := mode.GetGoal().Goal; g == nil || g.FreshContext {
		t.Errorf("expected FreshContext=false for /goal:new:reuse, got %+v", g)
	}
}

// TestGoalCommand_Create_ConfigDefault verifies the configured default is
// honored and the fresh token overrides it.
func TestGoalCommand_Create_ConfigDefault(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	cmd.FreshContextDefault = func() bool { return false } // config: reuse

	if err := cmd.Run(testContext(), []string{"new", "first objective"}); err != nil {
		t.Fatal(err)
	}
	if g := mode.GetGoal().Goal; g == nil || g.FreshContext {
		t.Errorf("expected FreshContext=false from config default, got %+v", g)
	}

	// The fresh token overrides the config-off default. A goal is already
	// active, so the first-or-last prompt fires; choose "first" (replace).
	ctx := testContext()
	ctx.SelectOptionFunc = func(_ string, _ []tui.SelectorItem, _ string, cb func(string, bool)) {
		cb("first", true)
	}
	if err := cmd.Run(ctx, []string{"new", "fresh", "second objective"}); err != nil {
		t.Fatal(err)
	}
	if g := mode.GetGoal().Goal; g == nil || !g.FreshContext {
		t.Errorf("expected FreshContext=true from the fresh token over config default, got %+v", g)
	}
}

// TestGoalCommand_QueueNext_FreshContextStored verifies /goal:next stores the
// resolved context mode in the queue so it survives promotion.
func TestGoalCommand_QueueNext_FreshContextStored(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))
	cmd := &GoalCommand{Mode: mode, Queue: queue}
	ctx := testContext()

	if err := cmd.Run(ctx, []string{"next", "fresh", "audit retry logic"}); err != nil {
		t.Fatal(err)
	}
	queued, err := queue.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 1 || !queued[0].FreshContext {
		t.Errorf("expected queued goal FreshContext=true, got %+v", queued)
	}
}

// TestGoalCommand_Current reproduces the /goal:current request: it must print
// the currently executed goal with its full objective, criterion, verify
// command and todos with statuses (richer than /goal:status).
func TestGoalCommand_Current(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &GoalCommand{Mode: mode, Queue: core.NewGoalQueueStore("")}
	ctx := testContext()

	// No goal → clear message.
	if err := cmd.Run(ctx, []string{"current"}); err != nil {
		t.Fatal(err)
	}
	if got := ctx.OutputBuffer.String(); !strings.Contains(got, "No current goal") {
		t.Fatalf("/goal:current with no goal = %q", got)
	}
	ctx.OutputBuffer.Reset()

	// Active goal with todos, criterion and verify command.
	criterion := "tests pass"
	verify := "go test ./..."
	if _, err := mode.CreateGoal(goal.CreateGoalInput{
		Objective: "fix the parser", Name: "calm.fox",
		CompletionCriterion: &criterion, VerifyCommand: &verify,
	}, goal.GoalActorUser); err != nil {
		t.Fatal(err)
	}
	if _, err := mode.AddGoalTodo("write failing test", goal.GoalActorUser); err != nil {
		t.Fatal(err)
	}
	if _, err := mode.AddGoalTodo("make it pass", goal.GoalActorUser); err != nil {
		t.Fatal(err)
	}

	if err := cmd.Run(ctx, []string{"current"}); err != nil {
		t.Fatal(err)
	}
	out := ctx.OutputBuffer.String()
	for _, want := range []string{
		"calm.fox", "fix the parser", "active",
		"tests pass", "go test ./...",
		"write failing test", "make it pass", "[ ]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("/goal:current output missing %q:\n%s", want, out)
		}
	}
}
