// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// The soft-repeat guard must NOT block the 2nd identical consecutive tool
// call. Re-running a tool twice is frequently legitimate (re-read a file that
// changed, re-run a test after a fix, poll a build). Only the CONFIGURED
// limits (MaxToolRepeatConsecutive, MaxToolCalls rolling window,
// MaxToolRepeatTotal) should actually skip execution; below them, a repeat
// may earn a hint but the tool must still run.
//
// Regression test for the over-sensitive guard: budgetOrRepeatSkipMessage
// returned a skip message at consecutiveCount >= 2, which routed through
// applyToolGuardrail → budgetToolCalls, and scheduleAndRunToolCalls then
// SKIPPED execution of the 2nd identical call.
func TestSoftRepeatGuardrail_SecondCallNotSkipped(t *testing.T) {
	a := NewAgent(Config{
		Model:                     testModel(provider.ApiOpenAICompletions),
		MaxToolRepeatConsecutive:  2, // configured: block at 3rd consecutive
		MaxToolRepeatTotal:        10,
	})

	callKey := "read::{\"path\":\"x.go\"}"

	// 1st call: no skip.
	a.mu.Lock()
	w1, c1, _, _ := a.recordToolCallInBudgetWindow(callKey)
	a.mu.Unlock()
	if msg := a.budgetOrRepeatSkipMessage(w1, c1); msg != "" {
		t.Fatalf("1st call must not be skipped, got: %q", msg)
	}

	// 2nd identical consecutive call: must NOT be skipped (advisory only).
	a.mu.Lock()
	w2, c2, _, _ := a.recordToolCallInBudgetWindow(callKey)
	a.mu.Unlock()
	if msg := a.budgetOrRepeatSkipMessage(w2, c2); msg != "" {
		t.Errorf("2nd identical consecutive call must still execute (hint only, no skip); got skip msg: %q", msg)
	}

	// 3rd identical consecutive call: exceeds MaxToolRepeatConsecutive(2) → skip.
	a.mu.Lock()
	w3, c3, _, _ := a.recordToolCallInBudgetWindow(callKey)
	a.mu.Unlock()
	if msg := a.budgetOrRepeatSkipMessage(w3, c3); msg == "" {
		t.Errorf("3rd identical consecutive call should be skipped (limit MaxToolRepeatConsecutive=2)")
	}
}

// Non-consecutive duplicates (A, B, A) must not trigger the consecutive guard
// at all — only the rolling-window / total limits apply.
func TestSoftRepeatGuardrail_NonConsecutiveAllowed(t *testing.T) {
	a := NewAgent(Config{
		Model:                    testModel(provider.ApiOpenAICompletions),
		MaxToolRepeatConsecutive: 2,
		MaxToolRepeatTotal:       10,
	})
	keyA := "read::{\"path\":\"a\"}"
	keyB := "read::{\"path\":\"b\"}"

	for i, key := range []string{keyA, keyB, keyA, keyB} {
		a.mu.Lock()
		w, c, _, _ := a.recordToolCallInBudgetWindow(key)
		a.mu.Unlock()
		if msg := a.budgetOrRepeatSkipMessage(w, c); msg != "" {
			t.Errorf("call %d (%s) non-consecutive must not be skipped, got: %q", i, key, msg)
		}
	}
}
