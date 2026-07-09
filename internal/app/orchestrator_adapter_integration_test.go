// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/core/orchestrator"
	orchpanel "github.com/pijalu/goa/tui/orchestrator"
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

// runLiveIntoView runs a live orchestration to completion, translating every
// runtime event into a fresh MultiAgentView, and returns the view + runtime.
// Used by the §4.1/4.3/4.4 live UI validations (all skip without LMStudio).
func runLiveIntoView(t *testing.T, roles []string, topology string) (*orchpanel.MultiAgentView, *orchestrator.Runtime) {
	t.Helper()
	rt, _ := newLiveRuntime(t, roles, topology)
	view := orchpanel.NewMultiAgentView("orchestration")
	sub := rt.Subscribe()
	runDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		runDone <- rt.Run(ctx, "Reply with the single word: ready")
	}()
	for {
		select {
		case ev := <-sub:
			if ne, ok := translateOrchEvent(ev); ok {
				view.ApplyEvent(ne)
			}
		case <-rt.Done():
			<-runDone
			return view, rt
		}
	}
}

// TestOrchestrator_LiveFanout_RendersTabbedView (§4.1) runs a 2-role fanout
// against LMStudio, feeds events to a MultiAgentView, and asserts the view
// shows the Conversation and Stats tabs and finishes. Per-agent filter tabs are
// replaced by ctrl-x steering targets.
func TestOrchestrator_LiveFanout_RendersTabbedView(t *testing.T) {
	if !lmstudioReachable(t) {
		t.Skip("LMStudio not reachable on :1234 — skipping live orchestrator integration test")
	}
	view, rt := runLiveIntoView(t, []string{"summarizer", "coder"}, "fanout")
	keys := make([]string, 0, 4)
	for _, tab := range view.Tabs() {
		keys = append(keys, tab.Key)
	}
	if len(keys) != 2 || keys[0] != "stats" || keys[1] != "conversation" {
		t.Errorf("live fanout tabs = %v, want [stats conversation]", keys)
	}
	if !view.Finished() {
		t.Error("live fanout view not finished")
	}
	for _, role := range []string{"summarizer", "coder"} {
		if rt.MessageFor(role) == "" {
			t.Errorf("role %s produced no streamed message", role)
		}
	}
}

// TestOrchestrator_LiveFanout_CacheHitReported (§4.4) asserts cache_read is
// correctly parsed (>=0) and displayed from the live stats events. If the local
// model does not report cache hits, the column must still render ("-" or a
// number) so the display path is covered regardless.
func TestOrchestrator_LiveFanout_CacheHitReported(t *testing.T) {
	if !lmstudioReachable(t) {
		t.Skip("LMStudio not reachable on :1234 — skipping live orchestrator integration test")
	}
	view, _ := runLiveIntoView(t, []string{"summarizer", "coder"}, "fanout")
	for _, row := range view.Rows() {
		if row.CacheRead < 0 {
			t.Errorf("role %s cache_read = %d, want >= 0", row.Role, row.CacheRead)
		}
	}
	// Rendering must not panic and must include the CH column.
	lines := orchpanel.RenderStatsTable(view.Rows(), 90)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "CH") {
		t.Errorf("stats table missing CH column")
	}
}

// startCollectingAgentStartedEvents begins a goroutine that drains rt.Events()
// and returns every EventAgentStarted. The caller must wait on the returned
// channel before inspecting the slice.
func startCollectingAgentStartedEvents(rt *orchestrator.Runtime) ([]orchestrator.Event, chan struct{}) {
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
	return started, done
}

// assertUniqueAgentIDs fails if any started event repeats an AgentID.
func assertUniqueAgentIDs(t *testing.T, started []orchestrator.Event) {
	t.Helper()
	seen := map[string]bool{}
	for _, ev := range started {
		if ev.AgentID == "" {
			continue
		}
		if seen[ev.AgentID] {
			t.Errorf("duplicate AgentID across delegations: %s (shared-agent leak)", ev.AgentID)
		}
		seen[ev.AgentID] = true
	}
}

// assertReplayableSnapshot fails if the run did not persist a replayable snapshot.
func assertReplayableSnapshot(t *testing.T, rootDir string) {
	t.Helper()
	runs, err := orchestrator.ListRuns(rootDir)
	if err != nil || len(runs) != 1 {
		t.Fatalf("ListRuns: %d runs (%v), want 1", len(runs), err)
	}
	snap, err := orchestrator.ReplaySnapshot(orchestrator.NewFileEventStore(rootDir, runs[0].RunID))
	if err != nil || !snap.Started {
		t.Fatalf("snapshot not started/Readable: snap=%+v err=%v", snap, err)
	}
}

// TestOrchestratorAdapter_LiveHubDelegationIsolation is the LMStudio e2e for
// the delegation-isolation fix cluster (R2/R3/R7/R12): a hub run must complete
// against a real model with every started agent carrying a DISTINCT agent id
// (no two handles collide on the shared cached agent), and the run must
// persist a replayable snapshot. It does not require the model to call
// delegate (model behaviour varies); it asserts the wiring is crash-free and
// attribution is unique. Skipped without LMStudio.
func TestOrchestratorAdapter_LiveHubDelegationIsolation(t *testing.T) {
	if !lmstudioReachable(t) {
		t.Skip("LMStudio not reachable on :1234 — skipping live hub e2e")
	}
	rt, rootDir := newLiveRuntime(t, []string{"coder"}, "hub")

	started, done := startCollectingAgentStartedEvents(rt)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if err := rt.Run(ctx, "Reply with the single word: ready"); err != nil {
		t.Fatalf("hub Run: %v", err)
	}
	<-done

	assertUniqueAgentIDs(t, started)
	assertReplayableSnapshot(t, rootDir)
}
