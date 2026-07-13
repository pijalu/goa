// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package verify

import (
	"context"
	"fmt"
)

// Remediator attempts to fix the failures described in a Report. It returns
// a human-readable summary of what was changed (or an error if it cannot
// remediate). A nil Remediator means the loop only runs tests without trying
// to fix them.
type Remediator interface {
	Remediate(ctx context.Context, report Report) (string, error)
}

// RemediateFunc is a function adapter for Remediator.
type RemediateFunc func(ctx context.Context, report Report) (string, error)

// Remediate implements Remediator.
func (f RemediateFunc) Remediate(ctx context.Context, report Report) (string, error) {
	return f(ctx, report)
}

// LoopConfig controls the self-verify loop.
type LoopConfig struct {
	// MaxAttempts is the maximum number of test/remediate iterations.
	// Zero or negative means 1 (test only, no remediation).
	MaxAttempts int
	// Runner executes the test suite. Required.
	Runner Runner
	// Remediator attempts to fix failures. Optional.
	Remediator Remediator
}

// LoopResult is the outcome of a self-verify loop.
type LoopResult struct {
	// Passed is true when the last test run succeeded.
	Passed bool
	// Attempts is the number of test runs executed.
	Attempts int
	// Reports holds the report from each attempt.
	Reports []Report
	// RemediationNotes holds one note per remediation attempt.
	RemediationNotes []string
	// Error is set when the loop failed for a non-test reason (e.g. missing runner).
	Error string
}

// Summary returns a concise human-readable status.
func (r LoopResult) Summary() string {
	if r.Error != "" {
		return fmt.Sprintf("verify loop failed: %s", r.Error)
	}
	if r.Passed {
		return fmt.Sprintf("verify loop passed after %d attempt(s)", r.Attempts)
	}
	return fmt.Sprintf("verify loop failed after %d attempt(s)", r.Attempts)
}

// RunLoop executes the test/remediate loop.
func RunLoop(ctx context.Context, cfg LoopConfig) LoopResult {
	if cfg.Runner == nil {
		return LoopResult{Error: "no runner configured"}
	}
	max := cfg.MaxAttempts
	if max <= 0 {
		max = 1
	}

	result := LoopResult{}
	for attempt := 1; attempt <= max; attempt++ {
		report, err := cfg.Runner.Run(ctx)
		if err != nil {
			result.Error = err.Error()
			result.Reports = append(result.Reports, report)
			return result
		}
		result.Attempts = attempt
		result.Reports = append(result.Reports, report)
		result.Passed = report.Passed
		if report.Passed {
			return result
		}
		if cfg.Remediator == nil || attempt == max {
			break
		}
		note, err := cfg.Remediator.Remediate(ctx, report)
		if err != nil {
			result.Error = err.Error()
			return result
		}
		result.RemediationNotes = append(result.RemediationNotes, note)
	}
	return result
}
