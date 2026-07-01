// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plan

import (
	"testing"

	"github.com/pijalu/goa/core/plan"
)

func TestPlanModeToolToggle(t *testing.T) {
	state := plan.NewState()
	tool := &PlanModeTool{State: state}

	out, err := tool.Execute(`{"action":"enter"}`)
	if err != nil {
		t.Fatalf("enter: %v", err)
	}
	if !state.IsActive() {
		t.Error("expected plan mode active")
	}
	if out == "" {
		t.Error("expected output")
	}

	exitOut, err := tool.Execute(`{"action":"exit"}`)
	if err != nil {
		t.Fatalf("exit: %v", err)
	}
	if exitOut == "" {
		t.Error("expected exit output")
	}
	if state.IsActive() {
		t.Error("expected plan mode inactive")
	}
}

func TestPlanModeToolInvalid(t *testing.T) {
	tool := &PlanModeTool{State: plan.NewState()}
	_, err := tool.Execute(`{"action":"dance"}`)
	if err == nil {
		t.Fatal("expected error")
	}
}
