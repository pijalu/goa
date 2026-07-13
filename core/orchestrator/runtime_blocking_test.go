// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
)

// slowStore is an EventStore whose Append sleeps, simulating slow disk I/O.
// It records every appended event type so tests can assert none were lost.
type slowStore struct {
	mu     sync.Mutex
	delay  time.Duration
	count  atomic.Int64
	types  []EventType
	closed atomic.Int64
}

func (s *slowStore) Append(evt Event) error {
	s.mu.Lock()
	s.types = append(s.types, evt.Type)
	s.mu.Unlock()
	time.Sleep(s.delay)
	s.count.Add(1)
	return nil
}

func (s *slowStore) Replay() ([]Event, error) { return nil, nil }
func (s *slowStore) Path() string             { return "" }

func (s *slowStore) Flush() {}
func (s *slowStore) Close() error {
	s.closed.Add(1)
	return nil
}

// nopFactory returns handles whose Run is a no-op, sufficient to satisfy the
// pool without a live provider.
func nopFactory() AgentFactory {
	return func(role, model string, _ AcquireOptions) (*AgentHandle, error) {
		h := NewAgentHandle("", role, model)
		return h, nil
	}
}

func newRuntimeWithStore(t *testing.T, store EventStore) *Runtime {
	t.Helper()
	cfg := config.OrchestratorConfig{
		Roles: map[string]config.OrchestratorRole{
			"orchestrator": {Model: "m"},
			"coder":        {Model: "m"},
		},
		Pool:     config.OrchestratorPoolConfig{MaxTotalAgents: 8},
		Defaults: config.OrchestratorDefaultsConfig{Topology: "fanout"},
	}
	pool := NewBoundedAgentPool(cfg, nopFactory())
	rt, err := NewRuntime(cfg, pool, store, "")
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	rt.SetIDGenerator(func() string { return "run-test" })
	return rt
}

// TestRuntime_EmitDoesNotBlockOnSlowStore is the RED test for R1: emitting
// many events must NOT wait for the durable store, because the store write
// runs on the agent's streaming goroutine and blocking it starves the stream
// reader (the "LM Studio freeze"). With a 50ms-per-append store, 20 emits
// must complete far faster than 20*50ms = 1s.
func TestRuntime_EmitDoesNotBlockOnSlowStore(t *testing.T) {
	store := &slowStore{delay: 50 * time.Millisecond}
	rt := newRuntimeWithStore(t, store)
	h := NewAgentHandle("coder-1", "coder", "m")

	start := time.Now()
	for i := 0; i < 20; i++ {
		rt.RecordAgentMessage(h, "x")
	}
	elapsed := time.Since(start)

	// Non-blocking budget: well under one slow append. Synchronous writes
	// would take ~1s; the async path returns in milliseconds.
	if elapsed > 200*time.Millisecond {
		rt.Stop()
		t.Fatalf("RecordAgentMessage blocked on the store: 20 emits took %v (want non-blocking; sync would be ~1s)", elapsed)
	}

	// All events must still be persisted — durability is decoupled, not
	// dropped. Wait for the async writer to drain.
	deadline := time.Now().Add(5 * time.Second)
	for store.count.Load() < 20 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := store.count.Load(); got != 20 {
		t.Errorf("durable persistence lost events: stored %d, want 20", got)
	}

	rt.Stop()
}

// TestRuntime_EmitPreservesOrder guards the async writer's FIFO invariant:
// events must reach the store in the order they were emitted, since downstream
// consumers (run snapshots) rely on seq ordering.
func TestRuntime_EmitPreservesOrder(t *testing.T) {
	store := &slowStore{delay: time.Millisecond}
	rt := newRuntimeWithStore(t, store)
	rt.SetIDGenerator(func() string { return "run-order" })
	h := NewAgentHandle("coder-1", "coder", "m")

	rt.RecordAgentThinking(h, "t1")
	rt.RecordAgentMessage(h, "m1")
	rt.RecordAgentToolCall(h, "writefile", "{}", "c1", false)
	rt.RecordAgentToolResult(h, "c1", "ok", true)

	deadline := time.Now().Add(2 * time.Second)
	want := []EventType{EventAgentThinking, EventAgentMessage, EventAgentToolCall, EventAgentToolResult}
	for {
		store.mu.Lock()
		got := append([]EventType(nil), store.types...)
		store.mu.Unlock()
		if len(got) >= len(want) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for events; got %v", got)
		}
		time.Sleep(2 * time.Millisecond)
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	for i, w := range want {
		if i >= len(store.types) || store.types[i] != w {
			t.Fatalf("event %d = %v, want %v (full: %v)", i, store.types, want, store.types)
		}
	}
	rt.Stop()
}

// ensure context import is used when extended later.
var _ = context.Background

// blockingStore's Append blocks until its gate is closed, simulating a disk
// path that has stalled so the durable queue saturates.
type blockingStore struct {
	gate  chan struct{}
	count atomic.Int64
}

func (s *blockingStore) Append(evt Event) error {
	s.count.Add(1)
	<-s.gate
	return nil
}
func (s *blockingStore) Replay() ([]Event, error) { return nil, nil }
func (s *blockingStore) Path() string             { return "" }
func (s *blockingStore) Flush()                   {}
func (s *blockingStore) Close() error             { return nil }

// TestRuntime_EmitDoesNotBlockWhenSinkSaturated proves the R1 invariant holds
// even when the durable writer itself has stalled: submit must still never
// block the caller (the streaming goroutine). The overflow counter climbs
// instead of back-pressuring the stream.
func TestRuntime_EmitDoesNotBlockWhenSinkSaturated(t *testing.T) {
	store := &blockingStore{gate: make(chan struct{})}
	rt := newRuntimeWithStore(t, store)
	h := NewAgentHandle("coder-1", "coder", "m")

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Far more than durableBufferSize while the writer is blocked.
		for i := 0; i < durableBufferSize+100; i++ {
			rt.RecordAgentMessage(h, "x")
		}
	}()
	select {
	case <-done:
		// submitted without blocking despite a stalled writer
	case <-time.After(3 * time.Second):
		close(store.gate)
		rt.Stop()
		t.Fatal("RecordAgentMessage blocked while the durable sink was saturated")
	}

	close(store.gate) // release the stalled writer so Stop can drain and return
	rt.Stop()
}
