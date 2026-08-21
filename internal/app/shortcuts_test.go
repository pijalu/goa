// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/multiagent"
	"github.com/pijalu/goa/prompts"
	"github.com/pijalu/goa/tui/agentctx"
)

func TestSortedMajors(t *testing.T) {
	reg := core.NewModeRegistry(prompts.NewRegistry(prompts.EmbeddedFS()))
	majors := sortedMajors(reg)
	if len(majors) == 0 {
		t.Fatal("expected majors")
	}
	for i := 1; i < len(majors); i++ {
		if majors[i] < majors[i-1] {
			t.Errorf("majors not sorted: %v", majors)
			break
		}
	}
}

func TestNextInCycle(t *testing.T) {
	values := []string{"a", "b", "c"}
	if got := nextInCycle("a", values); got != "b" {
		t.Errorf("nextInCycle(a) = %q, want b", got)
	}
	if got := nextInCycle("c", values); got != "a" {
		t.Errorf("nextInCycle(c) = %q, want a", got)
	}
	if got := nextInCycle("z", values); got != "a" {
		t.Errorf("nextInCycle(z) = %q, want a", got)
	}
}

func TestHandleChangeMode_CyclesMajor(t *testing.T) {
	cfg := &config.Config{}
	ss := core.NewSessionState(internal.ModeState{Major: internal.MajorCoder, Autonomy: internal.AutonomySolo})
	am := core.NewAgentManager(cfg, nil, nil, ss, nil, "")
	reg := core.NewModeRegistry(prompts.NewRegistry(prompts.EmbeddedFS()))
	am.SetModeRegistry(reg)

	app := &App{subs: &subsystems{agentMgr: am, modeRegistry: reg}}
	app.handleChangeMode()

	if am.CurrentMode().Major == internal.MajorCoder {
		t.Error("expected major mode to cycle away from coder")
	}
}

// TestHandleCycleAgentTab_CyclesAndUpdatesPrompt is the T5 hotkey test:
// cycling the multi-agent tab strip moves the active view AND updates the
// input prompt label (steer <label> on a running delegation tab, "steer all"
// on main while delegations run). Alt+]/Alt+[ and Tab/Shift+Tab all funnel
// into handleCycleAgentTab; Alt+<digit> funnels into handleJumpAgentTab.
func TestHandleCycleAgentTab_CyclesAndUpdatesPrompt(t *testing.T) {
	sc := newUIScenario(t, 100, 24)
	sc.app.subs.inputEditor = sc.editor
	reg := sc.app.subs.agentRegistry

	// Two delegations: coder running, planner not started.
	sc.engine.ApplySync(func() {
		sc.app.ensureDelegationView("dlg-coder-01", "coder")
		sc.app.setDelegationStatus("dlg-coder-01", multiagent.DelegationRunning)
		sc.app.ensureDelegationView("dlg-planner-01", "planner")
	})
	if id, _ := reg.Active(); id != agentctx.MainAgentID {
		t.Fatalf("active = %q, want main", id)
	}

	// Main tab with a live delegation: "steer all".
	if got := sc.editor.Title(); got != "steer all" {
		t.Errorf("main title = %q, want %q", got, "steer all")
	}

	// Cycle forward → coder·dlg-01 (running): prompt names it.
	sc.app.handleCycleAgentTab(1)
	if id, _ := reg.Active(); id != "dlg-coder-01" {
		t.Fatalf("after next: active = %q, want dlg-coder-01", id)
	}
	if got, want := sc.editor.Title(), "steer coder·dlg-01"; got != want {
		t.Errorf("coder title = %q, want %q", got, want)
	}

	// Cycle forward again → planner·dlg-01 (not running): no steer title.
	sc.app.handleCycleAgentTab(1)
	if id, _ := reg.Active(); id != "dlg-planner-01" {
		t.Fatalf("after next: active = %q, want dlg-planner-01", id)
	}
	if got := sc.editor.Title(); got != "" {
		t.Errorf("planner title = %q, want empty", got)
	}

	// Cycle backward returns to the coder tab.
	sc.app.handleCycleAgentTab(-1)
	if id, _ := reg.Active(); id != "dlg-coder-01" {
		t.Fatalf("after prev: active = %q, want dlg-coder-01", id)
	}
	if got, want := sc.editor.Title(), "steer coder·dlg-01"; got != want {
		t.Errorf("coder title after prev = %q, want %q", got, want)
	}

	// Digit jump to index 0 (main) — prompt falls back to "steer all".
	sc.app.handleJumpAgentTab(0)
	if id, _ := reg.Active(); id != agentctx.MainAgentID {
		t.Fatalf("after jump: active = %q, want main", id)
	}
	if got := sc.editor.Title(); got != "steer all" {
		t.Errorf("main title after jump = %q, want %q", got, "steer all")
	}

	// Out-of-range digit jumps are ignored.
	sc.app.handleJumpAgentTab(9)
	if id, _ := reg.Active(); id != agentctx.MainAgentID {
		t.Errorf("out-of-range jump moved active to %q", id)
	}
}
