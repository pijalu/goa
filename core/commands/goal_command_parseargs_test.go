// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"path/filepath"
	"strings"
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

func TestGoalCommand_parseArgs_SpaceGluedSubcommand(t *testing.T) {
	// The router splits on ':' only, so "/goal:next fix tests" reaches Run
	// as ONE arg "next fix tests". Text-consuming subcommands must be
	// recognized even when glued to their text by a space — else the line
	// falls into the objective-create flow (replace-or-queue prompt).
	cmd := &GoalCommand{}
	cases := []struct {
		args      []string
		kind      string
		objective string
	}{
		{args: []string{"next fix tests"}, kind: "next-add", objective: "fix tests"},
		{args: []string{"NEXT audit queue"}, kind: "next-add", objective: "audit queue"},
		{args: []string{"next last audit"}, kind: "next-add", objective: "audit"},
		{args: []string{"next"}, kind: "next-interactive"},
		{args: []string{"new build the parser"}, kind: "create", objective: "build the parser"},
		{args: []string{"replace flaky suite"}, kind: "replace", objective: "flaky suite"},
		{args: []string{"reorder 2B,1A"}, kind: "reorder", objective: "2B,1A"},
		{args: []string{"cancel all"}, kind: "cancel-all"},
		{args: []string{"cancel bogus"}, kind: "error"},
		{args: []string{"pause next"}, kind: "pause-next"},
		{args: []string{"pause next off"}, kind: "pause-next-off"},
	}
	for _, tc := range cases {
		got := cmd.parseArgs(tc.args)
		if got.kind != tc.kind || got.objective != tc.objective {
			t.Errorf("parseArgs(%q) = {kind %q, objective %q}, want {kind %q, objective %q}",
				tc.args, got.kind, got.objective, tc.kind, tc.objective)
		}
	}
}

func TestGoalCommand_parseArgs_NonTextKeywordsStayObjectives(t *testing.T) {
	// Escape hatch: only text-consuming keywords are space-split. An
	// objective starting with a no-argument keyword ("list everything …")
	// still parses as a free-form objective.
	cmd := &GoalCommand{}
	for _, args := range [][]string{
		{"list everything twice"},
		{"status now please"},
		{"resume tomorrow cleanup"},
	} {
		wantText := strings.Join(args, " ")
		got := cmd.parseArgs(args)
		if got.kind != "create" || got.objective != wantText {
			t.Errorf("parseArgs(%q) = {kind %q, objective %q}, want {create, %q}",
				args, got.kind, got.objective, wantText)
		}
	}
}
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
		// rfirst/rlast shorthand: reuse + placement in one token
		{name: "rfirst", args: []string{"next", "rfirst", "audit"},
			kind: "next-add", placement: placementNext, mode: "reuse", objective: "audit"},
		{name: "rlast", args: []string{"next", "rlast", "audit"},
			kind: "next-add", placement: placementLast, mode: "reuse", objective: "audit"},
		{name: "rlast bare is interactive with reuse", args: []string{"next", "rlast"},
			kind: "next-interactive", placement: placementLast, mode: "reuse"},
		{name: "rlast then fresh overrides context", args: []string{"next", "rlast", "fresh", "audit"},
			kind: "next-add", placement: placementLast, mode: "fresh", objective: "audit"},
		{name: "fresh then rfirst", args: []string{"next", "fresh", "rfirst", "audit"},
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

func TestGoalCommand_CancelAll_NothingToCancel(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	queue := core.NewGoalQueueStore(filepath.Join(t.TempDir(), "queue.json"))
	cmd := &GoalCommand{Mode: mode, Queue: queue}
	ctx := testContext()

	if err := cmd.Run(ctx, []string{"cancel", "all"}); err == nil {
		t.Fatal("expected error when there is nothing to cancel")
	}
}

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

// assertCompletionValues asserts the completion Values match want exactly,
// in order.
func assertCompletionValues(t *testing.T, vals []core.ArgCompletion, want []string) {
	t.Helper()
	if len(vals) != len(want) {
		t.Fatalf("completions = %+v, want %d entries %v", vals, len(want), want)
	}
	for i, w := range want {
		if vals[i].Value != w {
			t.Errorf("completions[%d] = %q, want %q (all: %+v)", i, vals[i].Value, w, vals)
		}
	}
}

func TestGoalCommand_CompleteArgs_NextOptions(t *testing.T) {
	cmd := &GoalCommand{}
	ctx := testContext()

	// Level-2: /goal:next:<tab> lists the placement and context options,
	// fully spelled out (mirrors the /goal:cancel: scopes).
	assertCompletionValues(t, cmd.CompleteArgs(ctx, "next:"),
		[]string{"next:first", "next:last", "next:fresh", "next:reuse", "next:rfirst", "next:rlast"})

	// Prefix filtering at the nested level.
	assertCompletionValues(t, cmd.CompleteArgs(ctx, "next:l"), []string{"next:last"})
	assertCompletionValues(t, cmd.CompleteArgs(ctx, "next:f"), []string{"next:first", "next:fresh"})
	assertCompletionValues(t, cmd.CompleteArgs(ctx, "next:r"), []string{"next:reuse", "next:rfirst", "next:rlast"})

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
