// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// Dedicated tests for the repeated-failure guardrail: when the SAME tool fails
// several times in a row, something is wrong and the model must be nudged with
// a hint (not silently allowed to keep burning calls). This is the "repeated
// failure" detector, distinct from the exact tool+args repeat detector.

// failRunner drives one tool call through the error-streak path and records
// its outcome, returning the guardrail hint ("" = call would execute).
func failRunner(a *Agent, tool string, execErr error) string {
	hint := a.errorStreakSkipMessage(tool)
	a.recordToolExecOutcome(tool, execErr)
	return hint
}

// TestRepeatedFailure_SameToolInARowTriggersHint is the canonical case: a tool
// failing MaxToolErrorStreak times consecutively must surface a hint naming
// the tool and telling the model to change approach.
func TestRepeatedFailure_SameToolInARowTriggersHint(t *testing.T) {
	const limit = 4
	a := newLoopAgent(t, Config{MaxToolErrorStreak: limit})

	// limit-1 failures: still no hint (model may be legitimately iterating).
	for i := 1; i < limit; i++ {
		if hint := failRunner(a, "python", fmt.Errorf("failure %d", i)); hint != "" {
			t.Fatalf("failure %d of %d must not hint yet, got: %q", i, limit, hint)
		}
	}
	// The limit-th consecutive failure: the NEXT call is hinted.
	a.recordToolExecOutcome("python", errors.New("failure 4"))
	hint := a.errorStreakSkipMessage("python")
	if hint == "" {
		t.Fatalf("%d consecutive failures must produce a hint", limit)
	}
	if !IsGuardrailResult(hint) {
		t.Errorf("hint must be recognised as a guardrail result, got: %q", hint)
	}
	if !strings.Contains(hint, "python") {
		t.Errorf("hint must name the failing tool, got: %q", hint)
	}
}

// Table-driven: what breaks (or preserves) a repeated-failure streak.
func TestRepeatedFailure_StreakLifecycle(t *testing.T) {
	type step struct {
		tool    string
		execErr error // nil = success
	}
	tests := []struct {
		name      string
		limit     int
		steps     []step
		wantHints int // total hints expected across the whole sequence
	}{
		{
			name:  "never reaches limit -> no hint",
			limit: 4,
			steps: []step{
				{"python", errors.New("x")},
				{"python", errors.New("x")},
				{"python", errors.New("x")},
			},
			wantHints: 0,
		},
		{
			name:  "success breaks the streak before limit",
			limit: 3,
			steps: []step{
				{"python", errors.New("x")},
				{"python", errors.New("x")},
				{"python", nil}, // success resets
				{"python", errors.New("x")},
				{"python", errors.New("x")},
			},
			wantHints: 0,
		},
		{
			name:  "switching tools breaks the streak",
			limit: 3,
			steps: []step{
				{"python", errors.New("x")},
				{"python", errors.New("x")},
				{"bash", nil}, // different tool + success resets
				{"python", errors.New("x")},
				{"python", errors.New("x")},
			},
			wantHints: 0,
		},
		{
			name:  "reaching limit hints exactly once per episode",
			limit: 3,
			steps: []step{
				{"python", errors.New("x")},
				{"python", errors.New("x")},
				{"python", errors.New("x")}, // limit reached -> next check hints
				{"python", errors.New("x")}, // same episode: no second hint
				{"python", errors.New("x")}, // still same episode
			},
			wantHints: 1,
		},
		{
			name:  "streak re-arms after a success",
			limit: 2,
			steps: []step{
				{"python", errors.New("x")},
				{"python", errors.New("x")}, // limit reached -> hint #1 fires here
				{"python", nil},             // success resets the streak
				{"python", errors.New("x")},
				{"python", errors.New("x")}, // limit reached again -> hint #2 fires here
				{"python", errors.New("x")}, // extra step so the re-armed hint is observed
			},
			wantHints: 2,
		},
		{
			name:  "disabled (limit 0) never hints",
			limit: 0,
			steps: []step{
				{"python", errors.New("x")},
				{"python", errors.New("x")},
				{"python", errors.New("x")},
				{"python", errors.New("x")},
			},
			wantHints: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newLoopAgent(t, Config{MaxToolErrorStreak: tt.limit})
			hints := 0
			for i, s := range tt.steps {
				if hint := failRunner(a, s.tool, s.execErr); hint != "" {
					hints++
					t.Logf("step %d: hint: %q", i, hint)
				}
			}
			if hints != tt.wantHints {
				t.Errorf("got %d hints, want %d", hints, tt.wantHints)
			}
		})
	}
}

// TestRepeatedFailure_HintDoesNotCountAsExecution: a hinted (skipped) call
// produces no real execution result, so it must NOT extend or reset the
// streak — only real outcomes do.
func TestRepeatedFailure_HintDoesNotExtendStreak(t *testing.T) {
	const limit = 3
	a := newLoopAgent(t, Config{MaxToolErrorStreak: limit})

	for i := 0; i < limit; i++ {
		failRunner(a, "python", errors.New("boom"))
	}
	// First check at limit: hint fires.
	if hint := a.errorStreakSkipMessage("python"); hint == "" {
		t.Fatalf("expected hint at limit")
	}
	// The hinted call was skipped (no recordToolExecOutcome). A further real
	// failure is a NEW episode step but errStreakNudged is still set, so no
	// immediate re-hint; a real success must clear it.
	a.recordToolExecOutcome("python", errors.New("boom"))
	if hint := a.errorStreakSkipMessage("python"); hint != "" {
		t.Errorf("already-nudged episode must not re-hint, got: %q", hint)
	}
	a.recordToolExecOutcome("python", nil)
	if hint := a.errorStreakSkipMessage("python"); hint != "" {
		t.Errorf("success must reset the streak, got: %q", hint)
	}
}

// Ensure the stub tools used across these tests satisfy the interfaces the
// guardrail relies on.
var (
	_ Tool         = mutatingStubTool{}
	_ Tool         = readOnlyStubTool{}
	_ StateMutator = mutatingStubTool{}
)

func TestRepeatedFailure_UsesProviderModel(t *testing.T) {
	// Sanity: the harness agent has a usable model + registry so
	// errorStreakSkipMessage / recordToolExecOutcome exercise the real paths.
	a := newLoopAgent(t, Config{Model: testModel(provider.ApiOpenAICompletions)})
	if a.reg == nil {
		t.Fatalf("agent registry must be initialised")
	}
	if _, ok := a.reg.Get("python"); ok {
		t.Log("python tool not registered in stub registry; using generic names")
	}
}
