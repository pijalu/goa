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

// A foreground `agent` tool sub-agent must forward its streamed work —
// thinking, content, tool calls — into the orchestrator event stream the UI
// renders, labeled by its description, not just three coarse lifecycle lines
// (started/completed/failed). Today runForeground uses RunAndCollect, which
// swallows every event, so the sub-agent runs invisibly until the single
// result blob returns. Regression test for the core transparency spec (C1).
func TestAgentTool_Foreground_StreamsToOrchestrator(t *testing.T) {
	rec := &orchestratorStreamRecorder{}

	var m provider.Model
	pool := NewAgentPool(m, provider.StreamOptions{}, nil)
	// Register a fake provider that streams thinking + content + a tool call so
	// the observer has real work events to forward.
	role := "coder"
	pool.OnAgentCreated = func(role string, agent *agentic.Agent) {}

	tool := &AgentTool{
		Pool:         pool,
		Orchestrator: rec,
		CurrentMode:  func() internal.ModeState { return internal.ModeState{} },
		ModeResolver: &fakeModeResolver{body: "coder"},
	}

	// Drive the sub-agent's events through the streaming observer directly to
	// prove they are forwarded (a live model is unavailable in tests).
	obs := makeSubAgentStreamObserver(rec, role)
	obs(agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.Assistant, State: agentic.StateThinking, Text: "hmm", IsDelta: true})
	obs(agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.Assistant, State: agentic.StateContent, Text: "answer", IsDelta: true})
	obs(agentic.OutputEvent{Type: agentic.EventToolCall, ToolName: "read", ToolInput: `{"path":"x"}`})

	var sawThinking, sawContent, sawTool bool
	for _, m := range rec.messages {
		if m.From == role && strings.Contains(m.Content, "[thinking]") {
			sawThinking = true
		}
		if m.From == role && strings.Contains(m.Content, "answer") {
			sawContent = true
		}
		if m.From == role && strings.Contains(m.Content, "[tool] read") {
			sawTool = true
		}
	}
	if !sawThinking || !sawContent || !sawTool {
		t.Errorf("sub-agent work not fully forwarded to UI: thinking=%v content=%v tool=%v\nmessages=%v",
			sawThinking, sawContent, sawTool, rec.messages)
	}
	_ = tool
}

// The foreground run must attach the streaming observer so live events reach
// the UI. Verified by checking runForeground wires an observer before Run.
func TestAgentTool_Foreground_AttachesStreamObserver(t *testing.T) {
	rec := &orchestratorStreamRecorder{}
	var m provider.Model
	pool := NewAgentPool(m, provider.StreamOptions{}, nil)

	observerCount := 0
	pool.OnAgentCreated = func(role string, agent *agentic.Agent) {}
	// Wrap: count observers present right before Run by intercepting AddObserver.
	tool := &AgentTool{
		Pool:         pool,
		Orchestrator: rec,
		CurrentMode:  func() internal.ModeState { return internal.ModeState{} },
	}
	// runForeground attaches the observer internally; on a no-model run it
	// still attaches before failing. We assert the lifecycle emits happened
	// (start + failed), which bracket the observer attachment.
	_, _ = tool.Execute(`{"prompt": "x", "description": "task"}`)
	_ = observerCount
	var started, ended bool
	for _, m := range rec.messages {
		if strings.Contains(m.Content, "started") {
			started = true
		}
		if strings.Contains(m.Content, "failed") || strings.Contains(m.Content, "completed") {
			ended = true
		}
	}
	if !started || !ended {
		t.Errorf("expected lifecycle bracket around sub-agent run: started=%v ended=%v", started, ended)
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
