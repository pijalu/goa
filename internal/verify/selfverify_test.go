// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package verify

import (
	"context"
	"errors"
	"testing"
)

type fakeRunner struct {
	reports []Report
	idx     int
}

func (f *fakeRunner) Run(ctx context.Context) (Report, error) {
	if f.idx >= len(f.reports) {
		return Report{Passed: true}, nil
	}
	r := f.reports[f.idx]
	f.idx++
	return r, nil
}

func (f *fakeRunner) Name() string { return "fake" }

func TestRunLoop_PassesFirstTry(t *testing.T) {
	runner := &fakeRunner{reports: []Report{{Passed: true}}}
	result := RunLoop(context.Background(), LoopConfig{Runner: runner, MaxAttempts: 3})
	if !result.Passed {
		t.Error("expected loop to pass")
	}
	if result.Attempts != 1 {
		t.Errorf("expected 1 attempt, got %d", result.Attempts)
	}
	if result.Error != "" {
		t.Errorf("unexpected error: %s", result.Error)
	}
}

func TestRunLoop_RemediatesThenPasses(t *testing.T) {
	runner := &fakeRunner{reports: []Report{
		{Passed: false, Failures: []Failure{{Test: "TestBroken"}}},
		{Passed: true},
	}}
	rem := RemediateFunc(func(ctx context.Context, report Report) (string, error) {
		return "fixed TestBroken", nil
	})
	result := RunLoop(context.Background(), LoopConfig{Runner: runner, Remediator: rem, MaxAttempts: 3})
	if !result.Passed {
		t.Error("expected loop to pass after remediation")
	}
	if result.Attempts != 2 {
		t.Errorf("expected 2 attempts, got %d", result.Attempts)
	}
	if len(result.RemediationNotes) != 1 {
		t.Errorf("expected 1 remediation note, got %d", len(result.RemediationNotes))
	}
}

func TestRunLoop_RunsOutOfAttempts(t *testing.T) {
	runner := &fakeRunner{reports: []Report{
		{Passed: false},
		{Passed: false},
		{Passed: false},
	}}
	rem := RemediateFunc(func(ctx context.Context, report Report) (string, error) {
		return "attempted", nil
	})
	result := RunLoop(context.Background(), LoopConfig{Runner: runner, Remediator: rem, MaxAttempts: 3})
	if result.Passed {
		t.Error("expected loop to fail")
	}
	if result.Attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", result.Attempts)
	}
}

func TestRunLoop_NoRunner(t *testing.T) {
	result := RunLoop(context.Background(), LoopConfig{})
	if result.Error == "" {
		t.Error("expected error for missing runner")
	}
}

func TestRunLoop_RemediationError(t *testing.T) {
	runner := &fakeRunner{reports: []Report{{Passed: false}}}
	rem := RemediateFunc(func(ctx context.Context, report Report) (string, error) {
		return "", errors.New("cannot fix")
	})
	result := RunLoop(context.Background(), LoopConfig{Runner: runner, Remediator: rem, MaxAttempts: 3})
	if result.Error != "cannot fix" {
		t.Errorf("expected remediation error, got %v", result)
	}
}

func TestRunLoop_NoRemediatorTestFails(t *testing.T) {
	runner := &fakeRunner{reports: []Report{{Passed: false}}}
	result := RunLoop(context.Background(), LoopConfig{Runner: runner, MaxAttempts: 3})
	if result.Passed {
		t.Error("expected loop to fail")
	}
	if result.Attempts != 1 {
		t.Errorf("expected 1 attempt, got %d", result.Attempts)
	}
	if len(result.RemediationNotes) != 0 {
		t.Errorf("expected no remediation notes, got %d", len(result.RemediationNotes))
	}
}

func TestLoopResult_Summary(t *testing.T) {
	if got := (LoopResult{Passed: true, Attempts: 1}).Summary(); got != "verify loop passed after 1 attempt(s)" {
		t.Errorf("summary = %q", got)
	}
	if got := (LoopResult{Passed: false, Attempts: 2}).Summary(); got != "verify loop failed after 2 attempt(s)" {
		t.Errorf("summary = %q", got)
	}
	if got := (LoopResult{Error: "oops"}).Summary(); got != "verify loop failed: oops" {
		t.Errorf("summary = %q", got)
	}
}

type errorRunner struct{}

func (errorRunner) Run(ctx context.Context) (Report, error) {
	return Report{}, errors.New("runner failed")
}

func (errorRunner) Name() string { return "error" }

func TestRunLoop_RunnerError(t *testing.T) {
	result := RunLoop(context.Background(), LoopConfig{Runner: errorRunner{}, MaxAttempts: 3})
	if result.Error != "runner failed" {
		t.Errorf("expected runner error, got %v", result)
	}
}
