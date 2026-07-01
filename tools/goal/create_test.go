// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"encoding/json"
	"testing"

	"github.com/pijalu/goa/core/goal"
)

func TestCreateGoalTool(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	tool := &CreateGoalTool{Mode: mode}

	input := `{"objective":"Fix tests","completionCriterion":"All tests pass"}`
	out, err := tool.Execute(input)
	if err != nil {
		t.Fatal(err)
	}

	var result struct {
		Goal map[string]any `json:"goal"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.Goal["objective"] != "Fix tests" {
		t.Errorf("objective = %v, want Fix tests", result.Goal["objective"])
	}
	if _, ok := result.Goal["goalId"]; ok {
		t.Error("goalId should be stripped from model output")
	}
}

func TestCreateGoalTool_EmptyObjective(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	tool := &CreateGoalTool{Mode: mode}

	_, err := tool.Execute(`{"objective":""}`)
	if err == nil {
		t.Error("expected error for empty objective")
	}
}

func TestCreateGoalTool_Replace(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	tool := &CreateGoalTool{Mode: mode}

	if _, err := tool.Execute(`{"objective":"first"}`); err != nil {
		t.Fatal(err)
	}
	out, err := tool.Execute(`{"objective":"second","replace":true}`)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Goal map[string]any `json:"goal"`
	}
	json.Unmarshal([]byte(out), &result)
	if result.Goal["objective"] != "second" {
		t.Errorf("objective = %v", result.Goal["objective"])
	}
}

func TestCreateGoalTool_InvalidJSON(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	tool := &CreateGoalTool{Mode: mode}
	_, err := tool.Execute(`not-json`)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestCreateGoalTool_Description(t *testing.T) {
	if CreateGoalDescription() == "" {
		t.Error("description empty")
	}
}

func TestCreateGoalTool_Schema(t *testing.T) {
	tool := &CreateGoalTool{}
	schema := tool.Schema()
	if schema.Name != "CreateGoal" {
		t.Errorf("name = %q", schema.Name)
	}
}

func TestCreateGoalTool_IsRetryable(t *testing.T) {
	tool := &CreateGoalTool{}
	if tool.IsRetryable(nil) {
		t.Error("expected non-retryable")
	}
}
