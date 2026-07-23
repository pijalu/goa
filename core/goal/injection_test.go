// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"strings"
	"testing"
)

func TestBuildActiveGoalReminder(t *testing.T) {
	snap := GoalSnapshot{
		Objective:           "fix tests",
		CompletionCriterion: strPtr("all tests pass"),
		Status:              GoalActive,
		TurnsUsed:           3,
		TokensUsed:          1234,
		WallClockMs:         65000,
		Budget: GoalBudgetReport{
			TurnBudget:      intPtr(10),
			RemainingTurns:  intPtr(7),
			TokenBudget:     intPtr(5000),
			RemainingTokens: intPtr(3766),
		},
	}
	reminder := BuildActiveGoalReminder(snap)
	for _, want := range []string{
		"fix tests",
		"all tests pass",
		"Status: active",
		"3 continuation turns",
		"1.2k tokens",
		"1m05 elapsed",
		"turns 3/10",
		"tokens 1.2k/5.0k",
	} {
		if !strings.Contains(reminder, want) {
			t.Errorf("reminder missing %q", want)
		}
	}
}

func TestBuildStaticGoalReminder_HowToEndGuidance(t *testing.T) {
	snap := GoalSnapshot{Objective: "fix tests", Status: GoalActive}
	reminder := BuildStaticGoalReminder(snap)
	// The reminder must make unmistakably clear that only an UpdateGoal tool
	// call ends the goal — prose ("the goal is complete"), a bash echo, or a
	// send_message do not. Regression: models announced completion in text and
	// never called UpdateGoal, so the driver kept burning continuation turns.
	for _, want := range []string{
		"HOW TO END A GOAL",
		"UpdateGoal TOOL CALL",
		"does NOT end it",
		"send_message",
	} {
		if !strings.Contains(reminder, want) {
			t.Errorf("reminder missing how-to-end guidance %q", want)
		}
	}
}

func TestStaticGoalReminder_StableAcrossTurns(t *testing.T) {
	base := GoalSnapshot{
		Objective:           "fix tests",
		CompletionCriterion: strPtr("all tests pass"),
		Status:              GoalActive,
	}
	a := BuildStaticGoalReminder(base)
	b := BuildStaticGoalReminder(GoalSnapshot{
		Objective:           "fix tests",
		CompletionCriterion: strPtr("all tests pass"),
		Status:              GoalActive,
		TurnsUsed:           5,
		TokensUsed:          9999,
		WallClockMs:         120000,
	})
	c := BuildStaticGoalReminder(GoalSnapshot{
		Objective:           "fix tests",
		CompletionCriterion: strPtr("all tests pass"),
		Status:              GoalActive,
		TurnsUsed:           10,
		TokensUsed:          50000,
		WallClockMs:         300000,
	})
	if a != b || b != c {
		t.Error("static reminder should be byte-identical across turns for the same goal")
	}
}

func TestDynamicGoalProgress_Changes(t *testing.T) {
	base := GoalSnapshot{
		Objective:  "fix tests",
		Status:     GoalActive,
		TurnsUsed:  1,
		TokensUsed: 100,
		WallClockMs: 1000,
	}
	a := BuildDynamicGoalProgress(base)
	b := BuildDynamicGoalProgress(GoalSnapshot{
		Objective:   "fix tests",
		Status:        GoalActive,
		TurnsUsed:     2,
		TokensUsed:    200,
		WallClockMs:   2000,
	})
	if a == b {
		t.Error("dynamic progress should differ across turns")
	}
	if !strings.Contains(a, "1 continuation turns") || !strings.Contains(b, "2 continuation turns") {
		t.Error("dynamic progress should reflect turn counts")
	}
}

func TestBuildDynamicGoalProgress_StatusAndBudgets(t *testing.T) {
	snap := GoalSnapshot{
		Objective:   "fix tests",
		Status:      GoalActive,
		TurnsUsed:   3,
		TokensUsed:  1234,
		WallClockMs: 65000,
		Budget: GoalBudgetReport{
			TurnBudget:      intPtr(10),
			RemainingTurns:  intPtr(7),
			TokenBudget:     intPtr(5000),
			RemainingTokens: intPtr(3766),
		},
	}
	progress := BuildDynamicGoalProgress(snap)
	for _, want := range []string{
		"Status: active",
		"3 continuation turns",
		"1.2k tokens",
		"turns 3/10",
		"tokens 1.2k/5.0k",
	} {
		if !strings.Contains(progress, want) {
			t.Errorf("dynamic progress missing %q", want)
		}
	}
}

