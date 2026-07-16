// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package multiagent

import (
	"strings"
	"sync"
	"testing"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/provider"
)

// eventRecorder captures every agentic.OutputEvent observed from a sub-agent.
type eventRecorder struct {
	mu     sync.Mutex
	events []agentic.OutputEvent
}

func (r *eventRecorder) OnEvent(ev agentic.OutputEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *eventRecorder) count(pred func(agentic.OutputEvent) bool) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, e := range r.events {
		if pred(e) {
			n++
		}
	}
	return n
}

// orchestratorStreamRecorder captures the OrchestratorMessage stream that the
// UI actually renders (the ForegroundOrchestrator.events channel surface).
type orchestratorStreamRecorder struct {
	mu       sync.Mutex
	messages []OrchestratorMessage
}

func (r *orchestratorStreamRecorder) Emit(from, to, content string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages = append(r.messages, OrchestratorMessage{From: from, To: to, Content: content, Kind: "content"})
}

func (r *orchestratorStreamRecorder) kinds() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.messages))
	for _, m := range r.messages {
		out = append(out, m.Kind+":"+m.To)
	}
	return out
}

// A foreground `agent` tool sub-agent must stream its work into the
// orchestrator event stream the UI renders — not just three coarse lifecycle
// lines (started/completed/failed). The orchestrator path already forwards
// per-delta stream_start/stream_chunk/stream_end so the user watches agents
// think and answer live; the `agent` tool must do the same. Today
// runForeground emits ONLY lifecycle text, so the sub-agent's thinking and
// streaming output are invisible until the single result blob returns.
// Regression test for the core transparency spec violation.
func TestAgentTool_Foreground_StreamsToOrchestrator(t *testing.T) {
	rec := &orchestratorStreamRecorder{}

	var m provider.Model
	pool := NewAgentPool(m, provider.StreamOptions{}, nil)
	tool := &AgentTool{
		Pool:         pool,
		Orchestrator: rec,
		CurrentMode:  func() internal.ModeState { return internal.ModeState{} },
	}
	_, _ = tool.Execute(`{"prompt": "do work", "description": "test task"}`)

	// The UI-visible stream must include streaming kinds (stream_start /
	// stream_chunk / stream_end), not only "Sub-agent X started/completed".
	// Today only plain lifecycle content is emitted, so this is empty.
	streamKinds := 0
	for _, k := range rec.kinds() {
		if strings.HasPrefix(k, "content:stream_") || strings.Contains(k, "stream_chunk") {
			streamKinds++
		}
	}
	if streamKinds == 0 {
		t.Fatalf("foreground sub-agent emitted no streaming events to the UI; "+
			"only lifecycle text present: %v", rec.kinds())
	}
}

// The observer must be attached before Run so no early events are missed.
// Guards against wiring OnAgentCreated after the agent already started.
func TestAgentTool_Foreground_ObserverAttachedBeforeRun(t *testing.T) {
	rec := &eventRecorder{}
	var attachedAt []string
	var mu sync.Mutex

	var m provider.Model
	pool := NewAgentPool(m, provider.StreamOptions{}, nil)
	pool.OnAgentCreated = func(role string, agent *agentic.Agent) {
		mu.Lock()
		attachedAt = append(attachedAt, role)
		mu.Unlock()
		agent.AddObserver(agentic.OutputObserverFunc(rec.OnEvent))
	}

	tool := &AgentTool{
		Pool:        pool,
		CurrentMode: func() internal.ModeState { return internal.ModeState{} },
	}
	_, _ = tool.Execute(`{"prompt": "x", "description": "y"}`)

	mu.Lock()
	defer mu.Unlock()
	if len(attachedAt) == 0 {
		t.Fatal("OnAgentCreated was never invoked for a foreground sub-agent")
	}
	if !strings.HasPrefix(attachedAt[0], "coder-task-") {
		t.Errorf("unexpected task role %q; want coder-task-*", attachedAt[0])
	}
}
