// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestExecCommandVerifier_TimeoutEnforced pins the verify-command bound
// (bugs.md Bug A: "the goal complete should have a clear timeout"): a command
// that outlives the configured timeout FAILS, is marked timed out, and the
// applied bound travels in the outcome for display.
func TestExecCommandVerifier_TimeoutEnforced(t *testing.T) {
	v := newExecCommandVerifier(t.TempDir(), 100*time.Millisecond)
	start := time.Now()
	outcome := v.Verify(context.Background(), "sleep 5")
	elapsed := time.Since(start)

	if outcome.OK {
		t.Error("command outliving the timeout must fail")
	}
	if !strings.Contains(outcome.Output, "timed out") {
		t.Errorf("output must say the command timed out, got: %q", outcome.Output)
	}
	if outcome.TimeoutMs != 100 {
		t.Errorf("TimeoutMs = %d, want 100", outcome.TimeoutMs)
	}
	if elapsed > 2*time.Second {
		t.Errorf("execution was not bounded by the timeout: %v", elapsed)
	}
}

// TestExecCommandVerifier_DefaultTimeout covers the zero-timeout fallback
// (default 2m) and a passing command's evidence (output, OK, duration).
func TestExecCommandVerifier_DefaultTimeout(t *testing.T) {
	v := newExecCommandVerifier(t.TempDir(), 0)
	outcome := v.Verify(context.Background(), "echo hello")
	if !outcome.OK {
		t.Fatalf("echo must pass, got %+v", outcome)
	}
	if outcome.TimeoutMs != defaultGoalVerifyTimeout.Milliseconds() {
		t.Errorf("TimeoutMs = %d, want default %d", outcome.TimeoutMs, defaultGoalVerifyTimeout.Milliseconds())
	}
	if !strings.Contains(outcome.Output, "hello") {
		t.Errorf("output must contain the command output, got: %q", outcome.Output)
	}
}

// TestExecCommandVerifier_ExitCodePropagates covers non-zero exits (evidence
// for the done-gate failure path).
func TestExecCommandVerifier_ExitCodePropagates(t *testing.T) {
	v := newExecCommandVerifier(t.TempDir(), 5*time.Second)
	outcome := v.Verify(context.Background(), "echo partial && exit 3")
	if outcome.OK {
		t.Error("exit 3 must fail the verification")
	}
	if !strings.Contains(outcome.Output, "partial") {
		t.Errorf("output must be preserved for the failure detail, got: %q", outcome.Output)
	}
}
