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

// TestOrchestratorAdapter_LiveAgentStartedCarriesProvider asserts that the
// EventAgentStarted payload carries the resolved provider id (T1 plumbing),
// so the tabbed-run stats table can render the per-role provider column from
// a single event without a second lookup. Skipped without a local model.
func TestOrchestratorAdapter_LiveAgentStartedCarriesProvider(t *testing.T) {
	if !lmstudioReachable(t) {
		t.Skip("LMStudio not reachable on :1234 — skipping live orchestrator integration test")
	}
	rt, _ := newLiveRuntime(t, []string{"summarizer", "coder"}, "fanout")

	var started []orchestrator.Event
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range rt.Events() {
			if ev.Type == orchestrator.EventAgentStarted {
				started = append(started, ev)
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := rt.Run(ctx, "Reply with the single word: ready"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	<-done

	if len(started) == 0 {
		t.Fatal("no EventAgentStarted observed")
	}
	for _, ev := range started {
		p, ok := ev.Payload["provider"]
		if !ok {
			t.Errorf("EventAgentStarted for %s missing provider payload: %+v", ev.Role, ev.Payload)
			continue
		}
		if s, _ := p.(string); s == "" {
			t.Errorf("EventAgentStarted for %s has empty provider", ev.Role)
		}
		if _, ok := ev.Payload["thinking"]; !ok {
			t.Errorf("EventAgentStarted for %s missing thinking payload", ev.Role)
		}
	}
}
