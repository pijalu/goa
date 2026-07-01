// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"errors"
	"testing"

	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/internal"
)

func TestSetGoalBudgetTool_Turns(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	create := &CreateGoalTool{Mode: mode}
	create.Execute(`{"objective":"fix"}`)

	tool := &SetGoalBudgetTool{Mode: mode}
	out, err := tool.Execute(`{"value":5,"unit":"turns"}`)
	if err != nil {
		t.Fatal(err)
	}
	if out != "Goal budget set: 5 turns." {
		t.Errorf("output = %q", out)
	}
	if mode.GetGoal().Goal.Budget.TurnBudget == nil || *mode.GetGoal().Goal.Budget.TurnBudget != 5 {
		t.Error("turn budget not set")
	}
}

func TestSetGoalBudgetTool_Tokens(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	create := &CreateGoalTool{Mode: mode}
	create.Execute(`{"objective":"fix"}`)

	tool := &SetGoalBudgetTool{Mode: mode}
	out, err := tool.Execute(`{"value":1000,"unit":"tokens"}`)
	if err != nil {
		t.Fatal(err)
	}
	if out != "Goal budget set: 1000 tokens." {
		t.Errorf("output = %q", out)
	}
}

func TestSetGoalBudgetTool_TimeSeconds(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	create := &CreateGoalTool{Mode: mode}
	create.Execute(`{"objective":"fix"}`)

	tool := &SetGoalBudgetTool{Mode: mode}
	out, err := tool.Execute(`{"value":30,"unit":"seconds"}`)
	if err != nil {
		t.Fatal(err)
	}
	if out != "Goal budget set: 30 seconds." {
		t.Errorf("output = %q", out)
	}
}

func TestSetGoalBudgetTool_Unreasonable(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	create := &CreateGoalTool{Mode: mode}
	create.Execute(`{"objective":"fix"}`)

	tool := &SetGoalBudgetTool{Mode: mode}
	out, err := tool.Execute(`{"value":0.5,"unit":"milliseconds"}`)
	if err != nil {
		t.Fatal(err)
	}
	if out == "" {
		t.Error("expected message for unreasonable budget")
	}
	if mode.GetGoal().Goal.Budget.WallClockBudgetMs != nil {
		t.Error("budget should not be set")
	}
}

func TestSetGoalBudgetTool_InvalidJSON(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	tool := &SetGoalBudgetTool{Mode: mode}
	_, err := tool.Execute(`not-json`)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestSetGoalBudgetTool_Minutes(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	create := &CreateGoalTool{Mode: mode}
	create.Execute(`{"objective":"fix"}`)

	tool := &SetGoalBudgetTool{Mode: mode}
	out, err := tool.Execute(`{"value":2,"unit":"minutes"}`)
	if err != nil {
		t.Fatal(err)
	}
	if out != "Goal budget set: 2 minutes." {
		t.Errorf("output = %q", out)
	}
}

func TestSetGoalBudgetTool_Hours(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	create := &CreateGoalTool{Mode: mode}
	create.Execute(`{"objective":"fix"}`)

	tool := &SetGoalBudgetTool{Mode: mode}
	out, err := tool.Execute(`{"value":1,"unit":"hours"}`)
	if err != nil {
		t.Fatal(err)
	}
	if out != "Goal budget set: 1 hour." {
		t.Errorf("output = %q", out)
	}
}

func TestSetGoalBudgetTool_Milliseconds(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	create := &CreateGoalTool{Mode: mode}
	create.Execute(`{"objective":"fix"}`)

	tool := &SetGoalBudgetTool{Mode: mode}
	out, err := tool.Execute(`{"value":5000,"unit":"milliseconds"}`)
	if err != nil {
		t.Fatal(err)
	}
	if out != "Goal budget set: 5000 milliseconds." {
		t.Errorf("output = %q", out)
	}
}

func TestSetGoalBudgetTool_Description(t *testing.T) {
	if SetGoalBudgetDescription() == "" {
		t.Error("description empty")
	}
}

func TestSetGoalBudgetTool_Schema(t *testing.T) {
	tool := &SetGoalBudgetTool{}
	schema := tool.Schema()
	if schema.Name != "SetGoalBudget" {
		t.Errorf("name = %q", schema.Name)
	}
}

func TestSetGoalBudgetTool_NoGoal(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	tool := &SetGoalBudgetTool{Mode: mode}
	// core/goal now returns an error (no panic) when no goal is active; AP-04
	// requires every Execute error to be a *internal.ToolError.
	_, err := tool.Execute(`{"value":5,"unit":"turns"}`)
	if err == nil {
		t.Fatal("expected error when no goal exists")
	}
	var te *internal.ToolError
	if !errors.As(err, &te) {
		t.Errorf("expected *internal.ToolError, got %T: %v", err, err)
	}
}

func TestSetGoalBudgetTool_IsRetryable(t *testing.T) {
	tool := &SetGoalBudgetTool{}
	if tool.IsRetryable(nil) {
		t.Error("expected non-retryable")
	}
}
