// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"errors"
	"testing"

	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
)

func TestUpdateGoalTool(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	tool := &UpdateGoalTool{Mode: mode, ReminderFn: func(s string) {}}

	create := &CreateGoalTool{Mode: mode}
	if _, err := create.Execute(`{"objective":"fix tests"}`); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		status     string
		wantStop   bool
		wantOutput string
	}{
		{"paused", true, "Goal paused."},
		{"active", false, "Goal resumed."},
		{"blocked", true, "Goal marked blocked."},
		{"complete", true, "Goal marked complete."},
	}
	for _, tc := range cases {
		if _, err := create.Execute(`{"objective":"fix tests","replace":true}`); err != nil {
			t.Fatal(err)
		}
		res, err := tool.ExecuteWithResult(`{"status":"` + tc.status + `"}`)
		if err != nil {
			t.Fatalf("status %s: %v", tc.status, err)
		}
		if res.StopTurn != tc.wantStop {
			t.Errorf("status %s StopTurn = %v", tc.status, res.StopTurn)
		}
		if res.Output != tc.wantOutput {
			t.Errorf("status %s Output = %q", tc.status, res.Output)
		}
	}
}

func TestUpdateGoalTool_InvalidStatus(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	tool := &UpdateGoalTool{Mode: mode}
	_, err := tool.ExecuteWithResult(`{"status":"unknown"}`)
	if err == nil {
		t.Error("expected error for invalid status")
	}
}

func TestUpdateGoalTool_InvalidJSON(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	tool := &UpdateGoalTool{Mode: mode}
	_, err := tool.ExecuteWithResult(`not-json`)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestUpdateGoalTool_NoGoal(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	tool := &UpdateGoalTool{Mode: mode}
	// core/goal now returns an error (no panic) when no goal is active; AP-04
	// requires every Execute error to be a *internal.ToolError.
	_, err := tool.ExecuteWithResult(`{"status":"paused"}`)
	if err == nil {
		t.Fatal("expected error when no goal exists")
	}
	var te *internal.ToolError
	if !errors.As(err, &te) {
		t.Errorf("expected *internal.ToolError, got %T: %v", err, err)
	}
}

func TestUpdateGoalTool_ReminderCalled(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	called := false
	tool := &UpdateGoalTool{Mode: mode, ReminderFn: func(string) { called = true }}
	create := &CreateGoalTool{Mode: mode}
	create.Execute(`{"objective":"x"}`)
	tool.ExecuteWithResult(`{"status":"complete"}`)
	if !called {
		t.Error("reminder not called on complete")
	}
}

func TestUpdateGoalTool_IsRetryable(t *testing.T) {
	tool := &UpdateGoalTool{}
	if tool.IsRetryable(nil) {
		t.Error("expected non-retryable")
	}
}

func TestUpdateGoalTool_ResultToolInterface(t *testing.T) {
	var _ agentic.ResultTool = &UpdateGoalTool{}
}

func TestUpdateGoalTool_Execute(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	tool := &UpdateGoalTool{Mode: mode, ReminderFn: func(string) {}}
	create := &CreateGoalTool{Mode: mode}
	create.Execute(`{"objective":"x"}`)
	out, err := tool.Execute(`{"status":"complete"}`)
	if err != nil {
		t.Fatal(err)
	}
	if out != "Goal marked complete." {
		t.Errorf("output = %q", out)
	}
}

func TestUpdateGoalTool_HandleActiveAlreadyActive(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	create := &CreateGoalTool{Mode: mode}
	create.Execute(`{"objective":"x"}`)
	tool := &UpdateGoalTool{Mode: mode}
	res, err := tool.ExecuteWithResult(`{"status":"active"}`)
	if err != nil {
		t.Fatal(err)
	}
	if res.Output != "Goal resumed." {
		t.Errorf("output = %q", res.Output)
	}
}

func TestUpdateGoalTool_Description(t *testing.T) {
	if UpdateGoalDescription() == "" {
		t.Error("description empty")
	}
}

func TestUpdateGoalTool_Schema(t *testing.T) {
	tool := &UpdateGoalTool{}
	schema := tool.Schema()
	if schema.Name != "UpdateGoal" {
		t.Errorf("name = %q", schema.Name)
	}
}
