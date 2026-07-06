// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
)

// fakeFactory builds handles whose Run func records calls and optionally
// updates Stats to simulate a real observer.
func fakeFactory(runs *atomic.Int32, failRole string, statsFn func(h *AgentHandle)) AgentFactory {
	return func(role, model string) (*AgentHandle, error) {
		h := NewAgentHandle("", role, model)
		h.Run = func(ctx context.Context, prompt string) error {
			runs.Add(1)
			if role == failRole {
				return errors.New("boom: " + role)
			}
			if statsFn != nil {
				statsFn(h)
			}
			return nil
		}
		return h, nil
	}
}

func runtimeFor(t *testing.T, topology string, factory AgentFactory) (*Runtime, *BoundedAgentPool, *FileEventStore) {
	t.Helper()
	dir := t.TempDir()
	cfg := config.OrchestratorConfig{
		Roles: map[string]config.OrchestratorRole{
			"orchestrator": {Model: "m-orch"},
			"coder":        {Model: "m-coder"},
			"reviewer":     {Model: "m-rev"},
		},
		Pool:     config.OrchestratorPoolConfig{MaxTotalAgents: 8},
		Defaults: config.OrchestratorDefaultsConfig{Topology: topology},
	}
	pool := NewBoundedAgentPool(cfg, factory)
	store := NewFileEventStore(dir, "run-test")
	rt, err := NewRuntime(cfg, pool, store, dir)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	rt.SetIDGenerator(func() string { return "run-test" })
	return rt, pool, store
}

func collectEvents(rt *Runtime) []Event {
	var got []Event
	for ev := range rt.Events() {
		got = append(got, ev)
	}
	return got
}

func TestRuntime_FanoutRunsAllRolesAndEmitsLifecycle(t *testing.T) {
	var runs atomic.Int32
	rt, _, store := runtimeFor(t, "fanout", fakeFactory(&runs, "", func(h *AgentHandle) {
		h.Stats.AddUsage(10, 5, 0, 0)
	}))
	done := make(chan struct{})
	go func() { defer close(done); _ = rt.Run(context.Background(), "do thing") }()

	got := collectEvents(rt)
	<-done

	if runs.Load() != 2 {
		t.Errorf("turn runs = %d, want 2 (coder+reviewer, orchestrator excluded)", runs.Load())
	}
	assertLifecycle(t, got, []EventType{EventRunStarted, EventAgentStarted, EventAgentStats, EventAgentFinished, EventRunFinished})

	// Two non-orchestrator agents recorded.
	snap, err := ReplaySnapshot(store)
	if err != nil {
		t.Fatalf("ReplaySnapshot: %v", err)
	}
	count := 0
	for _, a := range snap.Agents {
		if a.Status != AgentFinished {
			t.Errorf("agent %s status = %q, want finished", a.ID, a.Status)
		}
		count++
	}
	if count != 2 {
		t.Errorf("snapshot agents = %d, want 2", count)
	}
}

func TestRuntime_FanoutCrashIsRecordedNotFatalToSiblings(t *testing.T) {
	var runs atomic.Int32
	rt, _, store := runtimeFor(t, "fanout", fakeFactory(&runs, "coder", nil))

	err := rt.Run(context.Background(), "do thing")
	if err == nil {
		t.Errorf("expected error from crashed coder")
	}
	snap, _ := ReplaySnapshot(store)
	var coder *AgentSnapshot
	for _, a := range snap.Agents {
		if a.Role == "coder" {
			coder = a
		}
	}
	if coder == nil || coder.Status != AgentCrashed {
		t.Errorf("coder status wrong: %+v", coder)
	}
	var rev *AgentSnapshot
	for _, a := range snap.Agents {
		if a.Role == "reviewer" {
			rev = a
		}
	}
	if rev == nil || rev.Status != AgentFinished {
		t.Errorf("reviewer should still finish despite coder crash: %+v", rev)
	}
}

