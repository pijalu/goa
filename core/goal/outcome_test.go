// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"strings"
	"testing"
)

func TestBuildCompletionSummary(t *testing.T) {
	snap := GoalSnapshot{
		Objective:      "finish feature",
		Status:         GoalDone,
		TurnsUsed:      1,
		TokensUsed:     100,
		WallClockMs:    5000,
		TerminalReason: strPtr("tests pass"),
	}
	s := BuildCompletionSummary(snap)
	if !strings.Contains(s, "Goal completed successfully") {
		t.Errorf("missing completion header: %s", s)
	}
	if !strings.Contains(s, "tests pass") {
		t.Errorf("missing reason: %s", s)
	}
	if !strings.Contains(s, "5s") {
		t.Errorf("missing elapsed: %s", s)
	}
}

func TestBuildBlockedReasonPrompt(t *testing.T) {
	snap := GoalSnapshot{
		Objective:   "deploy",
		Status:      GoalBlocked,
		TurnsUsed:   2,
		TokensUsed:  200,
		WallClockMs: 10000,
	}
	s := BuildBlockedReasonPrompt(snap)
	if !strings.Contains(s, "Goal blocked") {
		t.Errorf("missing blocked header: %s", s)
	}
	if !strings.Contains(s, "Do not call more goal tools") {
		t.Errorf("missing instruction: %s", s)
	}
}

func TestPluralize(t *testing.T) {
	if got := Pluralize(1, "turn", "turns"); got != "1 turn" {
		t.Errorf("Pluralize(1) = %q", got)
	}
	if got := Pluralize(3, "turn", "turns"); got != "3 turns" {
		t.Errorf("Pluralize(3) = %q", got)
	}
}

func TestBuildCancellationReminder(t *testing.T) {
	if got := BuildCancellationReminder(); got == "" {
		t.Error("cancellation reminder empty")
	}
}

func TestBuildForkClearedReminder(t *testing.T) {
	if got := BuildForkClearedReminder(); got == "" {
		t.Error("fork cleared reminder empty")
	}
}
