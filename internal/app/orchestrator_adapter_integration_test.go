// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"testing"
	"time"

	"github.com/pijalu/goa/core/orchestrator"
)

// TestOrchestratorAdapter_LiveFanout drives a real fanout orchestration
// against LMStudio and asserts the full lifecycle: bounded pool admits both
// agents, real turns stream, token stats are captured, events are persisted,
// and the replayed snapshot marks the run finished.
//
// It is skipped (not failed) when LMStudio is unreachable or no model is
// configured, so the gate suite stays green on machines without a local model.
func TestOrchestratorAdapter_LiveFanout(t *testing.T) {
	if !lmstudioReachable(t) {
		t.Skip("LMStudio not reachable on :1234 — skipping live orchestrator integration test")
	}
	rt, rootDir := newLiveRuntime(t, []string{"summarizer", "coder"}, "fanout")

	// Collect lifecycle event types as the run progresses.
	var got []orchestrator.EventType
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range rt.Events() {
			got = append(got, ev.Type)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := rt.Run(ctx, "Reply with the single word: ready"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	<-done

	have := map[orchestrator.EventType]bool{}
	for _, e := range got {
		have[e] = true
	}
	for _, want := range []orchestrator.EventType{
		orchestrator.EventRunStarted, orchestrator.EventAgentStarted,
		orchestrator.EventAgentStats, orchestrator.EventAgentFinished,
		orchestrator.EventRunFinished,
	} {
		if !have[want] {
			t.Errorf("missing event %s; got %v", want, got)
		}
	}

	snap := assertRunSnapshotFinished(t, rootDir, 2)
	for id, a := range snap.Agents {
		if a.TokensOut == 0 {
			t.Errorf("agent %s captured zero output tokens (stats observer not wired)", id)
		}
	}
}
