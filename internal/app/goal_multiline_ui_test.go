// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/internal/event"
)

// TestUI_GoalCompletion_MultiLineObjective is the filmstrip validation of
// bugs.md "Goal completion screen corruption": a completion whose objective
// contains embedded newlines must render every row newline-free, within
// width, and left-aligned — the pre-fix renderer let the continuation print
// at the column where the first line ended (the misaligned "The goal ..."
// fragment at column ~80).
func TestUI_GoalCompletion_MultiLineObjective(t *testing.T) {
	sc := newUIScenario(t, 100, 24)

	snap := &goal.GoalSnapshot{
		Status:      goal.GoalDone,
		Objective:   "UNBLOCKING INVESTIGATION — find a solution for a blocked goal.\n\nThe goal \"Implement G05: ALTER TABLE Token-Level Rename\" was blocked because: schema entries lost\nIt was waiting for: user direction",
		TurnsUsed:   1,
		TokensUsed:  24900,
		WallClockMs: 154000,
	}
	sc.engine.ApplySync(func() {
		sc.app.handleGoalUpdate(&event.GoalUpdate{
			Snapshot: snap,
			Change:   &goal.GoalChange{Kind: goal.GoalChangeCompletion},
		})
	})
	sc.engine.RenderNow()
	frame := sc.engine.AgentFrame()
	sc.filmstrip().Capture("completion", frame, "")

	visible := frame.Visible
	for i, row := range visible {
		if strings.Contains(row, "\n") {
			t.Fatalf("visible row %d contains an embedded newline: %q", i, row)
		}
		if w := ansi.Width(row); w > 100 {
			t.Errorf("visible row %d exceeds width: %d > 100", i, w)
		}
	}
	joined := "\n" + strings.Join(visible, "\n") + "\n"
	for _, want := range []string{"✓ Goal complete — UNBLOCKING INVESTIGATION", "The goal \"Implement G05", "was blocked because", "It was waiting for", "Worked 1 turn over 2m34, using 24.9k tokens."} {
		if !strings.Contains(joined, want) {
			t.Errorf("completion row missing %q\n%s", want, frame.Dump())
		}
	}
	// Left alignment: the completion text must start at the left edge (column
	// 0), never as a right-shifted fragment.
	for _, row := range visible {
		if strings.Contains(row, "The goal \"Implement G05") && strings.HasPrefix(strings.TrimLeft(row, " "), row) == false {
			// leading whitespace before the continuation means misalignment
			trimmed := strings.TrimLeft(row, " ")
			if len(row) != len(trimmed) {
				t.Errorf("continuation row is right-shifted: %q", row)
			}
		}
	}
}

// TestUI_GoalBubble_MultiLineObjective validates bugs.md "Corruption on goal
// change": the bubble above the input must render a multi-line objective as
// proper rows (no embedded newlines), capped at 3 body lines between
// separators.
func TestUI_GoalBubble_MultiLineObjective(t *testing.T) {
	sc := newUIScenario(t, 100, 24)

	snap := &goal.GoalSnapshot{
		Status:    goal.GoalActive,
		Name:      "sunny.otter",
		Objective: "UNBLOCKING INVESTIGATION — find a solution for a blocked goal.\n\n1. analyze6: Add expect field to duplicate-step tests\n2. analyzeC: Fix EXPLAIN QUERY PLAN output\n3. analyzeD: something else entirely here",
	}
	sc.engine.ApplySync(func() {
		sc.app.handleGoalUpdate(&event.GoalUpdate{Snapshot: snap})
	})
	sc.engine.RenderNow()
	frame := sc.engine.AgentFrame()
	sc.filmstrip().Capture("bubble", frame, "")

	var bubbleRows []string
	for _, row := range frame.Visible {
		if strings.Contains(row, "\n") {
			t.Fatalf("visible row contains an embedded newline: %q", row)
		}
		if strings.Contains(row, "⟐") || strings.Contains(row, "analyze") || strings.Contains(row, "UNBLOCKING") {
			bubbleRows = append(bubbleRows, row)
		}
	}
	if len(bubbleRows) == 0 {
		t.Fatalf("goal bubble not visible\n%s", frame.Dump())
	}
	// Cap: at most 3 body rows (⟐ + content) between separators.
	body := 0
	for _, row := range frame.Visible {
		if strings.Contains(row, "─") && !strings.Contains(row, "⟐") {
			continue
		}
		if strings.Contains(row, "⟐") || strings.Contains(row, "analyze") || strings.Contains(row, "UNBLOCKING") {
			body++
		}
	}
	if body > 3 {
		t.Errorf("bubble body exceeds the 3-line cap: %d rows\n%s", body, frame.Dump())
	}
	// The footer goal segment must also be newline-free.
	for _, row := range frame.Visible {
		if strings.Contains(row, "⟐") && strings.Contains(row, "\n") {
			t.Errorf("footer goal segment contains newline: %q", row)
		}
	}
}

