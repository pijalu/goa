// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"errors"
	"strings"
	"testing"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/plan"
)

// newApproveTestPlan creates an in-review plan inside rootDir/.goa/plans and
// returns its ID.
func newApproveTestPlan(t *testing.T, rootDir string) string {
	t.Helper()
	store, err := plan.Create(rootDir+"/.goa/plans", "approve me")
	if err != nil {
		t.Fatalf("plan.Create: %v", err)
	}
	id := store.ID()
	if err := store.SubmitRevision(); err != nil {
		t.Fatalf("SubmitRevision: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return id
}

func planTestContext() (core.Context, *strings.Builder) {
	var buf strings.Builder
	return core.Context{OutputBuffer: &buf}, &buf
}

// TestPlanApprove_StartFailureThenRetrySucceeds covers the idempotent-retry
// path: approve succeeds but the execution start fails, leaving the plan
// approved. A second /plan approve must skip re-approval (Approve would error
// on an approved plan) and retry the execution start.
func TestPlanApprove_StartFailureThenRetrySucceeds(t *testing.T) {
	rootDir := t.TempDir()
	planID := newApproveTestPlan(t, rootDir)

	var starts int
	cmd := &PlanCommand{
		RootDir: rootDir,
		StartExecution: func(store *plan.Store) error {
			starts++
			if starts == 1 {
				return errors.New("orchestrator unavailable")
			}
			// Mirror the real binder's contract: record the execution start.
			return store.StartExecution("run-test")
		},
	}

	ctx, _ := planTestContext()

	// First attempt: approval persists, start fails.
	if err := cmd.Run(ctx, []string{"approve", "id=" + planID}); err == nil ||
		!strings.Contains(err.Error(), "start execution") {
		t.Fatalf("first approve: expected start-execution error, got %v", err)
	}

	// Retry: must NOT fail with "cannot approve plan in status approved".
	if err := cmd.Run(ctx, []string{"approve", "id=" + planID}); err != nil {
		t.Fatalf("retry approve: %v", err)
	}
	if starts != 2 {
		t.Errorf("StartExecution called %d times, want 2", starts)
	}

	check, err := plan.Open(rootDir+"/.goa/plans", planID)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer check.Close()
	if got := check.Plan().Status; got != plan.PlanExecuting {
		t.Errorf("status = %q, want %q", got, plan.PlanExecuting)
	}
}

// TestPlanApprove_StoreClosedOnStartFailure verifies the caller keeps (and
// closes) the store when StartExecution fails — no leaked handle, and the
// recorded status remains approved.
func TestPlanApprove_StoreClosedOnStartFailure(t *testing.T) {
	rootDir := t.TempDir()
	planID := newApproveTestPlan(t, rootDir)

	cmd := &PlanCommand{
		RootDir:        rootDir,
		StartExecution: func(store *plan.Store) error { return errors.New("boom") },
	}
	ctx, _ := planTestContext()

	if err := cmd.Run(ctx, []string{"approve", "id=" + planID}); err == nil {
		t.Fatal("expected error")
	}

	// Reopening must work (no stale lock/handle) and show approved-not-executing.
	check, err := plan.Open(rootDir+"/.goa/plans", planID)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer check.Close()
	if got := check.Plan().Status; got != plan.PlanApproved {
		t.Errorf("status = %q, want %q", got, plan.PlanApproved)
	}
}
