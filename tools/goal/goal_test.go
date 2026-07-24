// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/pijalu/goa/core/goal"
)

// newGoalTool builds a GoalTool with a controllable create gate.
func newGoalTool(mode *goal.GoalMode, createAllowed func() bool) *GoalTool {
	return &GoalTool{Mode: mode, CreateAllowed: createAllowed}
}

func TestGoalTool_SchemaShape(t *testing.T) {
	tool := newGoalTool(goal.NewGoalMode(nil, nil, nil, nil), nil)
	s := tool.Schema()
	if s.Name != "goal" {
		t.Errorf("schema name = %q, want goal", s.Name)
	}
	if s.Description == "" {
		t.Error("schema description is empty")
	}
	props, _ := s.Schema["properties"].(map[string]any)
	if props == nil || props["action"] == nil {
		t.Error("schema missing action property")
	}
}

func TestGoalTool_CreateGatedOff_NoActiveGoal(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	tool := newGoalTool(mode, func() bool { return false })
	_, err := tool.Execute(`{"action":"create","objective":"do x"}`)
	if err == nil {
		t.Fatal("expected a gate error when create is disabled and no goal is active")
	}
	// State must be unchanged: still no goal.
	if mode.GetActiveGoal() != nil {
		t.Error("gate-off create must not create a goal")
	}
}

func TestGoalTool_CreateGatedOff_ActiveGoalAllows(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	// Seed an active goal (e.g. user started one via /goal).
	if _, err := mode.CreateGoal(goal.CreateGoalInput{Objective: "first"}, goal.GoalActorUser); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tool := newGoalTool(mode, func() bool { return mode.GetActiveGoal() != nil })
	out, err := tool.Execute(`{"action":"create","objective":"second","replace":true}`)
	if err != nil {
		t.Fatalf("create during an active goal must be allowed: %v", err)
	}
	if !strings.Contains(out, "second") {
		t.Errorf("output = %q", out)
	}
}

func TestGoalTool_CreateGateOn(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	tool := newGoalTool(mode, func() bool { return true })
	out, err := tool.Execute(`{"action":"create","objective":"build it"}`)
	if err != nil {
		t.Fatalf("create with gate on: %v", err)
	}
	var decoded struct {
		Goal struct {
			Objective string `json:"objective"`
		} `json:"goal"`
	}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("output not JSON: %v (%q)", err, out)
	}
	if decoded.Goal.Objective != "build it" {
		t.Errorf("objective = %q", decoded.Goal.Objective)
	}
}

func TestGoalTool_UpdateCompleteSetsStopTurn(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	if _, err := mode.CreateGoal(goal.CreateGoalInput{Objective: "x"}, goal.GoalActorUser); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tool := newGoalTool(mode, nil)
	res, err := tool.ExecuteWithResult(`{"action":"update","status":"complete"}`)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !res.StopTurn {
		t.Error("update complete must set StopTurn so the turn ends")
	}
	if mode.GetActiveGoal() != nil {
		t.Error("goal should no longer be active after complete")
	}
}

func TestGoalTool_GetReturnsSnapshot(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	tool := newGoalTool(mode, nil)
	out, err := tool.Execute(`{"action":"get"}`)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !strings.Contains(out, `"goal":null`) {
		t.Errorf("get with no goal = %q", out)
	}
}

func TestGoalTool_SetBudget(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	if _, err := mode.CreateGoal(goal.CreateGoalInput{Objective: "x"}, goal.GoalActorUser); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tool := newGoalTool(mode, nil)
	out, err := tool.Execute(`{"action":"set_budget","value":10,"unit":"turns"}`)
	if err != nil {
		t.Fatalf("set_budget: %v", err)
	}
	if !strings.Contains(out, "10 turns") {
		t.Errorf("output = %q", out)
	}
}

func TestGoalTool_ActionFieldMismatch(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	tool := newGoalTool(mode, func() bool { return true })
	if _, err := tool.Execute(`{"action":"create"}`); err == nil {
		t.Error("create without objective must error")
	}
	if _, err := tool.Execute(`{"action":"update"}`); err == nil {
		t.Error("update without status must error")
	}
	if _, err := tool.Execute(`{"action":"set_budget","value":5}`); err == nil {
		t.Error("set_budget without unit must error")
	}
}
