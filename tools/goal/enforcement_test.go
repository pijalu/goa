// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
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
