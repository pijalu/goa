// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"testing"

	"github.com/pijalu/goa/internal"
)

func TestModeManager_SetMode(t *testing.T) {
	mm := NewModeManager(NewSessionState(internal.ModeState{Major: internal.MajorCoder}), NewAgentDrivenGate())
	info := mm.SetMode(internal.ModeState{Major: internal.MajorPlanner})
	if info == nil {
		t.Fatal("expected mode change info")
	}
	if info.OldMode.Major != internal.MajorCoder {
		t.Errorf("old mode = %q, want coder", info.OldMode.Major)
	}
	if info.NewMode.Major != internal.MajorPlanner {
		t.Errorf("new mode = %q, want planner", info.NewMode.Major)
	}
	if mm.CurrentMode().Major != internal.MajorPlanner {
		t.Errorf("current = %q, want planner", mm.CurrentMode().Major)
	}
}

func TestModeManager_PushPopMode(t *testing.T) {
	mm := NewModeManager(NewSessionState(internal.ModeState{Major: internal.MajorCoder}), NewAgentDrivenGate())
	info := mm.PushMode(internal.ModeState{Major: internal.MajorPlanner}, "skill: planner")
	if info == nil {
		t.Fatal("expected push info")
	}
	if info.Source != "skill: planner" {
		t.Errorf("source = %q, want skill: planner", info.Source)
	}
	if mm.PreviousMode() == nil || mm.PreviousMode().Major != internal.MajorCoder {
		t.Error("previous mode should be coder")
	}
	if mm.Source() != "skill: planner" {
		t.Errorf("current source = %q, want skill: planner", mm.Source())
	}

	popped := mm.PopMode()
	if popped == nil || popped.NewMode.Major != internal.MajorCoder {
		t.Error("pop should restore coder")
	}
	if mm.CurrentMode().Major != internal.MajorCoder {
		t.Errorf("current after pop = %q, want coder", mm.CurrentMode().Major)
	}
}

func TestModeManager_ThinkingLevel(t *testing.T) {
	mm := NewModeManager(NewSessionState(internal.ModeState{}), NewAgentDrivenGate())
	mm.SetThinkingLevel("high")
	if mm.GetThinkingLevel() != "high" {
		t.Errorf("thinking level = %q, want high", mm.GetThinkingLevel())
	}
}

func TestModeManager_AgentDrivenDisabledByDefault(t *testing.T) {
	mm := NewModeManager(NewSessionState(internal.ModeState{}), NewAgentDrivenGate())
	if mm.AgentDrivenEnabled() {
		t.Error("agent-driven should be disabled by default")
	}
}

func TestModeManager_PushMode_SetsSource(t *testing.T) {
	mm := NewModeManager(NewSessionState(internal.ModeState{Major: internal.MajorCoder}), NewAgentDrivenGate())
	info := mm.PushMode(internal.ModeState{Major: internal.MajorPlanner}, "workflow: review")
	if info == nil {
		t.Fatal("expected push info")
	}
	if info.Source != "workflow: review" {
		t.Errorf("source = %q, want workflow: review", info.Source)
	}
	if mm.CurrentMode().Major != internal.MajorPlanner {
		t.Errorf("current = %q, want planner", mm.CurrentMode().Major)
	}
}

func TestModeManager_RestoreFromSnapshot(t *testing.T) {
	mm := NewModeManager(NewSessionState(internal.ModeState{}), NewAgentDrivenGate())
	mm.RestoreFromSnapshot(SessionStateSnapshot{
		ModeState:          internal.ModeState{Major: internal.MajorReviewer},
		MinorMode:          "companion",
		AgentDrivenEnabled: true,
		ThinkingLevel:      "low",
	})

	if mm.CurrentMode().Major != internal.MajorReviewer {
		t.Errorf("restored mode = %q, want reviewer", mm.CurrentMode().Major)
	}
	if mm.CurrentMinorMode() != "companion" {
		t.Errorf("minor = %q, want companion", mm.CurrentMinorMode())
	}
	if !mm.AgentDrivenEnabled() {
		t.Error("agent-driven should be restored to true")
	}
	if mm.GetThinkingLevel() != "low" {
		t.Errorf("thinking level = %q, want low", mm.GetThinkingLevel())
	}
}
