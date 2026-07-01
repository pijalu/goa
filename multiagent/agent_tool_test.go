// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package multiagent

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/core/tasks"
)

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
