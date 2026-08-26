// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestParseDoneGate(t *testing.T) {
	cases := []struct {
		in   string
		want DoneGateMode
		ok   bool
	}{
		{"", DoneGateVerify, true},
		{"verify", DoneGateVerify, true},
		{"VERIFY", DoneGateVerify, true},
		{" evidence ", DoneGateEvidence, true},
		{"off", DoneGateOff, true},
		{"strict", "", false},
		{"on", "", false},
	}
	for _, tc := range cases {
		got, ok := ParseDoneGate(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Errorf("ParseDoneGate(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

// fakeVerifier records calls and returns a scripted result.
type fakeVerifier struct {
	ok     bool
	output string
	calls  []string
}

func (v *fakeVerifier) Verify(_ context.Context, command string) VerifyOutcome {
	v.calls = append(v.calls, command)
	return VerifyOutcome{Output: v.output, OK: v.ok, DurationMs: 5, TimeoutMs: 120000}
}

// fakeJudge returns a scripted verdict.
type fakeJudge struct {
	pass      bool
	rationale string
	err       error
	calls     int
}

func (j *fakeJudge) Judge(_ context.Context, _ JudgeInput) (JudgeVerdict, error) {
	j.calls++
	return JudgeVerdict{Pass: j.pass, Rationale: j.rationale}, j.err
}

// gatedMode creates a goal with a completion criterion and the given gate.
func gatedMode(t *testing.T, gate DoneGateMode) *GoalMode {
	t.Helper()
	m := NewGoalMode(nil, nil, nil, nil)
	m.SetDoneGate(gate)
	criterion := "go test ./... passes"
	if _, err := m.CreateGoal(CreateGoalInput{
		Objective:           "make the build green",
		CompletionCriterion: &criterion,
	}, GoalActorUser); err != nil {
		t.Fatal(err)
	}
	return m
}

func completeEvidence() GoalReasonInput {
	evidence := "ran go test ./...: all packages ok"
	return GoalReasonInput{Reason: &evidence}
}

func TestRequestComplete_VerifyChallengesThenCloses(t *testing.T) {
	m := gatedMode(t, DoneGateVerify)
	assertVerifyChallenge(t, m)
	assertEvidenceClosesGoal(t, m)
}

// assertVerifyChallenge pins the first model complete in verify mode: it must
// be challenged, leave the goal active, and a reason-less retry must fail.
func assertVerifyChallenge(t *testing.T, m *GoalMode) {
	t.Helper()
	res, err := m.RequestComplete(context.Background(), GoalReasonInput{}, GoalActorModel)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != CompleteChallenged {
		t.Fatalf("first complete must be challenged in verify mode, got %v", res.Outcome)
	}
	if res.Snapshot == nil || res.Snapshot.Status != GoalActive {
		t.Fatalf("goal must stay active after challenge, got %+v", res.Snapshot)
	}
	if m.GetActiveGoal() == nil {
		t.Fatal("goal was cleared despite pending verification")
	}
	if _, err := m.RequestComplete(context.Background(), GoalReasonInput{}, GoalActorModel); err == nil {
		t.Fatal("second complete without reason must fail")
	}
}

// assertEvidenceClosesGoal completes with evidence and verifies the goal is
// closed with the evidence recorded as terminal reason.
func assertEvidenceClosesGoal(t *testing.T, m *GoalMode) {
	t.Helper()
	res, err := m.RequestComplete(context.Background(), completeEvidence(), GoalActorModel)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != CompleteClosed {
		t.Fatalf("confirmed complete must close, got %v", res.Outcome)
	}
	if res.Snapshot == nil || res.Snapshot.Status != GoalDone {
		t.Fatalf("expected completed snapshot, got %+v", res.Snapshot)
	}
	if res.Snapshot.TerminalReason == nil || !strings.Contains(*res.Snapshot.TerminalReason, "go test") {
		t.Errorf("evidence must be recorded as terminal reason, got %v", res.Snapshot.TerminalReason)
	}
	if m.GetGoal().Goal != nil {
		t.Error("goal record must be cleared after completion")
	}
}

func TestRequestComplete_VerifyChallengeContent(t *testing.T) {
	m := gatedMode(t, DoneGateVerify)
	res, err := m.RequestComplete(context.Background(), GoalReasonInput{}, GoalActorModel)
	if err != nil || res.Outcome != CompleteChallenged {
		t.Fatalf("expected challenge, got %v err=%v", res.Outcome, err)
	}
	challenge := BuildVerificationChallenge(*res.Snapshot)
	if !strings.Contains(challenge, "go test ./... passes") {
		t.Error("challenge must restate the completion criterion")
	}
	if !strings.Contains(challenge, "reason") {
		t.Error("challenge must instruct the model to re-call complete with reason")
	}
}

func TestRequestComplete_EvidenceRequiresReasonSingleCall(t *testing.T) {
	m := gatedMode(t, DoneGateEvidence)
	if _, err := m.RequestComplete(context.Background(), GoalReasonInput{}, GoalActorModel); err == nil {
		t.Fatal("evidence mode must reject a reason-less complete")
	}
	if m.GetActiveGoal() == nil {
		t.Fatal("goal must stay active after rejected complete")
	}
	res, err := m.RequestComplete(context.Background(), completeEvidence(), GoalActorModel)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != CompleteClosed {
		t.Fatalf("evidence mode with reason must close, got %v", res.Outcome)
	}
}

func TestRequestComplete_OffClosesImmediately(t *testing.T) {
	m := gatedMode(t, DoneGateOff)
	res, err := m.RequestComplete(context.Background(), GoalReasonInput{}, GoalActorModel)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != CompleteClosed {
		t.Fatalf("gate off must close immediately, got %v", res.Outcome)
	}
}

func TestRequestComplete_SkippedActorsAndStates(t *testing.T) {
	// User-initiated completion bypasses the gate even with a criterion.
	m := gatedMode(t, DoneGateVerify)
	res, err := m.RequestComplete(context.Background(), GoalReasonInput{}, GoalActorUser)
	if err != nil || res.Outcome != CompleteClosed {
		t.Fatalf("user completion must bypass gate: %v err=%v", res.Outcome, err)
	}

	// No criterion recorded: nothing to verify, closes immediately.
	m2 := NewGoalMode(nil, nil, nil, nil)
	m2.SetDoneGate(DoneGateVerify)
	if _, err := m2.CreateGoal(CreateGoalInput{Objective: "no criterion"}, GoalActorUser); err != nil {
		t.Fatal(err)
	}
	res, err = m2.RequestComplete(context.Background(), GoalReasonInput{}, GoalActorModel)
	if err != nil || res.Outcome != CompleteClosed {
		t.Fatalf("criterion-less goal must bypass gate: %v err=%v", res.Outcome, err)
	}

	// Orchestrator-managed goal: bypasses gate (the orchestrator evaluates).
	m3 := NewGoalMode(nil, nil, nil, nil)
	m3.SetDoneGate(DoneGateVerify)
	criterion := "stage done"
	if _, err := m3.CreateGoal(CreateGoalInput{Objective: "stage", ManagedBy: "orchestrator", CompletionCriterion: &criterion}, GoalActorUser); err != nil {
		t.Fatal(err)
	}
	res, err = m3.RequestComplete(context.Background(), GoalReasonInput{}, GoalActorModel)
	if err != nil || res.Outcome != CompleteClosed {
		t.Fatalf("managed goal must bypass gate: %v err=%v", res.Outcome, err)
	}

	// No active goal: CompleteNoGoal, no error.
	m4 := NewGoalMode(nil, nil, nil, nil)
	res, err = m4.RequestComplete(context.Background(), GoalReasonInput{}, GoalActorModel)
	if err != nil || res.Outcome != CompleteNoGoal || res.Snapshot != nil {
		t.Fatalf("no goal: want NoGoal/nil/nil, got (%v,%v,%v)", res.Outcome, res.Snapshot, err)
	}
}

func TestRequestComplete_PauseAfterChallengeReArmsGate(t *testing.T) {
	m := gatedMode(t, DoneGateVerify)
	if res, _ := m.RequestComplete(context.Background(), GoalReasonInput{}, GoalActorModel); res.Outcome != CompleteChallenged {
		t.Fatal("expected challenge")
	}
	reason := "need to think"
	if _, err := m.PauseGoal(GoalReasonInput{Reason: &reason}, GoalActorModel); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ResumeGoal(GoalReasonInput{}, GoalActorModel); err != nil {
		t.Fatal(err)
	}
	res, err := m.RequestComplete(context.Background(), GoalReasonInput{}, GoalActorModel)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != CompleteChallenged {
		t.Fatal("pause/resume cycle must re-arm the verification challenge")
	}
}

func TestRequestComplete_BlockedAfterChallengeReArmsGate(t *testing.T) {
	m := gatedMode(t, DoneGateVerify)
	if res, _ := m.RequestComplete(context.Background(), GoalReasonInput{}, GoalActorModel); res.Outcome != CompleteChallenged {
		t.Fatal("expected challenge")
	}
	reason := "missing API key"
	expectation := "user provides OPENAI_API_KEY"
	if _, err := m.MarkBlocked(GoalReasonInput{Reason: &reason, Expectation: &expectation}, GoalActorModel); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ResumeGoal(GoalReasonInput{}, GoalActorModel); err != nil {
		t.Fatal(err)
	}
	if res, _ := m.RequestComplete(context.Background(), GoalReasonInput{}, GoalActorModel); res.Outcome != CompleteChallenged {
		t.Fatal("blocked/resume cycle must re-arm the verification challenge")
	}
}

// TestRequestComplete_TodoConsistency blocks completion while the goal's own
// checklist is unfinished (gated modes).
func TestRequestComplete_TodoConsistency(t *testing.T) {
	m := gatedMode(t, DoneGateEvidence)
	item, err := m.AddGoalTodo("write tests", GoalActorModel)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.RequestComplete(context.Background(), completeEvidence(), GoalActorModel); err == nil {
		t.Fatal("complete with open todos must fail")
	} else if !strings.Contains(err.Error(), "todo") {
		t.Fatalf("error must mention todos: %v", err)
	}
	if m.GetActiveGoal() == nil {
		t.Fatal("goal must stay active after todo rejection")
	}
	if _, err := m.UpdateGoalTodo(item.ID, TodoDone, GoalActorModel); err != nil {
		t.Fatal(err)
	}
	res, err := m.RequestComplete(context.Background(), completeEvidence(), GoalActorModel)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != CompleteClosed {
		t.Fatalf("all todos done: completion must close, got %v", res.Outcome)
	}
}

// TestRequestComplete_VerifyCommandPass covers the machine-checked path:
// evidence confirmed, command exits 0, goal closes, command actually ran.
func TestRequestComplete_VerifyCommandPass(t *testing.T) {
	m := gatedMode(t, DoneGateEvidence)
	verifier := &fakeVerifier{ok: true, output: "ok\t12 packages"}
	m.SetVerifier(verifier, true)
	cmd := "go test ./..."
	// Re-create the goal with a verify command.
	if _, err := m.CreateGoal(CreateGoalInput{
		Objective:           "make the build green",
		CompletionCriterion: ptrString("go test ./... passes"),
		VerifyCommand:       &cmd,
		Replace:             true,
	}, GoalActorUser); err != nil {
		t.Fatal(err)
	}
	res, err := m.RequestComplete(context.Background(), completeEvidence(), GoalActorModel)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != CompleteClosed {
		t.Fatalf("passing verify command must close the goal, got %v", res.Outcome)
	}
	if len(verifier.calls) != 1 || verifier.calls[0] != cmd {
		t.Errorf("verify command must run exactly once with the recorded command: %v", verifier.calls)
	}
	// Bug A: the command evidence must travel with the result so the user can
	// see exactly what ran, its output, and the applied timeout.
	if res.Verification == nil {
		t.Fatal("CompleteClosed must carry the verification evidence")
	}
	if res.Verification.Command != cmd {
		t.Errorf("evidence command = %q, want %q", res.Verification.Command, cmd)
	}
	if res.Verification.Output != "ok\t12 packages" {
		t.Errorf("evidence output = %q", res.Verification.Output)
	}
	if res.Verification.TimeoutMs != 120000 {
		t.Errorf("evidence timeout = %d, want 120000", res.Verification.TimeoutMs)
	}
}

// TestRequestComplete_VerifyCommandFailKeepsActive covers the rejection path:
// non-zero exit keeps the goal active with the output tail as detail.
func TestRequestComplete_VerifyCommandFailKeepsActive(t *testing.T) {
	m := gatedMode(t, DoneGateEvidence)
	verifier := &fakeVerifier{ok: false, output: "FAIL: TestCheckout\nexit status 1"}
	m.SetVerifier(verifier, true)
	cmd := "go test ./..."
	if _, err := m.CreateGoal(CreateGoalInput{
		Objective:           "make the build green",
		CompletionCriterion: ptrString("go test ./... passes"),
		VerifyCommand:       &cmd,
		Replace:             true,
	}, GoalActorUser); err != nil {
		t.Fatal(err)
	}
	res, err := m.RequestComplete(context.Background(), completeEvidence(), GoalActorModel)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != CompleteVerifyFailed {
		t.Fatalf("failing verify command must reject completion, got %v", res.Outcome)
	}
	if res.Failure == nil || res.Failure.Kind != "command" {
		t.Fatalf("failure kind = %+v", res.Failure)
	}
	if !strings.Contains(res.Failure.Detail, "FAIL: TestCheckout") {
		t.Errorf("failure detail must contain the output tail: %q", res.Failure.Detail)
	}
	if res.Failure.Streak != 1 || res.Failure.Escalated {
		t.Errorf("streak=%d escalated=%v, want 1/false", res.Failure.Streak, res.Failure.Escalated)
	}
	if m.GetActiveGoal() == nil {
		t.Fatal("goal must stay active after verification failure")
	}
	msg := BuildVerifyFailureMessage(res)
	if !strings.Contains(msg, "FAIL: TestCheckout") || !strings.Contains(msg, "#1") {
		t.Errorf("failure message must carry detail and streak: %q", msg)
	}
}

// TestRequestComplete_EscalationAutoBlocks: at the configured failure cap the
// goal is auto-blocked for user review instead of retrying forever.
func TestRequestComplete_EscalationAutoBlocks(t *testing.T) {
	m := gatedMode(t, DoneGateEvidence)
	m.SetMaxVerifyFailures(2)
	verifier := &fakeVerifier{ok: false, output: "still failing"}
	m.SetVerifier(verifier, true)
	createVerifyGoal(t, m, "make the build green", "go test ./... passes")

	res, err := m.RequestComplete(context.Background(), completeEvidence(), GoalActorModel)
	if err != nil || res.Outcome != CompleteVerifyFailed || res.Failure.Escalated {
		t.Fatalf("first failure: %v %+v err=%v", res.Outcome, res.Failure, err)
	}
	res, err = m.RequestComplete(context.Background(), completeEvidence(), GoalActorModel)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != CompleteVerifyFailed || !res.Failure.Escalated {
		t.Fatalf("second failure must escalate, got %v %+v", res.Outcome, res.Failure)
	}
	assertAutoBlocked(t, res)
}

// createVerifyGoal seeds a goal with criterion + verify command for the gate
// tests; failures here abort the test immediately.
func createVerifyGoal(t *testing.T, m *GoalMode, objective, criterion string) {
	t.Helper()
	crit, cmd := criterion, "go test ./..."
	if _, err := m.CreateGoal(CreateGoalInput{
		Objective:           objective,
		CompletionCriterion: &crit,
		VerifyCommand:       &cmd,
		Replace:             true,
	}, GoalActorUser); err != nil {
		t.Fatal(err)
	}
}

// assertAutoBlocked pins the escalation side effects: blocked status with a
// user-review expectation surfaced in snapshot and failure message.
func assertAutoBlocked(t *testing.T, res CompleteResult) {
	t.Helper()
	if res.Snapshot == nil || res.Snapshot.Status != GoalBlocked {
		t.Fatalf("escalation must auto-block, got %+v", res.Snapshot)
	}
	if res.Snapshot.TerminalExpectation == nil || !strings.Contains(*res.Snapshot.TerminalExpectation, "user review") {
		t.Errorf("escalation expectation = %v", res.Snapshot.TerminalExpectation)
	}
	msg := BuildVerifyFailureMessage(res)
	if !strings.Contains(msg, "auto-BLOCKED") {
		t.Errorf("escalated message must say the goal was blocked: %q", msg)
	}
}

// TestRequestComplete_JudgeRejects keeps the goal active on a judge FAIL;
// a judge error is fail-open (completion proceeds).
func TestRequestComplete_JudgeVerdicts(t *testing.T) {
	m := gatedMode(t, DoneGateEvidence)
	judge := &fakeJudge{pass: false, rationale: "no test output was actually observed"}
	m.SetJudge(judge)
	res, err := m.RequestComplete(context.Background(), completeEvidence(), GoalActorModel)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != CompleteVerifyFailed || res.Failure.Kind != "judge" {
		t.Fatalf("judge FAIL must reject: %v %+v", res.Outcome, res.Failure)
	}
	if !strings.Contains(res.Failure.Detail, "no test output") {
		t.Errorf("failure must carry the rationale: %q", res.Failure.Detail)
	}

	// Fail-open on judge error.
	m2 := gatedMode(t, DoneGateEvidence)
	m2.SetJudge(&fakeJudge{err: errors.New("provider down")})
	res, err = m2.RequestComplete(context.Background(), completeEvidence(), GoalActorModel)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != CompleteClosed {
		t.Fatalf("judge error must be fail-open, got %v", res.Outcome)
	}

	// Pass closes.
	m3 := gatedMode(t, DoneGateEvidence)
	m3.SetJudge(&fakeJudge{pass: true})
	res, err = m3.RequestComplete(context.Background(), completeEvidence(), GoalActorModel)
	if err != nil || res.Outcome != CompleteClosed {
		t.Fatalf("judge PASS must close, got %v err=%v", res.Outcome, err)
	}
}

// TestRequestComplete_FailureStreakResetsOnStatusChange: a pause after a
// verification failure resets the streak (and re-arms the challenge).
func TestRequestComplete_FailureStreakResetsOnStatusChange(t *testing.T) {
	m := gatedMode(t, DoneGateEvidence)
	m.SetMaxVerifyFailures(2)
	verifier := &fakeVerifier{ok: false, output: "nope"}
	m.SetVerifier(verifier, true)
	cmd := "go test ./..."
	if _, err := m.CreateGoal(CreateGoalInput{
		Objective:           "make the build green",
		CompletionCriterion: ptrString("tests pass"),
		VerifyCommand:       &cmd,
		Replace:             true,
	}, GoalActorUser); err != nil {
		t.Fatal(err)
	}
	if res, _ := m.RequestComplete(context.Background(), completeEvidence(), GoalActorModel); res.Failure == nil || res.Failure.Streak != 1 {
		t.Fatalf("first failure streak = %+v", res.Failure)
	}
	reason := "regrouping"
	if _, err := m.PauseGoal(GoalReasonInput{Reason: &reason}, GoalActorModel); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ResumeGoal(GoalReasonInput{}, GoalActorModel); err != nil {
		t.Fatal(err)
	}
	res, _ := m.RequestComplete(context.Background(), completeEvidence(), GoalActorModel)
	if res.Failure == nil || res.Failure.Streak != 1 || res.Failure.Escalated {
		t.Fatalf("streak must reset after pause/resume, got %+v", res.Failure)
	}
}

// TestRunVerifyCommand covers the /goal:verify audit surface.
func TestRunVerifyCommand(t *testing.T) {
	m := NewGoalMode(nil, nil, nil, nil)
	if _, _, err := m.RunVerifyCommand(context.Background()); err == nil {
		t.Error("no goal: must error")
	}
	cmd := "go test ./..."
	if _, err := m.CreateGoal(CreateGoalInput{Objective: "x", VerifyCommand: &cmd}, GoalActorUser); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.RunVerifyCommand(context.Background()); err == nil {
		t.Error("no verifier wired: must error")
	}
	verifier := &fakeVerifier{ok: true, output: "all good"}
	m.SetVerifier(verifier, true)
	out, ok, err := m.RunVerifyCommand(context.Background())
	if err != nil || !ok || out != "all good" {
		t.Errorf("RunVerifyCommand = (%q,%v,%v)", out, ok, err)
	}
	if len(verifier.calls) != 1 || verifier.calls[0] != cmd {
		t.Errorf("calls = %v", verifier.calls)
	}

	// Disabled verifier: error, no execution.
	m.SetVerifier(verifier, false)
	if _, _, err := m.RunVerifyCommand(context.Background()); err == nil {
		t.Error("disabled verification must error")
	}
	if len(verifier.calls) != 1 {
		t.Error("disabled verification must not execute the command")
	}
}

// TestEventLog covers the /goal:log audit surface.
func TestEventLog(t *testing.T) {
	m := NewGoalMode(nil, nil, nil, nil)
	if records, err := m.EventLog(); err != nil || records != nil {
		t.Errorf("store-less mode: want nil/nil, got %v/%v", records, err)
	}
	st := &testStore{}
	m2 := NewGoalMode(st, nil, nil, nil)
	if _, err := m2.CreateGoal(CreateGoalInput{Objective: "x"}, GoalActorUser); err != nil {
		t.Fatal(err)
	}
	records, err := m2.EventLog()
	if err != nil || len(records) == 0 {
		t.Fatalf("EventLog = %d records, err %v", len(records), err)
	}
	if records[0].Type != GoalEventCreate {
		t.Errorf("first record = %q", records[0].Type)
	}
}
