// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package multiagent

import (
	"strings"
	"sync"
	"testing"

	"github.com/pijalu/goa/core/tasks"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic/provider"
)

func TestAgentTool_Execute_PlannerBlocksNonPlanSubagents(t *testing.T) {
	tool := &AgentTool{
		CurrentMode: func() internal.ModeState { return internal.ModeState{Major: internal.MajorPlanner} },
	}
	_, err := tool.Execute(`{"prompt": "write code", "description": "coding", "subagent_type": "coder"}`)
	if err == nil {
		t.Fatal("expected planner mode to reject coder sub-agent")
	}
	if !strings.Contains(err.Error(), "plan") {
		t.Errorf("error should mention plan sub-agents, got: %v", err)
	}
}

func TestAgentTool_Execute_PlannerAllowsPlanSubagent(t *testing.T) {
	// A plan sub-agent is still disallowed when there is no pool, but we just
	// need to verify the planner-specific gate is not the reason for failure.
	tool := &AgentTool{
		CurrentMode: func() internal.ModeState { return internal.ModeState{Major: internal.MajorPlanner} },
	}
	_, err := tool.Execute(`{"prompt": "plan", "description": "planning", "subagent_type": "plan"}`)
	if err == nil {
		t.Fatal("expected error when pool is nil")
	}
	if strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("planner should be allowed to spawn plan sub-agents, got: %v", err)
	}
}

type fakeModeResolver struct {
	body string
}

func (f *fakeModeResolver) Resolve(major string) (ModeSpec, error) {
	return ModeSpec{Body: f.body, AllowedTools: []string{"read", "write"}, Temperature: 0.2}, nil
}

func TestAgentTool_Schema(t *testing.T) {
	tool := &AgentTool{}
	schema := tool.Schema()
	if schema.Name != "agent" {
		t.Errorf("name = %q, want agent", schema.Name)
	}
}

func TestAgentTool_Execute_MissingPrompt(t *testing.T) {
	tool := &AgentTool{}
	_, err := tool.Execute(`{"description": "test"}`)
	if err == nil {
		t.Error("expected error for missing prompt")
	}
}

func TestAgentTool_Execute_MissingDescription(t *testing.T) {
	tool := &AgentTool{}
	_, err := tool.Execute(`{"prompt": "test"}`)
	if err == nil {
		t.Error("expected error for missing description")
	}
}

func TestAgentTool_Execute_NoPool(t *testing.T) {
	tool := &AgentTool{}
	_, err := tool.Execute(`{"prompt": "test", "description": "test task"}`)
	if err == nil {
		t.Error("expected error when pool is nil")
	}
}

func TestAgentTool_agentConfig_UsesModeResolver(t *testing.T) {
	tool := &AgentTool{ModeResolver: &fakeModeResolver{body: "coder mode body"}}
	cfg := tool.agentConfig("coder")
	if cfg.SystemPrompt != "coder mode body" {
		t.Errorf("SystemPrompt = %q, want %q", cfg.SystemPrompt, "coder mode body")
	}
	if cfg.Temperature != 0.2 {
		t.Errorf("Temperature = %f, want 0.2", cfg.Temperature)
	}
	if len(cfg.AllowedTools) != 2 {
		t.Errorf("AllowedTools = %v", cfg.AllowedTools)
	}
}

func TestAgentTool_agentConfig_NoResolver(t *testing.T) {
	tool := &AgentTool{}
	cfg := tool.agentConfig("coder")
	if cfg.SystemPrompt != "" {
		t.Error("expected empty SystemPrompt when resolver is nil")
	}
}

