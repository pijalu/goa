// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/internal/ansi"
)

func TestMarker_Paused(t *testing.T) {
	status := goal.GoalPaused
	actor := goal.GoalActorUser
	reason := "break"
	m := NewMarker(&goal.GoalChange{
		Kind:   goal.GoalChangeLifecycle,
		Status: &status,
		Actor:  &actor,
		Reason: &reason,
	})

	lines := m.Render(80)
	if len(lines) == 0 {
		t.Fatal("expected marker line")
	}
	if !strings.Contains(lines[0], "paused") {
		t.Errorf("marker should contain paused: %s", lines[0])
	}
	// Reason renders on a detail line below the headline; check all lines.
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "break") {
		t.Errorf("marker should show reason somewhere in: %q", joined)
	}
}

func TestMarker_AllStatuses(t *testing.T) {
	for _, status := range []goal.GoalStatus{goal.GoalActive, goal.GoalPaused, goal.GoalBlocked, goal.GoalDone} {
		actor := goal.GoalActorModel
		m := NewMarker(&goal.GoalChange{Status: &status, Actor: &actor})
		lines := m.Render(80)
		if len(lines) == 0 {
			t.Errorf("%s: no lines", status)
		}
	}
}

func TestMarker_NoStatus(t *testing.T) {
	m := NewMarker(&goal.GoalChange{})
	lines := m.Render(80)
	if len(lines) == 0 {
		t.Fatal("expected lines")
	}
}

func TestMarker_SystemActor(t *testing.T) {
	status := goal.GoalBlocked
	actor := goal.GoalActorRuntime
	m := NewMarker(&goal.GoalChange{Status: &status, Actor: &actor})
	lines := m.Render(80)
	if !strings.Contains(lines[0], "system") {
		t.Errorf("line = %q", lines[0])
	}
}

func TestMarker_HandleInput(t *testing.T) {
	m := NewMarker(nil)
	m.HandleInput("x")
	m.Invalidate()
}

// TestMarker_BlockedShowsFullReasonAndExpectation is the regression for
// "goal block does not show any details": a long reason and expectation must
// be rendered in full (wrapped across lines), never truncated to fit one
// headline line.
func TestMarker_BlockedShowsFullReasonAndExpectation(t *testing.T) {
	status := goal.GoalBlocked
	actor := goal.GoalActorModel
	reason := "P1B parser/engine fixes complete (Steps 2-4 done, WINDOW clause fixed, CTE+VALUES supported); remaining 70 failures require P1-level ALTER TABLE work — column-level constraint name preservation, circular view detection, trigger validation during RENAME"
	expectation := "user decision: implement ALTER TABLE constraint preservation or descope P1 to parser-only"
	m := NewMarker(&goal.GoalChange{
		Kind:        goal.GoalChangeLifecycle,
		Status:      &status,
		Actor:       &actor,
		Reason:      &reason,
		Expectation: &expectation,
	})

	lines := m.Render(80)
	if len(lines) < 3 {
		t.Fatalf("expected headline + reason + expectation lines, got %d: %v", len(lines), lines)
	}
	// Strip ANSI, join, and collapse whitespace: wrapping inserts line breaks
	// and indent inside the text, so we compare word sequences, not raw bytes.
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(ansi.Strip(l))
		b.WriteByte(' ')
	}
	full := collapseWS(b.String())
	if !strings.Contains(full, collapseWS(reason)) {
		t.Errorf("reason words lost in wrapping.\n want: %q\n got: %q", reason, full)
	}
	if !strings.Contains(full, collapseWS(expectation)) {
		t.Errorf("expectation words lost in wrapping.\n want: %q\n got: %q", expectation, full)
	}
}

// collapseWS reduces every run of whitespace to a single space so a wrapped
// string can be compared to its unwrapped source by word sequence.
func collapseWS(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// TestMarker_NoReasonStaysSingleLine ensures a marker with no reason or
// expectation still renders as a compact single line.
func TestMarker_NoReasonStaysSingleLine(t *testing.T) {
	status := goal.GoalActive
	actor := goal.GoalActorModel
	m := NewMarker(&goal.GoalChange{Status: &status, Actor: &actor})
	if got := m.Render(80); len(got) != 1 {
		t.Errorf("expected 1 line for reason-less marker, got %d: %v", len(got), got)
	}
}

// TestMarker_MultiLineReason pins the bugs.md "Corruption on goal change"
// fix: model-supplied reasons routinely contain newlines; every rendered row
// must be a single visual line (no embedded "\n").
func TestMarker_MultiLineReason(t *testing.T) {
	status := goal.GoalBlocked
	actor := goal.GoalActorModel
	reason := "Core fixes are solid: altercons3 passes.\n\nRemaining work:\n1. analyze6: Add expect field\n2. analyzeC: Fix EXPLAIN output"
	m := NewMarker(&goal.GoalChange{
		Kind:   goal.GoalChangeLifecycle,
		Status: &status,
		Actor:  &actor,
		Reason: &reason,
	})
	lines := m.Render(80)
	for i, l := range lines {
		if strings.Contains(l, "\n") {
			t.Errorf("row %d contains an embedded newline: %q", i, l)
		}
		if w := ansi.Width(l); w > 80 {
			t.Errorf("row %d exceeds width: %d > 80 (%q)", i, w, l)
		}
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"Core fixes are solid", "Remaining work", "analyze6", "analyzeC"} {
		if !strings.Contains(joined, want) {
			t.Errorf("render missing %q", want)
		}
	}
}