func TestBuildStaticGoalReminder_NoDynamicContent(t *testing.T) {
	snap := GoalSnapshot{
		Objective:   "fix tests",
		Status:      GoalActive,
		TurnsUsed:   3,
		TokensUsed:  1234,
		WallClockMs: 65000,
	}
	static := BuildStaticGoalReminder(snap)
	if strings.Contains(static, "Status: active") {
		t.Error("static reminder should not contain dynamic status")
	}
	if strings.Contains(static, "continuation turns") {
		t.Error("static reminder should not contain dynamic progress")
	}
}

func TestBuildPausedNote(t *testing.T) {
	snap := GoalSnapshot{
		Objective:      "refactor",
		Status:         GoalPaused,
		TerminalReason: strPtr("user paused"),
	}
	s := BuildPausedNote(snap)
	if !strings.Contains(s, "currently paused") || !strings.Contains(s, "user paused") {
		t.Errorf("unexpected paused note: %s", s)
	}
}

func TestBuildBlockedNote(t *testing.T) {
	snap := GoalSnapshot{
		Objective:      "deploy",
		Status:         GoalBlocked,
		TerminalReason: strPtr("missing token"),
	}
	s := BuildBlockedNote(snap)
	if !strings.Contains(s, "currently blocked") || !strings.Contains(s, "missing token") {
		t.Errorf("unexpected blocked note: %s", s)
	}
}

func TestFormatElapsed(t *testing.T) {
	cases := []struct {
		ms   int64
		want string
	}{
		{500, "1s"},
		{65000, "1m05"},
		{3661000, "1h01m"},
		{0, "0s"},
	}
	for _, tc := range cases {
		if got := FormatElapsed(tc.ms); got != tc.want {
			t.Errorf("FormatElapsed(%d) = %q, want %q", tc.ms, got, tc.want)
		}
	}
}

func TestFormatTokens(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{999, "999"},
		{1500, "1.5k"},
		{2_500_000, "2.5M"},
		{0, "0"},
	}
	for _, tc := range cases {
		if got := FormatTokens(tc.n); got != tc.want {
			t.Errorf("FormatTokens(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

func TestZeroPad(t *testing.T) {
	if got := zeroPad(5); got != "05" {
		t.Errorf("zeroPad(5) = %q", got)
	}
	if got := zeroPad(15); got != "15" {
		t.Errorf("zeroPad(15) = %q", got)
	}
}

func TestFormatInt(t *testing.T) {
	cases := map[int]string{
		0:  "0",
		5:  "5",
		-5: "-5",
	}
	for in, want := range cases {
		if got := FormatInt(in); got != want {
			t.Errorf("FormatInt(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatFloat(t *testing.T) {
	cases := map[float64]string{
		1.5: "1.5",
		0.5: "0.5",
	}
	for in, want := range cases {
		if got := FormatFloat(in); got != want {
			t.Errorf("FormatFloat(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestMaxBudgetFraction_Time(t *testing.T) {
	var ms int64 = 10000
	snap := GoalSnapshot{
		WallClockMs: 5000,
		Budget:      GoalBudgetReport{WallClockBudgetMs: &ms},
	}
	if got := MaxBudgetFraction(snap); got != 0.5 {
		t.Errorf("MaxBudgetFraction = %v", got)
	}
}

func TestEscapeUntrustedText(t *testing.T) {
	got := EscapeUntrustedText("a < b & c > d")
	want := "a &lt; b &amp; c &gt; d"
	if got != want {
		t.Errorf("EscapeUntrustedText = %q, want %q", got, want)
	}
}

func TestFormatBudgetLines(t *testing.T) {
	snap := GoalSnapshot{
		TurnsUsed:   2,
		TokensUsed:  1000,
		WallClockMs: 5000,
		Budget: GoalBudgetReport{
			TurnBudget:           intPtr(5),
			RemainingTurns:       intPtr(3),
			TokenBudget:          intPtr(2000),
			RemainingTokens:      intPtr(1000),
			WallClockBudgetMs:    int64Ptr(10000),
			RemainingWallClockMs: int64Ptr(5000),
		},
	}
	lines := formatBudgetLines(snap)
	if len(lines) != 3 {
		t.Fatalf("lines = %d", len(lines))
	}
	for _, want := range []string{"turns 2/5", "tokens 1.0k/2.0k", "time 5s/10s"} {
		found := false
		for _, line := range lines {
			if strings.Contains(line, want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing budget line containing %q", want)
		}
	}
}

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }
func int64Ptr(i int64) *int64 { return &i }