// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"encoding/json"
	"testing"

	"github.com/pijalu/goa/core/goal"
)

func TestGetGoalTool_Empty(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	tool := &GetGoalTool{Mode: mode}
	out, err := tool.Execute(`{}`)
	if err != nil {
		t.Fatal(err)
	}
	var result goal.GoalToolResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.Goal != nil {
		t.Error("expected nil goal")
	}
}

func TestGetGoalTool_Active(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	create := &CreateGoalTool{Mode: mode}
	if _, err := create.Execute(`{"objective":"fix"}`); err != nil {
		t.Fatal(err)
	}

	tool := &GetGoalTool{Mode: mode}
	out, err := tool.Execute(`{}`)
	if err != nil {
		t.Fatal(err)
	}
	var result goal.GoalToolResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.Goal == nil {
		t.Fatal("expected goal")
	}
	if result.Goal.GoalID != "" {
		t.Error("goalId should be stripped")
	}
}

func TestGetGoalTool_IsRetryable(t *testing.T) {
	tool := &GetGoalTool{}
	if tool.IsRetryable(nil) {
		t.Error("expected non-retryable")
	}
}

func TestGetGoalTool_Description(t *testing.T) {
	if GetGoalDescription() == "" {
		t.Error("description empty")
	}
}

func TestGetGoalTool_Schema(t *testing.T) {
	tool := &GetGoalTool{}
	schema := tool.Schema()
	if schema.Name != "GetGoal" {
		t.Errorf("name = %q", schema.Name)
	}
}
