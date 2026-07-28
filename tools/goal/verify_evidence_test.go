// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/internal/agentic"
)

// fakeCmdVerifier returns a scripted verify outcome.
type fakeCmdVerifier struct {
	outcome goal.VerifyOutcome
}

func (f *fakeCmdVerifier) Verify(_ context.Context, _ string) goal.VerifyOutcome {
	return f.outcome
}

// newVerifyingTool builds a tool with an active criterion+verifyCommand goal
// and a scripted verifier, for the Bug A evidence tests.
func newVerifyingTool(t *testing.T, outcome goal.VerifyOutcome) (*GoalTool, *goal.GoalMode) {
	t.Helper()
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	mode.SetDoneGate(goal.DefaultDoneGate)
	mode.SetVerifier(&fakeCmdVerifier{outcome: outcome}, true)
	criterion := "tests pass"
	cmd := "go test ./..."
	if _, err := mode.CreateGoal(goal.CreateGoalInput{
		Objective:           "make the build green",
		CompletionCriterion: &criterion,
		VerifyCommand:       &cmd,
	}, goal.GoalActorUser); err != nil {
		t.Fatal(err)
	}
	tool := &GoalTool{
		Mode:          mode,
		CreateAllowed: func() bool { return true },
		VerifyTimeout: func() time.Duration { return 90 * time.Second },
	}
	return tool, mode
}

// TestGoalTool_Complete_AnnouncesAndReportsVerification is the bugs.md Bug A
// regression: the confirming completion ANNOUNCES the verify command live
// (exact command + timeout) and the result carries the full evidence block
// (command, elapsed, timeout, output tail) — the user can follow exactly what
// validated the goal.
func TestGoalTool_Complete_AnnouncesAndReportsVerification(t *testing.T) {
	tool, _ := newVerifyingTool(t, goal.VerifyOutcome{
		Output: "ok  \tgithub.com/x/y\t0.332s", OK: true, DurationMs: 332, TimeoutMs: 90000,
	})

	// First complete → challenged (no progress, no evidence).
	res, err := tool.ExecuteWithResult(`{"action":"update","status":"complete"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "Completion verification required") {
		t.Fatalf("first call must be the challenge, got: %q", res.Output)
	}

	// Confirming complete with evidence reason → announce + run + evidence.
	var progress []string
	ctx := agentic.WithProgress(context.Background(), func(partial string) {
		progress = append(progress, partial)
	})
	res, err = tool.ExecuteContextWithResult(ctx, `{"action":"update","status":"complete","reason":"go test ./... passes locally"}`)
	if err != nil {
		t.Fatal(err)
	}

	// Live announcement: exact command + timeout.
	if len(progress) != 1 {
		t.Fatalf("expected exactly one progress announcement, got %v", progress)
	}
	if !strings.Contains(progress[0], "Running goal verification") ||
		!strings.Contains(progress[0], "$ go test ./...") ||
		!strings.Contains(progress[0], "timeout 1m30s") {
		t.Errorf("progress must name command + timeout, got: %q", progress[0])
	}

	// Result evidence block.
	if !res.StopTurn {
		t.Error("completion must still stop the turn")
	}
	for _, want := range []string{
		"Goal marked complete.",
		"Verification passed in 0.3s",
		"(timeout 1m30s)",
		"$ go test ./...",
		"ok  \tgithub.com/x/y\t0.332s",
	} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("result missing %q, got:\n%s", want, res.Output)
		}
	}
}

// TestGoalTool_Complete_NoEvidenceWithoutCommand completes a goal with no
// verify command: the result stays the plain one-liner (no evidence block).
func TestGoalTool_Complete_NoEvidenceWithoutCommand(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	mode.SetDoneGate(goal.DoneGateOff) // no gate → immediate close
	criterion := "reviewed"
	if _, err := mode.CreateGoal(goal.CreateGoalInput{
		Objective:           "review the change",
		CompletionCriterion: &criterion,
	}, goal.GoalActorUser); err != nil {
		t.Fatal(err)
	}
	tool := &GoalTool{Mode: mode, CreateAllowed: func() bool { return true }}

	res, err := tool.ExecuteWithResult(`{"action":"update","status":"complete","reason":"reviewed the diff"}`)
	if err != nil {
		t.Fatal(err)
	}
	if res.Output != "Goal marked complete." {
		t.Errorf("plain completion expected, got: %q", res.Output)
	}
}

// TestGoalTool_Complete_VerifyFailureShowsOutput covers the failure path:
// the goal stays active and the message carries the output tail (pre-existing
// behavior — asserted here so the Bug A evidence change cannot weaken it).
func TestGoalTool_Complete_VerifyFailureShowsOutput(t *testing.T) {
	tool, mode := newVerifyingTool(t, goal.VerifyOutcome{
		Output: "FAIL\tgithub.com/x/y\t0.1s\nFAIL: TestCheckout", OK: false, DurationMs: 100, TimeoutMs: 90000,
	})
	if _, err := tool.ExecuteWithResult(`{"action":"update","status":"complete"}`); err != nil {
		t.Fatal(err)
	}
	res, err := tool.ExecuteContextWithResult(context.Background(),
		`{"action":"update","status":"complete","reason":"claimed green"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "verify command FAILED") || !strings.Contains(res.Output, "FAIL: TestCheckout") {
		t.Errorf("failure message must carry the output tail, got:\n%s", res.Output)
	}
	if g := mode.GetActiveGoal(); g == nil {
		t.Error("goal must stay active after a failed verification")
	}
}