func TestSubagentMajorMode(t *testing.T) {
	cases := map[string]string{
		"plan":    "planner",
		"explore": "reviewer",
		"coder":   "coder",
		"":        "coder",
		"bogus":   "coder", // unknown falls back to coder
	}
	for in, want := range cases {
		if got := SubagentMajorMode(in); got != want {
			t.Errorf("SubagentMajorMode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAgentTool_Resume_NoTaskBus(t *testing.T) {
	tool := &AgentTool{}
	_, err := tool.Execute(`{"prompt": "x", "description": "y", "resume": "task-1"}`)
	if err == nil {
		t.Error("expected error when resuming without task bus")
	}
}

func TestAgentTool_Resume_TaskNotFound(t *testing.T) {
	bus := tasks.NewBus(tasks.NopStore{}, nil)
	tool := &AgentTool{TaskBus: bus}
	_, err := tool.Execute(`{"prompt": "x", "description": "y", "resume": "task-1"}`)
	if err == nil {
		t.Error("expected error for unknown task")
	}
}

func TestAgentTool_Resume_SurfacesStoredResult(t *testing.T) {
	bus := tasks.NewBus(tasks.NopStore{}, nil)
	bus.Register("task-done", "agent", "coder-task-done", "done task")
	bus.Start("task-done")
	bus.Complete("task-done", "final result body")

	tool := &AgentTool{TaskBus: bus}
	out, err := tool.Execute(`{"prompt": "x", "description": "y", "resume": "task-done"}`)
	if err != nil {
		t.Fatalf("resume completed task: %v", err)
	}
	if !strings.Contains(out, "completed") || !strings.Contains(out, "final result body") {
		t.Fatalf("resume should surface stored result, got: %q", out)
	}
}

func TestAgentTool_Resume_SurfacesStoredError(t *testing.T) {
	bus := tasks.NewBus(tasks.NopStore{}, nil)
	bus.Register("task-fail", "agent", "coder-task-fail", "fail task")
	bus.Start("task-fail")
	bus.Fail("task-fail", "boom")

	tool := &AgentTool{TaskBus: bus}
	_, err := tool.Execute(`{"prompt": "x", "description": "y", "resume": "task-fail"}`)
	if err == nil {
		t.Fatal("expected error when resuming a failed task")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error should include stored cause, got: %v", err)
	}
}

func TestAgentTool_Resume_RunningIsHonest(t *testing.T) {
	bus := tasks.NewBus(tasks.NopStore{}, nil)
	bus.Register("task-run", "agent", "coder-task-run", "run task")
	bus.Start("task-run")

	tool := &AgentTool{TaskBus: bus}
	out, err := tool.Execute(`{"prompt": "x", "description": "y", "resume": "task-run"}`)
	if err != nil {
		t.Fatalf("resume running task: %v", err)
	}
	if !strings.Contains(out, "still running") {
		t.Fatalf("resume of in-flight task must say 'still running', got: %q", out)
	}
}

type fakeOrchestrator struct {
	mu       sync.Mutex
	messages []string
}

func (o *fakeOrchestrator) Emit(from, to, content string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.messages = append(o.messages, content)
}

func TestAgentTool_orchestrator_Fallback(t *testing.T) {
	tool := &AgentTool{}
	if tool.orchestrator() != nil {
		t.Error("expected nil orchestrator when no pool or field set")
	}
	var m provider.Model
	pool := NewAgentPool(m, provider.StreamOptions{}, nil)
	tool2 := &AgentTool{Pool: pool}
	if tool2.orchestrator() != nil {
		t.Error("expected nil orchestrator when pool has no orchestrator")
	}
	orch := &ForegroundOrchestrator{}
	pool.SetOrchestrator(orch)
	if tool2.orchestrator() == nil {
		t.Error("expected orchestrator from pool")
	}
	tool3 := &AgentTool{Orchestrator: &fakeOrchestrator{}}
	if tool3.orchestrator() == nil {
		t.Error("expected explicit orchestrator")
	}
}

func TestAgentTool_Execute_EmitsOrchestratorLifecycle(t *testing.T) {
	fake := &fakeOrchestrator{}
	tool := &AgentTool{Orchestrator: fake}
	// Missing prompt should fail before emitting any lifecycle message.
	_, _ = tool.Execute(`{"description": "no prompt"}`)
	fake.mu.Lock()
	if len(fake.messages) != 0 {
		t.Errorf("expected no lifecycle messages for invalid input, got %v", fake.messages)
	}
	fake.mu.Unlock()

	// With a pool but no live model, RunAndCollect fails. Lifecycle messages
	// should still be emitted around the attempt.
	var m provider.Model
	pool := NewAgentPool(m, provider.StreamOptions{}, nil)
	tool2 := &AgentTool{Pool: pool, Orchestrator: fake, CurrentMode: func() internal.ModeState { return internal.ModeState{} }}
	_, _ = tool2.Execute(`{"prompt": "test", "description": "test task"}`)
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.messages) == 0 {
		t.Fatal("expected orchestrator lifecycle messages to be emitted")
	}
	foundStart := false
	foundEnd := false
	for _, msg := range fake.messages {
		if strings.Contains(msg, "started") {
			foundStart = true
		}
		if strings.Contains(msg, "completed") || strings.Contains(msg, "failed") {
			foundEnd = true
		}
	}
	if !foundStart {
		t.Errorf("expected a 'started' message, got %v", fake.messages)
	}
	if !foundEnd {
		t.Errorf("expected a completion/failure message, got %v", fake.messages)
	}
}
