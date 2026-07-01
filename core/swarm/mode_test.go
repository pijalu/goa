// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package swarm

import "testing"

func TestStateLifecycle(t *testing.T) {
	s := NewState()
	if s.IsActive() {
		t.Error("new state should be inactive")
	}
	s.Enter(TaskTrigger, "fix lints")
	if !s.IsActive() {
		t.Error("expected active")
	}
	if s.Task() != "fix lints" {
		t.Errorf("task = %q, want fix lints", s.Task())
	}
	if s.Trigger() != TaskTrigger {
		t.Errorf("trigger = %v, want TaskTrigger", s.Trigger())
	}
	s.Exit()
	if s.IsActive() {
		t.Error("expected inactive after exit")
	}
}

func TestEnterIsIdempotent(t *testing.T) {
	s := NewState()
	s.Enter(ManualTrigger, "first")
	s.Enter(TaskTrigger, "second")
	if s.Trigger() != ManualTrigger {
		t.Errorf("first trigger should win, got %v", s.Trigger())
	}
	if s.Task() != "first" {
		t.Errorf("task = %q, want first", s.Task())
	}
}

func TestEnterNoTriggerIsNoop(t *testing.T) {
	s := NewState()
	s.Enter(NoTrigger, "x")
	if s.IsActive() {
		t.Error("NoTrigger must not activate")
	}
}

func TestShouldAutoExit(t *testing.T) {
	cases := []struct {
		trigger Trigger
		want    bool
	}{
		{ManualTrigger, false},
		{TaskTrigger, true},
		{ToolTrigger, true},
		{NoTrigger, false},
	}
	for _, c := range cases {
		s := NewState()
		if c.trigger != NoTrigger {
			s.Enter(c.trigger, "t")
		}
		if got := s.ShouldAutoExit(); got != c.want {
			t.Errorf("trigger=%v: ShouldAutoExit = %v, want %v", c.trigger, got, c.want)
		}
	}
}

func TestMaybeAutoExit(t *testing.T) {
	// Manual trigger stays on across turns.
	manual := NewState()
	manual.Enter(ManualTrigger, "m")
	if manual.MaybeAutoExit() {
		t.Error("manual trigger must not auto-exit")
	}
	if !manual.IsActive() {
		t.Error("manual trigger should still be active")
	}

	// Task trigger auto-exits at end of turn.
	task := NewState()
	task.Enter(TaskTrigger, "t")
	if !task.MaybeAutoExit() {
		t.Error("task trigger should auto-exit")
	}
	if task.IsActive() {
		t.Error("task should be inactive after auto-exit")
	}

	// Tool trigger auto-exits at end of turn.
	tool := NewState()
	tool.Enter(ToolTrigger, "tool")
	if !tool.MaybeAutoExit() {
		t.Error("tool trigger should auto-exit")
	}
}

func TestRemindersNonEmpty(t *testing.T) {
	if EnterReminder() == "" {
		t.Error("EnterReminder must not be empty")
	}
	if ExitReminder() == "" {
		t.Error("ExitReminder must not be empty")
	}
}
