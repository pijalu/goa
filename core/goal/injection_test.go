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