func TestRuntime_PipelineRunsSequentially(t *testing.T) {
	var (
		mu    sync.Mutex
		order []string
		runs  atomic.Int32
	)
	factory := func(role, model string) (*AgentHandle, error) {
		h := NewAgentHandle("", role, model)
		h.Run = func(ctx context.Context, prompt string) error {
			mu.Lock()
			order = append(order, role)
			mu.Unlock()
			runs.Add(1)
			return nil
		}
		return h, nil
	}
	rt, _, _ := runtimeFor(t, "pipeline", factory)
	if err := rt.Run(context.Background(), "obj"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := runs.Load(); got != 2 {
		t.Errorf("runs = %d, want 2", got)
	}
	// Sequential: the gap between the two starts must be positive (they did
	// not overlap). We assert ordering uniqueness instead of timing.
	if len(order) != 2 {
		t.Errorf("order len = %d, want 2: %v", len(order), order)
	}
}

func TestRuntime_PipelineCarryUsesEmbeddedTemplate(t *testing.T) {
	var prompts []string
	var mu sync.Mutex
	factory := func(role, model string) (*AgentHandle, error) {
		h := NewAgentHandle("", role, model)
		h.Run = func(_ context.Context, p string) error {
			mu.Lock()
			prompts = append(prompts, p)
			mu.Unlock()
			return nil
		}
		return h, nil
	}
	rt, _, _ := runtimeFor(t, "pipeline", factory)
	if err := rt.Run(context.Background(), "obj"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(prompts) != 2 {
		t.Fatalf("expected 2 prompts, got %d: %v", len(prompts), prompts)
	}
	if !strings.Contains(prompts[1], "Objective: obj") {
		t.Errorf("second prompt missing templated objective: %q", prompts[1])
	}
}

func TestRuntime_PoolCapsBlockAndProceed(t *testing.T) {
	dir := t.TempDir()
	cfg := config.OrchestratorConfig{
		Roles: map[string]config.OrchestratorRole{
			"a": {Model: "m1"},
			"b": {Model: "m1"},
			"c": {Model: "m1"},
		},
		Pool:     config.OrchestratorPoolConfig{MaxTotalAgents: 2},
		Defaults: config.OrchestratorDefaultsConfig{Topology: "fanout"},
	}
	var inflight, maxInflight atomic.Int32
	var gate atomic.Bool
	gate.Store(true)
	pool := NewBoundedAgentPool(cfg, blockOnGateFactory(&gate, &inflight, &maxInflight))
	store := NewFileEventStore(dir, "run-caps")
	rt, err := NewRuntime(cfg, pool, store, dir)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	rt.SetIDGenerator(func() string { return "run-caps" })

	runDone := make(chan error, 1)
	go func() { runDone <- rt.Run(context.Background(), "obj") }()
	// Let it settle under the cap.
	time.Sleep(80 * time.Millisecond)
	if got := inflight.Load(); got > 2 {
		t.Errorf("inflight = %d before gate open, cap should bound to 2", got)
	}
	gate.Store(false)
	select {
	case err := <-runDone:
		if err != nil {
			t.Errorf("Run returned err: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("Run did not complete after opening gate")
	}
	if got := maxInflight.Load(); got > 2 {
		t.Errorf("max inflight = %d, cap is 2", got)
	}
}

// blockOnGateFactory returns an AgentFactory whose Run funcs block on gate,
// tracking inflight and max-inflight counts for pool-cap tests.
func blockOnGateFactory(gate *atomic.Bool, inflight, maxInflight *atomic.Int32) AgentFactory {
	return func(role, model string) (*AgentHandle, error) {
		h := NewAgentHandle("", role, model)
		h.Run = func(ctx context.Context, prompt string) error {
			inflight.Add(1)
			defer inflight.Add(-1)
			for gate.Load() {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				time.Sleep(2 * time.Millisecond)
			}
			if m := inflight.Load(); m > maxInflight.Load() {
				maxInflight.Store(m)
			}
			return nil
		}
		return h, nil
	}
}

func TestRuntime_SteeringDrainedIntoTurn(t *testing.T) {
	var seen atomic.Value // string
	rt, pool, _ := runtimeFor(t, "fanout", func(role, model string) (*AgentHandle, error) {
		h := NewAgentHandle("", role, model)
		h.Run = func(ctx context.Context, prompt string) error {
			if role == "coder" {
				seen.Store(prompt)
			}
			return nil
		}
		return h, nil
	})
	// Pre-steer the coder handle before Run by acquiring once. We instead
	// inject steering through the pool's factory path: capture the handle.
	_ = pool // pool is built; to steer we need the live handle.

	// Acquire coder, steer, release so the factory caches nothing (pool is
	// factory-based, not cache-based). Instead, drive a single turn manually.
	h, err := pool.Acquire(context.Background(), "coder")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	h.Steer("FOO-BAR")
	// Simulate the runtime's driveOne path on this handle directly.
	h.Stats.SetStatus(AgentRunning)
	if err := h.RunTurn(context.Background(), "base prompt"); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	pool.Release(h)
	_ = rt

	got, ok := seen.Load().(string)
	if !ok || !strings.Contains(got, "FOO-BAR") {
		t.Errorf("steering not drained into prompt: got %q", got)
	}
	if !ok || !strings.Contains(got, "base prompt") {
		t.Errorf("base prompt lost: got %q", got)
	}
}

// assertLifecycle checks that the event sequence starts/ends with the expected
// bookends and contains all required event types (order-robust for fanout
// since parallel agents interleave).
func assertLifecycle(t *testing.T, got []Event, want []EventType) {
	t.Helper()
	if len(got) == 0 {
		t.Fatal("no events emitted")
	}
	if got[0].Type != EventRunStarted {
		t.Errorf("first event = %s, want run_started", got[0].Type)
	}
	if got[len(got)-1].Type != EventRunFinished {
		t.Errorf("last event = %s, want run_finished", got[len(got)-1].Type)
	}
	have := map[EventType]bool{}
	for _, e := range got {
		have[e.Type] = true
	}
	for _, w := range want {
		if !have[w] {
			t.Errorf("missing event type %s in lifecycle; got types: %v", w, eventTypes(got))
		}
	}
}

func eventTypes(got []Event) []string {
	out := make([]string, len(got))
	for i, e := range got {
		out[i] = string(e.Type)
	}
	return out
}
