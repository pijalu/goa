// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"strings"
	"testing"
	"time"

	coregoal "github.com/pijalu/goa/core/goal"
)

const (
	oldVerifyTimeoutCommand = "go test ./internal/agentic -count=1 -timeout 30s"
	newVerifyTimeoutCommand = "go test ./internal/agentic -count=1 -timeout 120s"
)

func newVerifyCommandUpdateTool(t *testing.T) (*GoalTool, *coregoal.GoalMode) {
	t.Helper()
	mode := coregoal.NewGoalMode(nil, nil, nil, nil)
	criterion := "agentic tests pass"
	command := oldVerifyTimeoutCommand
	if _, err := mode.CreateGoal(coregoal.CreateGoalInput{
		Objective: "fix compression", CompletionCriterion: &criterion, VerifyCommand: &command,
	}, coregoal.GoalActorUser); err != nil {
		t.Fatal(err)
	}
	return &GoalTool{
		Mode:               mode,
		VerifyTimeoutLimit: func() time.Duration { return 10 * time.Minute },
	}, mode
}

func TestUpdate_VerifyCommandTimeout(t *testing.T) {
	tool, mode := newVerifyCommandUpdateTool(t)
	out, err := tool.Execute(`{"action":"update","verifyCommand":"` + newVerifyTimeoutCommand + `"}`)
	if err != nil {
		t.Fatalf("verifier-only update: %v", err)
	}
	if !strings.Contains(out, newVerifyTimeoutCommand) {
		t.Fatalf("output must confirm updated command, got %q", out)
	}
	got := mode.GetActiveGoal()
	if got == nil || got.VerifyCommand == nil || *got.VerifyCommand != newVerifyTimeoutCommand {
		t.Fatalf("active goal after update = %+v", got)
	}
	if got.Objective != "fix compression" || got.CompletionCriterion == nil || *got.CompletionCriterion != "agentic tests pass" {
		t.Fatalf("update changed goal scope: %+v", got)
	}
}

func TestUpdate_VerifyCommandTimeoutValidation(t *testing.T) {
	tests := []struct {
		name    string
		command string
		wantErr string
	}{
		{name: "package scope widened", command: "go test ./... -count=1 -timeout 120s", wantErr: "only its timeout"},
		{name: "package scope narrowed", command: "go test ./internal/agentic/provider -count=1 -timeout 120s", wantErr: "only its timeout"},
		{name: "test selection added", command: "go test ./internal/agentic -count=1 -run TestCompression -timeout 120s", wantErr: "only its timeout"},
		{name: "other flag changed", command: "go test ./internal/agentic -count=2 -timeout 120s", wantErr: "only its timeout"},
		{name: "above authorized bound", command: "go test ./internal/agentic -count=1 -timeout 601s", wantErr: "at most 10m0s"},
		{name: "zero timeout", command: "go test ./internal/agentic -count=1 -timeout 0s", wantErr: "positive"},
		{name: "malformed timeout", command: "go test ./internal/agentic -count=1 -timeout soon", wantErr: "invalid"},
		{name: "missing new timeout", command: "go test ./internal/agentic -count=1", wantErr: "timeout"},
		{name: "different executable", command: "gotestsum ./internal/agentic -count=1 -timeout 120s", wantErr: "go test"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tool, mode := newVerifyCommandUpdateTool(t)
			_, err := tool.Execute(`{"action":"update","verifyCommand":"` + tc.command + `"}`)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("update error = %v, want substring %q", err, tc.wantErr)
			}
			if got := mode.GetActiveGoal().VerifyCommand; got == nil || *got != oldVerifyTimeoutCommand {
				t.Fatalf("rejected update changed command: %v", got)
			}
		})
	}
}

func TestUpdate_VerifyCommandTimeoutEquivalentForms(t *testing.T) {
	mode := coregoal.NewGoalMode(nil, nil, nil, nil)
	oldCommand := `go test "./internal/agentic" -count=1 --timeout=30s`
	if _, err := mode.CreateGoal(coregoal.CreateGoalInput{Objective: "test", VerifyCommand: &oldCommand}, coregoal.GoalActorUser); err != nil {
		t.Fatal(err)
	}
	tool := &GoalTool{Mode: mode, VerifyTimeoutLimit: func() time.Duration { return 10 * time.Minute }}
	newCommand := `go test './internal/agentic' -count=1 -timeout 120s`
	if _, err := tool.Execute(`{"action":"update","verifyCommand":"` + newCommand + `"}`); err != nil {
		t.Fatalf("equivalent quoted command and flag form must be accepted: %v", err)
	}
}

func TestUpdate_VerifyCommandRejectsCombinedStatusAndEmptyValues(t *testing.T) {
	t.Run("combined status", func(t *testing.T) {
		tool, mode := newVerifyCommandUpdateTool(t)
		_, err := tool.Execute(`{"action":"update","status":"complete","reason":"done","verifyCommand":"` + newVerifyTimeoutCommand + `"}`)
		if err == nil || !strings.Contains(err.Error(), "separate") {
			t.Fatalf("combined update error = %v, want separate-call guidance", err)
		}
		if mode.GetActiveGoal() == nil {
			t.Fatal("combined update must not complete goal")
		}
	})

	t.Run("empty command", func(t *testing.T) {
		tool, _ := newVerifyCommandUpdateTool(t)
		if _, err := tool.Execute(`{"action":"update","verifyCommand":""}`); err == nil {
			t.Fatal("empty verifyCommand must be rejected")
		}
	})
}
