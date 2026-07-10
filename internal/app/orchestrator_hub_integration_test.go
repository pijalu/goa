// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
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
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		done <- rt.Run(ctx,
			"Use the 'delegate' tool to ask the 'coder' role: \"Reply with the single word: ready\".")
	}()

	started, wait := drainAgentStarts(rt.Events())

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("hub Run: %v", err)
		}
	case <-time.After(100 * time.Second):
		t.Fatalf("hub run timed out")
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

	// With the conversation-style hub, the orchestrator runs twice: once for
	// planning/delegation and once for synthesis. The coder runs once.
	assertRunSnapshotFinished(t, rootDir, 3)
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
