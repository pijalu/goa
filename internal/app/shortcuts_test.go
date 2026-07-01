// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/prompts"
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
