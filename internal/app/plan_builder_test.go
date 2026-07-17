// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core/plan"
)

// fakePlanRuntime is a planRunRuntime test double.
type fakePlanRuntime struct {
	runErr   error
	panics   bool
	planID   string
	runGiven chan string // receives the objective, closed after Run returns
}

func (f *fakePlanRuntime) SetIDGenerator(func() string) {}
func (f *fakePlanRuntime) SetPlanID(id string)          { f.planID = id }
func (f *fakePlanRuntime) Run(_ context.Context, objective string) error {
	if f.runGiven != nil {
		f.runGiven <- objective
	}
	if f.panics {
		panic("fake orchestrator boom")
	}
	return f.runErr
}

// fakePlanFactory returns a preset runtime.
type fakePlanFactory struct {
	rt  planRunRuntime
	err error
}

func (f fakePlanFactory) NewRuntime(config.OrchestratorConfig, string) (planRunRuntime, error) {
	return f.rt, f.err
}

// newPlanBinderTestStore creates an approved plan in a temp dir, ready for
// execution start.
func newPlanBinderTestStore(t *testing.T) (store *plan.Store, rootDir string) {
	t.Helper()
	rootDir = t.TempDir()
	plansDir := rootDir + "/.goa/plans"
	store, err := plan.Create(plansDir, "test plan")
	if err != nil {
		t.Fatalf("plan.Create: %v", err)
	}
	if err := store.SubmitRevision(); err != nil {
		t.Fatalf("SubmitRevision: %v", err)
	}
	if err := store.Approve(); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	return store, rootDir
}

// newTestBinder builds a PlanBinder over the fake factory with a runDone
// channel for synchronizing on background completion.
func newTestBinder(factory planRuntimeFactory, rootDir string) (*PlanBinder, chan struct{}) {
	done := make(chan struct{})
	b := &PlanBinder{
		factory: factory,
		cfg:     &config.Config{},
		rootDir: rootDir,
		runDone: func() { close(done) },
	}
	return b, done
}

func waitRunDone(t *testing.T, done chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for plan run to finish")
	}
}

// TestPlanExecution_FailingRunMarksPlanFailed is the regression test for the
// use-after-close crash: the run fails AFTER startExecution returned. The
// store must still be open (owned by the run goroutine) so Fail is recorded,
// and the process must not panic.
func TestPlanExecution_FailingRunMarksPlanFailed(t *testing.T) {
	store, rootDir := newPlanBinderTestStore(t)
	rt := &fakePlanRuntime{runErr: errors.New("connection refused")}
	b, done := newTestBinder(fakePlanFactory{rt: rt}, rootDir)

	if err := b.startExecution(store); err != nil {
		t.Fatalf("startExecution: %v", err)
	}
	waitRunDone(t, done)

	// The run owned and closed the store; verify the recorded state from disk.
	check, err := plan.Open(b.plansDir(), store.ID())
	if err != nil {
		t.Fatalf("reopen plan: %v", err)
	}
	defer check.Close()
	if got := check.Plan().Status; got != plan.PlanFailed {
		t.Errorf("plan status = %q, want %q", got, plan.PlanFailed)
	}
	if rt.planID != store.ID() {
		t.Errorf("runtime planID = %q, want %q", rt.planID, store.ID())
	}
}

// TestPlanExecution_PanickingRunMarkedFailedNotCrash ensures a panicking
// orchestrator run is converted into a failed plan instead of crashing the
// process.
func TestPlanExecution_PanickingRunMarkedFailedNotCrash(t *testing.T) {
	store, rootDir := newPlanBinderTestStore(t)
	b, done := newTestBinder(fakePlanFactory{rt: &fakePlanRuntime{panics: true}}, rootDir)

	if err := b.startExecution(store); err != nil {
		t.Fatalf("startExecution: %v", err)
	}
	waitRunDone(t, done) // would not be reached if the panic escaped

	check, err := plan.Open(b.plansDir(), store.ID())
	if err != nil {
		t.Fatalf("reopen plan: %v", err)
	}
	defer check.Close()
	if got := check.Plan().Status; got != plan.PlanFailed {
		t.Errorf("plan status = %q, want %q", got, plan.PlanFailed)
	}
}

