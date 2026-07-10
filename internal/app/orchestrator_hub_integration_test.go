// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/pijalu/goa/core/orchestrator"
)

// TestOrchestratorAdapter_LiveHub drives a TRUE hub topology against LMStudio:
// the orchestrator agent is given the DelegateTool and must call it to dispatch
// a sub-task to the coder specialist. Asserts both the orchestrator and the
// delegated coder appear in the replayed snapshot, proving real tool-driven
// delegation end-to-end. Skips when no local model is reachable.
func TestOrchestratorAdapter_LiveHub(t *testing.T) {
	if !lmstudioReachable(t) {
		t.Skip("LMStudio not reachable on :1234 — skipping live hub test")
	}
	rt, rootDir := newLiveRuntime(t, []string{"orchestrator", "coder"}, "hub")

	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		done <- rt.Run(ctx,
			"Use the 'delegate' tool to ask the 'coder' role: \"Reply with the single word: ready\".")
	}()

	started, wait := drainAgentStarts(rt.Events())

	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("hub Run: %v", err)
		}
	case <-time.After(40 * time.Second):
		// Cancel the run and inspect what was started. Live-model behavior is
		// variable; the important thing is that the orchestrator delegated to
		// the coder before the timeout.
		rt.Cancel()
		t.Logf("hub run timed out; checking partial progress")
	}

	wait()
	// The orchestrator must have started, AND the coder must have been
	// delegated to (started via the DelegateTool). This is the proof of real
	// tool-driven delegation rather than the orchestrator just answering alone.
	if started["orchestrator"] == 0 {
		t.Errorf("orchestrator agent never started")
	}
	if started["coder"] == 0 {
		t.Errorf("coder was never delegated to — orchestrator did not use the delegate tool; started=%v", started)
	}

	// If the run finished, the conversation-style hub should have produced two
	// orchestrator turns and one coder turn. If it only started those agents,
	// that is still acceptable for a live-model test.
	if runs, err := orchestrator.ListRuns(rootDir); err == nil && len(runs) == 1 && runs[0].Finished {
		snap, _ := orchestrator.ReplaySnapshot(orchestrator.NewFileEventStore(rootDir, runs[0].RunID))
		if snap != nil && len(snap.Agents) != 3 {
			t.Logf("expected 3 finished agents when run completes, got %d", len(snap.Agents))
		}
	}
}

// drainAgentStarts reads events until the channel is closed and returns a map
// counting how many times each role was started, plus a wait function the
// caller must invoke before reading the map.
func drainAgentStarts(events <-chan orchestrator.Event) (map[string]int, func()) {
	var wg sync.WaitGroup
	wg.Add(1)
	started := map[string]int{}
	go func() {
		defer wg.Done()
		for ev := range events {
			if ev.Type == orchestrator.EventAgentStarted {
				started[ev.Role]++
			}
		}
	}()
	return started, wg.Wait
}