// TestUI_StreamingWithChromeChanges validates the compositor perf fix
// (bugs.md "Slow performance"): streaming chunks while the steering bubble
// appears and clears must render without panic, keep input/footer pinned at
// the bottom, and never wipe scrollback on chrome-only changes.
func TestUI_StreamingWithChromeChanges(t *testing.T) {
	sc := newUIScenario(t, 80, 20)

	// Fill history so the transcript scrolls.
	for i := 0; i < 25; i++ {
		sc.engine.ApplySync(func() {
			sc.chat.AddSystemMessage("history baseline row for the streaming chrome test")
		})
	}
	sc.engine.RenderNow()

	// Stream several chunks.
	for i := 0; i < 6; i++ {
		sc.engine.ApplySync(func() {
			if i == 0 {
				sc.chat.AddAssistantMessage("")
			}
			sc.chat.UpdateLastMessage(strings.Repeat("chunk of streamed answer text\n", i+1), 0)
		})
		sc.engine.RenderNow()
	}

	// Steering bubble appears (chrome grows) — must NOT wipe scrollback.
	wipeMark := len(sc.term.writes)
	sc.engine.ApplySync(func() { sc.app.subs.steeringChrome.Add("hold on, steering") })
	sc.engine.RenderNow()
	frame1 := sc.engine.AgentFrame()
	sc.filmstrip().Capture("steering-added", frame1, "")
	wroteOnAdd := strings.Join(sc.term.writes[wipeMark:], "")
	if strings.Contains(wroteOnAdd, "\x1b[3J") {
		t.Errorf("steering bubble appearing wiped scrollback (the O(history) reset)")
	}

	// More streaming with the bubble up.
	for i := 0; i < 3; i++ {
		sc.engine.ApplySync(func() {
			sc.chat.AddSystemMessage("post-steering streamed row")
		})
		sc.engine.RenderNow()
	}

	// Steering clears (chrome shrinks) — must NOT wipe scrollback either.
	wipeMark = len(sc.term.writes)
	sc.engine.ApplySync(func() { sc.app.subs.steeringChrome.Clear() })
	sc.engine.RenderNow()
	frame2 := sc.engine.AgentFrame()
	sc.filmstrip().Capture("steering-cleared", frame2, "")
	wroteOnClear := strings.Join(sc.term.writes[wipeMark:], "")
	if strings.Contains(wroteOnClear, "\x1b[3J") {
		t.Errorf("steering bubble clearing wiped scrollback (the O(history) reset)")
	}

	// Input and footer stay pinned at the bottom of the final frame.
	visible := frame2.Visible
	if len(visible) < 20 {
		t.Fatalf("visible frame too short: %d rows", len(visible))
	}
	tail := strings.Join(visible[len(visible)-4:], "\n")
	if !strings.Contains(tail, "no-model") && !strings.Contains(tail, "─") {
		t.Errorf("bottom chrome (input/footer) not pinned at the bottom:\n%s", frame2.Dump())
	}
	// Streamed content is present somewhere (scrollback or screen).
	joined := strings.Join(visible, "\n")
	if !strings.Contains(joined, "post-steering streamed row") {
		t.Errorf("streamed rows missing from the final frame:\n%s", frame2.Dump())
	}
}
