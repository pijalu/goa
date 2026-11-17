// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUpdateVerifyCommand_PersistsAndCompletionUsesReplacement(t *testing.T) {
	store := NewFileEventStore(filepath.Join(t.TempDir(), "goal-events.jsonl"))
	mode := NewGoalMode(store, nil, nil, nil)
	mode.SetDoneGate(DoneGateEvidence)
	verifier := &fakeVerifier{ok: true, output: "ok"}
	mode.SetVerifier(verifier, true)

	oldCommand := "go test ./internal/agentic -count=1 -timeout 30s"
	newCommand := "go test ./internal/agentic -count=1 -timeout 120s"
	criterion := "agentic tests pass"
	handover := "keep this context"
	created, err := mode.CreateGoal(CreateGoalInput{
		Objective: "fix compression", CompletionCriterion: &criterion,
		VerifyCommand: &oldCommand, Handoff: &handover,
	}, GoalActorUser)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := mode.UpdateVerifyCommand(oldCommand, newCommand, GoalActorModel)
	if err != nil {
		t.Fatalf("UpdateVerifyCommand: %v", err)
	}
	requireGoalVerifyCommand(t, &updated, newCommand)
	assertVerifierUpdatePreservedGoal(t, created, updated)
	assertReplayedVerifyCommand(t, store, newCommand)

	result, err := mode.RequestComplete(context.Background(), completeEvidence(), GoalActorModel)
	if err != nil {
		t.Fatalf("RequestComplete: %v", err)
	}
	if result.Outcome != CompleteClosed {
		t.Fatalf("completion outcome = %v, want closed", result.Outcome)
	}
	if len(verifier.calls) != 1 || verifier.calls[0] != newCommand {
		t.Fatalf("verify calls = %v, want only updated command %q", verifier.calls, newCommand)
	}
}

func requireGoalVerifyCommand(t *testing.T, snapshot *GoalSnapshot, want string) {
	t.Helper()
	if snapshot == nil || snapshot.VerifyCommand == nil || *snapshot.VerifyCommand != want {
		t.Fatalf("verify command = %+v, want %q", snapshot, want)
	}
}

func assertVerifierUpdatePreservedGoal(t *testing.T, before, after GoalSnapshot) {
	t.Helper()
	if after.GoalID != before.GoalID || after.Objective != before.Objective {
		t.Fatalf("verifier update changed goal identity/scope: before=%+v after=%+v", before, after)
	}
	if after.CompletionCriterion == nil || *after.CompletionCriterion != *before.CompletionCriterion {
		t.Fatalf("verifier update changed criterion: before=%+v after=%+v", before, after)
	}
	if after.Handoff == nil || *after.Handoff != *before.Handoff {
		t.Fatalf("verifier update changed handover: before=%+v after=%+v", before, after)
	}
}

func assertReplayedVerifyCommand(t *testing.T, store EventStore, want string) {
	t.Helper()
	replayed := NewGoalMode(store, nil, nil, nil)
	if err := replayed.Replay(); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	requireGoalVerifyCommand(t, replayed.GetGoal().Goal, want)
}

const testOldVerifyCommand = "go test ./internal/agentic -timeout 30s"

func modeWithTestVerifyCommand(t *testing.T) *GoalMode {
	t.Helper()
	mode := NewGoalMode(nil, nil, nil, nil)
	command := testOldVerifyCommand
	if _, err := mode.CreateGoal(CreateGoalInput{Objective: "test", VerifyCommand: &command}, GoalActorUser); err != nil {
		t.Fatal(err)
	}
	return mode
}

func TestUpdateVerifyCommand_RejectsNoGoal(t *testing.T) {
	mode := NewGoalMode(nil, nil, nil, nil)
	if _, err := mode.UpdateVerifyCommand(testOldVerifyCommand, "go test ./internal/agentic -timeout 60s", GoalActorModel); err == nil {
		t.Fatal("update without a goal must fail")
	}
}

func TestUpdateVerifyCommand_RejectsStaleExpectedCommand(t *testing.T) {
	mode := modeWithTestVerifyCommand(t)
	if _, err := mode.UpdateVerifyCommand("go test ./other -timeout 30s", "go test ./other -timeout 60s", GoalActorModel); err == nil {
		t.Fatal("stale compare-and-swap update must fail")
	}
	if got := mode.GetActiveGoal().VerifyCommand; got == nil || *got != testOldVerifyCommand {
		t.Fatalf("rejected update changed command: %v", got)
	}
}

func TestUpdateVerifyCommand_RejectsEmptyReplacement(t *testing.T) {
	mode := modeWithTestVerifyCommand(t)
	if _, err := mode.UpdateVerifyCommand(testOldVerifyCommand, "  ", GoalActorModel); err == nil {
		t.Fatal("empty replacement must fail")
	}
}

func TestUpdateVerifyCommand_RejectsInactiveGoal(t *testing.T) {
	mode := modeWithTestVerifyCommand(t)
	if _, err := mode.PauseGoal(GoalReasonInput{}, GoalActorUser); err != nil {
		t.Fatal(err)
	}
	if _, err := mode.UpdateVerifyCommand(testOldVerifyCommand, "go test ./internal/agentic -timeout 60s", GoalActorModel); err == nil {
		t.Fatal("paused goal verifier update must fail")
	}
}

type blockingVerifier struct {
	started chan struct{}
	release chan struct{}
}

func (v *blockingVerifier) Verify(_ context.Context, _ string) VerifyOutcome {
	close(v.started)
	<-v.release
	return VerifyOutcome{OK: true, Output: "ok"}
}

func TestUpdateVerifyCommand_InFlightVerificationCannotCloseGoal(t *testing.T) {
	mode := NewGoalMode(nil, nil, nil, nil)
	mode.SetDoneGate(DoneGateEvidence)
	verifier := &blockingVerifier{started: make(chan struct{}), release: make(chan struct{})}
	mode.SetVerifier(verifier, true)
	oldCommand := "go test ./internal/agentic -timeout 30s"
	newCommand := "go test ./internal/agentic -timeout 120s"
	criterion := "tests pass"
	if _, err := mode.CreateGoal(CreateGoalInput{
		Objective: "test", CompletionCriterion: &criterion, VerifyCommand: &oldCommand,
	}, GoalActorUser); err != nil {
		t.Fatal(err)
	}

	type completionResult struct {
		result CompleteResult
		err    error
	}
	done := make(chan completionResult, 1)
	go func() {
		result, err := mode.RequestComplete(context.Background(), completeEvidence(), GoalActorModel)
		done <- completionResult{result: result, err: err}
	}()
	select {
	case <-verifier.started:
	case <-time.After(2 * time.Second):
		t.Fatal("verifier did not start")
	}
	if _, err := mode.UpdateVerifyCommand(oldCommand, newCommand, GoalActorModel); err != nil {
		t.Fatalf("UpdateVerifyCommand during verification: %v", err)
	}
	close(verifier.release)
	select {
	case completed := <-done:
		if completed.err == nil || !strings.Contains(completed.err.Error(), "changed during verification") {
			t.Fatalf("stale verification result = %+v, err=%v; want changed-command error", completed.result, completed.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("completion did not finish")
	}
	if got := mode.GetActiveGoal(); got == nil || got.VerifyCommand == nil || *got.VerifyCommand != newCommand {
		t.Fatalf("goal after stale verification = %+v, want active with updated command", got)
	}
}
