// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"errors"
	"strings"
	"testing"

	"github.com/pijalu/goa/core/goal"
)

// newEnforcedTool builds a tool with a criterion-bearing active goal and the
// default (verify) done-gate.
func newEnforcedTool(t *testing.T) (*GoalTool, *goal.GoalMode) {
	t.Helper()
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	mode.SetDoneGate(goal.DefaultDoneGate)
	criterion := "go test ./... passes"
	if _, err := mode.CreateGoal(goal.CreateGoalInput{
		Objective:           "make the build green",
		CompletionCriterion: &criterion,
	}, goal.GoalActorUser); err != nil {
		t.Fatal(err)
	}
	return &GoalTool{Mode: mode, CreateAllowed: func() bool { return true }}, mode
}

func TestUpdate_PausedRequiresReason(t *testing.T) {
	tool, mode := newEnforcedTool(t)
	_, err := tool.Execute(`{"action":"update","status":"paused"}`)
	if err == nil {
		t.Fatal("paused without reason must fail")
	}
	if !strings.Contains(err.Error(), "reason") {
		t.Errorf("error must name the missing reason: %v", err)
	}
	if g := mode.GetActiveGoal(); g == nil || g.Status != goal.GoalActive {
		t.Fatalf("rejected pause must leave the goal active, got %+v", mode.GetGoal().Goal)
	}

	out, err := tool.Execute(`{"action":"update","status":"paused","reason":"rate limited by upstream, backing off"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "rate limited") {
		t.Errorf("pause output must echo the reason: %q", out)
	}
	g := mode.GetGoal().Goal
	if g == nil || g.Status != goal.GoalPaused {
		t.Fatalf("status = %+v", g)
	}
	if g.TerminalReason == nil || *g.TerminalReason != "rate limited by upstream, backing off" {
		t.Errorf("terminal reason = %v", g.TerminalReason)
	}
}

func TestUpdate_BlockedRequiresReasonAndExpectation(t *testing.T) {
	tool, mode := newEnforcedTool(t)

	cases := []struct {
		name string
		in   string
	}{
		{"neither", `{"action":"update","status":"blocked"}`},
		{"reason only", `{"action":"update","status":"blocked","reason":"need API key"}`},
		{"expectation only", `{"action":"update","status":"blocked","expectation":"provide key"}`},
		{"blank strings", `{"action":"update","status":"blocked","reason":"  ","expectation":" "}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tool.Execute(tc.in); err == nil {
				t.Fatal("blocked without reason+expectation must fail")
			}
			if g := mode.GetActiveGoal(); g == nil {
				t.Fatal("rejected blocked must leave the goal active")
			}
		})
	}

	res, err := tool.ExecuteWithResult(`{"action":"update","status":"blocked","reason":"missing OPENAI_API_KEY","expectation":"user provides a valid OPENAI_API_KEY"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !res.StopTurn {
		t.Error("blocked must stop the turn")
	}
	if !strings.Contains(res.Output, "missing OPENAI_API_KEY") || !strings.Contains(res.Output, "user provides a valid OPENAI_API_KEY") {
		t.Errorf("output must surface reason and expectation: %q", res.Output)
	}
	g := mode.GetGoal().Goal
	if g.Status != goal.GoalBlocked {
		t.Fatalf("status = %q", g.Status)
	}
	if g.TerminalExpectation == nil || *g.TerminalExpectation != "user provides a valid OPENAI_API_KEY" {
		t.Errorf("expectation = %v", g.TerminalExpectation)
	}
}

func TestUpdate_CompleteVerifyGateFlow(t *testing.T) {
	tool, mode := newEnforcedTool(t)

	// First complete: challenged, turn continues, goal stays active.
	res, err := tool.ExecuteWithResult(`{"action":"update","status":"complete"}`)
	if err != nil {
		t.Fatal(err)
	}
	if res.StopTurn {
		t.Error("verification challenge must NOT stop the turn — the model must respond in-turn")
	}
	if !strings.Contains(res.Output, "go test ./... passes") {
		t.Errorf("challenge must restate the criterion: %q", res.Output)
	}
	if mode.GetActiveGoal() == nil {
		t.Fatal("goal must stay active after challenge")
	}

	// Re-complete without evidence: hard error, still active.
	if _, err = tool.Execute(`{"action":"update","status":"complete"}`); err == nil {
		t.Fatal("confirmed complete without reason must fail")
	}
	if mode.GetActiveGoal() == nil {
		t.Fatal("rejected complete must leave the goal active")
	}

	// Confirmed complete with evidence: closes, stops turn.
	res, err = tool.ExecuteWithResult(`{"action":"update","status":"complete","reason":"ran go test ./...: ok 12 packages"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !res.StopTurn {
		t.Error("actual completion must stop the turn")
	}
	if mode.GetGoal().Goal != nil {
		t.Error("goal must be cleared after completion")
	}
}

func TestUpdate_CompleteWithoutCriterionBypassesGate(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	mode.SetDoneGate(goal.DefaultDoneGate)
	if _, err := mode.CreateGoal(goal.CreateGoalInput{Objective: "no criterion"}, goal.GoalActorUser); err != nil {
		t.Fatal(err)
	}
	tool := &GoalTool{Mode: mode, CreateAllowed: func() bool { return true }}
	res, err := tool.ExecuteWithResult(`{"action":"update","status":"complete"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !res.StopTurn {
		t.Error("immediate completion must stop the turn")
	}
	if mode.GetGoal().Goal != nil {
		t.Error("criterion-less goal must close without a challenge")
	}
}

func TestCreate_BatchForwardsCriterionToQueue(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	tool := &GoalTool{Mode: mode, CreateAllowed: func() bool { return true }, Queue: &fakeQueue{}}
	out, err := tool.Execute(`{"action":"create","objectives":["one","two","three"],"completionCriterion":"all three shipped"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"queued":2`) {
		t.Errorf("expected 2 queued, got %q", out)
	}
	q := tool.Queue.(*fakeQueue)
	if len(q.goals) != 2 {
		t.Fatalf("queue = %d goals", len(q.goals))
	}
	for i, g := range q.goals {
		if g.CompletionCriterion == nil || *g.CompletionCriterion != "all three shipped" {
			t.Errorf("queued goal %d lost its criterion: %+v", i, g)
		}
	}
}

// TestBlocked_AutoEnqueuesUnblockingGoal is the F5 regression: a model block
// WITH justification (queue wired) must NOT simply park the goal — it must
// demote the blocked goal to the front of the queue and activate an
// "unblocking" investigation goal in front of it. The blocked goal's
// criterion and verify command must ride along so it resumes intact.
func TestBlocked_AutoEnqueuesUnblockingGoal(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	criterion := "go test ./... passes"
	verify := "go test ./..."
	if _, err := mode.CreateGoal(goal.CreateGoalInput{
		Objective:           "implement ALTER TABLE",
		CompletionCriterion: &criterion,
		VerifyCommand:       &verify,
	}, goal.GoalActorUser); err != nil {
		t.Fatal(err)
	}
	q := &fakeQueue{}
	tool := &GoalTool{Mode: mode, CreateAllowed: func() bool { return true }, Queue: q}

	res, err := tool.ExecuteWithResult(`{"action":"update","status":"blocked","reason":"70 failures need ALTER TABLE constraint preservation","expectation":"user decision on scope"}`)
	if err != nil {
		t.Fatal(err)
	}

	// The blocked goal must be at the FRONT of the queue, criterion intact.
	if len(q.goals) != 1 {
		t.Fatalf("expected blocked goal re-queued at front, queue = %d: %+v", len(q.goals), q.goals)
	}
	assertRequeuedGoal(t, q.goals[0], "implement ALTER TABLE", criterion, verify)
	// The NEW active goal must be the unblocking investigation goal, carrying
	// the blocker context and the investigate→execute-or-block contract.
	assertUnblockGoalActive(t, mode)
	// The tool must report the auto-unblock, not the plain "waiting for user".
	if !strings.Contains(res.Output, "unblocking goal") {
		t.Errorf("output must report the auto-unblock flow: %q", res.Output)
	}
}

// TestBlocked_InvestigationGoal_DoesNotSpawnSuccessor is the ping-pong F1
// regression (goa-export-20260903-183525): a blocked UNBLOCKING INVESTIGATION
// goal must terminate the auto-unblock flow — no successor investigation,
// no re-queue of the failed investigation in front of the real goal. The old
// behavior prepended the investigation itself (queue [U, A]) and re-spawned,
// so the real goal never resumed and the same root-cause conclusion repeated
// forever.
func TestBlocked_InvestigationGoal_DoesNotSpawnSuccessor(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	q := &fakeQueue{}
	tool := &GoalTool{Mode: mode, CreateAllowed: func() bool { return true }, Queue: q}

	// Step 1: a standard goal blocks with justification → investigation U1
	// spawns and A is re-queued in front-parked position.
	if _, err := mode.CreateGoal(goal.CreateGoalInput{Objective: "implement ALTER TABLE"}, goal.GoalActorUser); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.ExecuteWithResult(`{"action":"update","status":"blocked","reason":"70 failures","expectation":"user decision"}`); err != nil {
		t.Fatal(err)
	}
	u1 := mode.GetActiveGoal()
	if u1 == nil || u1.Kind != goal.GoalKindUnblock {
		t.Fatalf("expected active unblocking investigation, got %+v", u1)
	}
	if len(q.goals) != 1 {
		t.Fatalf("step 1: queue = %d goals, want 1 (the re-queued standard goal): %+v", len(q.goals), q.goals)
	}

	// Step 2: the INVESTIGATION blocks (it found no autonomous solution).
	res, err := tool.ExecuteWithResult(`{"action":"update","status":"blocked","reason":"fix is a multi-session port","expectation":"user approves scope"}`)
	if err != nil {
		t.Fatal(err)
	}

	// The investigation must NOT be re-queued and NO successor investigation
	// may spawn: the queue still holds exactly the original re-queued goal.
	if len(q.goals) != 1 {
		t.Errorf("investigation block changed the queue to %d goals (zombie re-queue + successor spawn): %+v", len(q.goals), q.goals)
	}
	if q.goals[0].Objective != "implement ALTER TABLE" {
		t.Errorf("queue head = %q, want the original standard goal", q.goals[0].Objective)
	}
	if !strings.Contains(res.Output, "No successor investigation") {
		t.Errorf("output must say no successor investigation is spawned: %q", res.Output)
	}
	if !res.StopTurn {
		t.Error("investigation block must stop the turn (ask the user)")
	}
}

// TestBlocked_InvestigationSpawn_CarriesCriterionAndKind pins F2: the
// auto-spawned investigation must be created WITH its completion criterion
// (arming the done-gate against prose-only completions) and the unblock kind.
func TestBlocked_InvestigationSpawn_CarriesCriterionAndKind(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	tool := &GoalTool{Mode: mode, CreateAllowed: func() bool { return true }, Queue: &fakeQueue{}}
	if _, err := mode.CreateGoal(goal.CreateGoalInput{Objective: "implement ALTER TABLE"}, goal.GoalActorUser); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.ExecuteWithResult(`{"action":"update","status":"blocked","reason":"70 failures","expectation":"user decision"}`); err != nil {
		t.Fatal(err)
	}
	u := mode.GetActiveGoal()
	if u == nil {
		t.Fatal("no active goal after block")
	}
	if u.Kind != goal.GoalKindUnblock {
		t.Errorf("investigation kind = %q, want %q", u.Kind, goal.GoalKindUnblock)
	}
	if u.CompletionCriterion == nil || !strings.Contains(*u.CompletionCriterion, "execution goal") {
		t.Errorf("investigation criterion must demand a created follow-up goal, got %v", u.CompletionCriterion)
	}
}

// TestBlocked_RequeuedGoalCarriesBlockHandoff pins F3: the re-queued blocked
// goal must carry the block context (reason + expectation) as its handover,
// so the promotion reminder tells the resumed turn why it stopped. The old
// code dropped it despite a comment claiming the opposite.
func TestBlocked_RequeuedGoalCarriesBlockHandoff(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	q := &fakeQueue{}
	tool := &GoalTool{Mode: mode, CreateAllowed: func() bool { return true }, Queue: q}
	if _, err := mode.CreateGoal(goal.CreateGoalInput{Objective: "implement ALTER TABLE"}, goal.GoalActorUser); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.ExecuteWithResult(`{"action":"update","status":"blocked","reason":"70 failures need ALTER TABLE constraint preservation","expectation":"user decision on scope"}`); err != nil {
		t.Fatal(err)
	}
	if len(q.goals) != 1 {
		t.Fatalf("queue = %d goals", len(q.goals))
	}
	h := q.goals[0].Handoff
	if h == nil {
		t.Fatal("re-queued goal has no handover — the resumed turn will not know why the goal blocked")
	}
	for _, want := range []string{"70 failures", "user decision on scope", "auto-blocked"} {
		if !strings.Contains(*h, want) {
			t.Errorf("handover missing %q: %s", want, *h)
		}
	}
}

// TestBlocked_SpawnFailureSurfaced pins the silent-failure half of F4/F5:
// when the queue rejects the re-queue insert (e.g. oversized objective), the
// block output must SAY the auto-unblock failed instead of implying an
// investigation is running. The referenced export dead-ended on exactly
// this silent fallback.
func TestBlocked_SpawnFailureSurfaced(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	q := &fakeQueue{failPrepend: errors.New("goal objective cannot exceed 4000 characters")}
	tool := &GoalTool{Mode: mode, CreateAllowed: func() bool { return true }, Queue: q}
	if _, err := mode.CreateGoal(goal.CreateGoalInput{Objective: "implement ALTER TABLE"}, goal.GoalActorUser); err != nil {
		t.Fatal(err)
	}
	res, err := tool.ExecuteWithResult(`{"action":"update","status":"blocked","reason":"70 failures","expectation":"user decision"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "auto-unblock failed") {
		t.Errorf("output must surface the spawn failure: %q", res.Output)
	}
	if !strings.Contains(res.Output, "no investigation is running") {
		t.Errorf("output must clarify no investigation is running: %q", res.Output)
	}
	// The failed goal is still blocked (MarkBlocked already ran) and nothing
	// was enqueued.
	if len(q.goals) != 0 {
		t.Errorf("failed prepend must not enqueue anything, queue = %+v", q.goals)
	}
}

// TestBlocked_SpawnCapAndResumeReset pins the belt-and-braces cap (F6): two
// consecutive auto-unblock spawns with NO completion or resume in between
// stop the flow; an explicit resume resets the counter.
func TestBlocked_SpawnCapAndResumeReset(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	q := &fakeQueue{}
	tool := &GoalTool{Mode: mode, CreateAllowed: func() bool { return true }, Queue: q}

	blockStandard := func(objective string) string {
		t.Helper()
		if _, err := mode.CreateGoal(goal.CreateGoalInput{Objective: objective, Replace: true}, goal.GoalActorUser); err != nil {
			t.Fatal(err)
		}
		res, err := tool.ExecuteWithResult(`{"action":"update","status":"blocked","reason":"blocker","expectation":"input"}`)
		if err != nil {
			t.Fatal(err)
		}
		return res.Output
	}

	// Spawn #1 and #2 succeed (counter 1, 2).
	for i, objective := range []string{"goal A", "goal B"} {
		out := blockStandard(objective)
		if !strings.Contains(out, "unblocking goal was started") {
			t.Fatalf("spawn #%d: expected auto-unblock, got %q", i+1, out)
		}
	}
	// Spawn #3 hits the cap: plain block, no new queue entry, no spawn.
	out := blockStandard("goal C")
	if strings.Contains(out, "unblocking goal was started") {
		t.Errorf("spawn #3 must be capped, got %q", out)
	}
	if len(q.goals) != 2 {
		t.Errorf("capped block must not touch the queue, queue = %d goals", len(q.goals))
	}

	// An explicit resume resets the counter: the next standard block spawns
	// again (goal C is still the active-blocked goal; resume it, re-block).
	if _, err := tool.ExecuteWithResult(`{"action":"update","status":"active"}`); err != nil {
		t.Fatal(err)
	}
	out = blockStandard("goal C")
	if !strings.Contains(out, "unblocking goal was started") {
		t.Errorf("post-resume block must spawn again, got %q", out)
	}
	if len(q.goals) != 3 {
		t.Errorf("post-resume spawn must re-queue the goal, queue = %d goals", len(q.goals))
	}
}

// assertRequeuedGoal verifies the blocked goal was re-queued at the front
// with its objective, criterion, and verify command intact.
func assertRequeuedGoal(t *testing.T, g goal.UpcomingGoal, objective, criterion, verify string) {
	t.Helper()
	if g.Objective != objective {
		t.Errorf("re-queued objective = %q, want %q", g.Objective, objective)
	}
	if g.CompletionCriterion == nil || *g.CompletionCriterion != criterion {
		t.Errorf("re-queued goal lost its criterion: %+v", g.CompletionCriterion)
	}
	if g.VerifyCommand == nil || *g.VerifyCommand != verify {
		t.Errorf("re-queued goal lost its verify command: %+v", g.VerifyCommand)
	}
}

// assertUnblockGoalActive verifies the active goal is the unblocking
// investigation goal carrying the blocker context and the
// investigate→execute-or-block contract.
func assertUnblockGoalActive(t *testing.T, mode *goal.GoalMode) {
	t.Helper()
	active := mode.GetActiveGoal()
	if active == nil {
		t.Fatal("expected an active unblocking goal")
	}
	for _, want := range []string{"UNBLOCKING INVESTIGATION", "ALTER TABLE", "70 failures", "priority \"front\"", "blocked"} {
		if !strings.Contains(active.Objective, want) {
			t.Errorf("unblocking objective missing %q:\n%s", want, active.Objective)
		}
	}
}

// TestBlocked_NoQueueFallsBackToPlainBlock pins the fallback: with no queue
// wired, a justified block keeps the legacy single-goal behavior (goal stays
// active-blocked, turn stops).
func TestBlocked_NoQueueFallsBackToPlainBlock(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	if _, err := mode.CreateGoal(goal.CreateGoalInput{Objective: "x"}, goal.GoalActorUser); err != nil {
		t.Fatal(err)
	}
	tool := &GoalTool{Mode: mode, CreateAllowed: func() bool { return true }} // no Queue
	res, err := tool.ExecuteWithResult(`{"action":"update","status":"blocked","reason":"need key","expectation":"provide key"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !res.StopTurn {
		t.Error("no-queue fallback must stop the turn")
	}
	// The blocked goal remains in state (GetActiveGoal returns nil for any
	// non-active status); GetGoal surfaces it with status blocked.
	g := mode.GetGoal().Goal
	if g == nil || g.Status != goal.GoalBlocked {
		t.Fatalf("goal must stay blocked, got %+v", g)
	}
}

// TestCreate_PriorityFrontPrepend verifies create with priority "front"
// inserts the goal at the FRONT of the queue (promoted next), not the back.
func TestCreate_PriorityFrontPrepend(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	if _, err := mode.CreateGoal(goal.CreateGoalInput{Objective: "active"}, goal.GoalActorUser); err != nil {
		t.Fatal(err)
	}
	q := &fakeQueue{}
	tool := &GoalTool{Mode: mode, CreateAllowed: func() bool { return true }, Queue: q}

	// Append one normally (goes to back), then one with front priority.
	if _, err := tool.Execute(`{"action":"create","objective":"background task"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(`{"action":"create","objective":"urgent fix","priority":"front"}`); err != nil {
		t.Fatal(err)
	}
	if len(q.goals) != 2 {
		t.Fatalf("queue = %d goals: %+v", len(q.goals), q.goals)
	}
	if q.goals[0].Objective != "urgent fix" {
		t.Errorf("front-priority goal must be first, got order: %q, %q", q.goals[0].Objective, q.goals[1].Objective)
	}
	if q.goals[1].Objective != "background task" {
		t.Errorf("background goal must be second, got: %q", q.goals[1].Objective)
	}
}

// TestBlocked_AutoUnblockGateDisabled verifies the configuration gate: when
// goals.auto_unblock is off, a justified block falls back to plain blocking
// (goal parked, turn stops, no unblocking goal spawned, queue untouched).
func TestBlocked_AutoUnblockGateDisabled(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	if _, err := mode.CreateGoal(goal.CreateGoalInput{Objective: "x"}, goal.GoalActorUser); err != nil {
		t.Fatal(err)
	}
	q := &fakeQueue{}
	tool := &GoalTool{
		Mode:          mode,
		CreateAllowed: func() bool { return true },
		Queue:         q,
		AutoUnblock:   func() bool { return false }, // gate OFF
	}
	res, err := tool.ExecuteWithResult(`{"action":"update","status":"blocked","reason":"need key","expectation":"provide key"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !res.StopTurn {
		t.Error("gate-off block must stop the turn (plain block behavior)")
	}
	if len(q.goals) != 0 {
		t.Errorf("gate-off must not enqueue anything, queue = %+v", q.goals)
	}
	g := mode.GetGoal().Goal
	if g == nil || g.Status != goal.GoalBlocked {
		t.Fatalf("goal must stay blocked, got %+v", g)
	}
}

// TestBlocked_AutoUnblockGateEnabled verifies the gate default (nil = on)
// spawns the unblocking goal exactly like an unset gate.
func TestBlocked_AutoUnblockGateEnabled(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	if _, err := mode.CreateGoal(goal.CreateGoalInput{Objective: "x"}, goal.GoalActorUser); err != nil {
		t.Fatal(err)
	}
	q := &fakeQueue{}
	tool := &GoalTool{
		Mode:          mode,
		CreateAllowed: func() bool { return true },
		Queue:         q,
		AutoUnblock:   func() bool { return true }, // gate ON
	}
	if _, err := tool.ExecuteWithResult(`{"action":"update","status":"blocked","reason":"r","expectation":"e"}`); err != nil {
		t.Fatal(err)
	}
	if len(q.goals) != 1 {
		t.Fatalf("gate-on must re-queue the blocked goal at front, queue = %+v", q.goals)
	}
	if mode.GetActiveGoal() == nil {
		t.Error("gate-on must activate an unblocking goal")
	}
}
