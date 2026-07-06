// SPDX-License-Identifier: GPL-3.0-or-later

package orchestrator

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/pijalu/goa/config"
)

// TestRuntime_ResumeSkipsFinishedRoles is the RED→GREEN test for correct
// orchestration session reload: resuming a finished fanout run re-uses the
// completed work (no new turns), continues the SAME run-id/event-log (not a
// fork), and emits "resumed" lifecycle events for the carried-forward roles.
func TestRuntime_ResumeSkipsFinishedRoles(t *testing.T) {
	dir := t.TempDir()
	cfg := config.OrchestratorConfig{
		Roles: map[string]config.OrchestratorRole{
			"orchestrator": {Model: "m"},
			"coder":        {Model: "m"},
			"reviewer":     {Model: "m"},
		},
		Pool:     config.OrchestratorPoolConfig{MaxTotalAgents: 8},
		Defaults: config.OrchestratorDefaultsConfig{Topology: "fanout"},
	}

	var rt *Runtime
	var turns atomic.Int32
	factory := func(role, model string) (*AgentHandle, error) {
		h := NewAgentHandle("", role, model)
		h.Run = func(ctx context.Context, prompt string) error {
			turns.Add(1)
			rt.RecordAgentMessage(h, "did:"+role)
			return nil
		}
		return h, nil
	}
	store := NewFileEventStore(dir, "run-X")
	pool := NewBoundedAgentPool(cfg, factory)
	rt, _ = NewRuntime(cfg, pool, store, "")
	rt.SetIDGenerator(func() string { return "run-X" })

	ctx := context.Background()
	if err := rt.Run(ctx, "build it"); err != nil {
		t.Fatalf("initial run: %v", err)
	}
	if got := turns.Load(); got != 2 {
		t.Fatalf("initial run drove %d turns, want 2 (coder+reviewer)", got)
	}

	// Resume into a fresh runtime sharing the same store.
	snap, err := ReplaySnapshot(store)
	if err != nil {
		t.Fatalf("ReplaySnapshot: %v", err)
	}
	turns.Store(0)
	var rt2 *Runtime
	factory2 := func(role, model string) (*AgentHandle, error) {
		h := NewAgentHandle("", role, model)
		h.Run = func(ctx context.Context, prompt string) error {
			turns.Add(1)
			rt2.RecordAgentMessage(h, "redo:"+role)
			return nil
		}
		return h, nil
	}
	pool2 := NewBoundedAgentPool(cfg, factory2)
	rt2, _ = NewRuntime(cfg, pool2, nil, "") // store provided via Resume
	rt2.Resume(store, snap)
	if err := rt2.Run(ctx, "build it"); err != nil {
		t.Fatalf("resume run: %v", err)
	}

	if got := turns.Load(); got != 0 {
		t.Errorf("resume re-ran %d turns; want 0 (finished roles must be skipped)", got)
	}

	// Same run-id continued + resumed lifecycle events carried forward.
	events, _ := store.Replay()
	resumed := 0
	for _, e := range events {
		if e.RunID != "run-X" {
			t.Errorf("event %+v runID = %q, want run-X", e.Type, e.RunID)
		}
		if e.Type == EventAgentFinished {
			if o, _ := e.Payload["outcome"].(string); o == "resumed" {
				resumed++
			}
		}
	}
	if resumed != 2 {
		t.Errorf("resumed finished events = %d, want 2 (coder+reviewer)", resumed)
	}
}