// TestPlanExecution_SuccessfulRunFinishesPlan covers the completion path: a
// plan with no pending items (all terminal — trivially true with zero items)
// must transition to done.
func TestPlanExecution_SuccessfulRunFinishesPlan(t *testing.T) {
	store, rootDir := newPlanBinderTestStore(t)
	b, done := newTestBinder(fakePlanFactory{rt: &fakePlanRuntime{}}, rootDir)

	if err := b.startExecution(store); err != nil {
		t.Fatalf("startExecution: %v", err)
	}
	waitRunDone(t, done)

	check, err := plan.Open(b.plansDir(), store.ID())
	if err != nil {
		t.Fatalf("reopen plan: %v", err)
	}
	defer check.Close()
	if got := check.Plan().Status; got != plan.PlanDone {
		t.Errorf("plan status = %q, want %q", got, plan.PlanDone)
	}
}

// TestPlanExecution_SuccessWithPendingItemsLeavesExecuting covers the gap
// where run agents never reported task_outcome: Finish must refuse, the plan
// stays executing, and no failure is recorded.
func TestPlanExecution_SuccessWithPendingItemsLeavesExecuting(t *testing.T) {
	store, rootDir := newPlanBinderTestStore(t)
	if _, err := store.AddItem("task 1", "do something", "", nil, "coder"); err != nil {
		t.Fatalf("AddItem: %v", err)
	}
	b, done := newTestBinder(fakePlanFactory{rt: &fakePlanRuntime{}}, rootDir)

	if err := b.startExecution(store); err != nil {
		t.Fatalf("startExecution: %v", err)
	}
	waitRunDone(t, done)

	check, err := plan.Open(b.plansDir(), store.ID())
	if err != nil {
		t.Fatalf("reopen plan: %v", err)
	}
	defer check.Close()
	if got := check.Plan().Status; got != plan.PlanExecuting {
		t.Errorf("plan status = %q, want %q (not failed, not done)", got, plan.PlanExecuting)
	}
}

// TestPlanExecution_FactoryErrorKeepsStoreOwnership verifies that when the
// runtime cannot be built, startExecution fails and the caller still owns the
// (open, unmodified) store.
func TestPlanExecution_FactoryErrorKeepsStoreOwnership(t *testing.T) {
	store, rootDir := newPlanBinderTestStore(t)
	defer store.Close() // caller retains ownership on error
	b, _ := newTestBinder(fakePlanFactory{err: errors.New("no adapter")}, rootDir)

	if err := b.startExecution(store); err == nil {
		t.Fatal("expected startExecution error")
	}
	if got := store.Plan().Status; got != plan.PlanApproved {
		t.Errorf("plan status = %q, want unchanged %q", got, plan.PlanApproved)
	}
}

// TestPlanExecution_StartByID covers the pager-approval path: approval
// happened on a different store handle, execution starts from a fresh open.
func TestPlanExecution_StartByID(t *testing.T) {
	store, rootDir := newPlanBinderTestStore(t)
	planID := store.ID()
	// The "pager" handle stays open while execution starts — mirrors the
	// review-pager approve flow.
	defer store.Close()

	b, done := newTestBinder(fakePlanFactory{rt: &fakePlanRuntime{}}, rootDir)
	if err := b.startExecutionByID(planID); err != nil {
		t.Fatalf("startExecutionByID: %v", err)
	}
	waitRunDone(t, done)

	check, err := plan.Open(b.plansDir(), planID)
	if err != nil {
		t.Fatalf("reopen plan: %v", err)
	}
	defer check.Close()
	if got := check.Plan().Status; got != plan.PlanDone {
		t.Errorf("plan status = %q, want %q", got, plan.PlanDone)
	}
}
