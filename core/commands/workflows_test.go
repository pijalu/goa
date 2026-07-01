// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/multiagent"
	"github.com/pijalu/goa/prompts"
)

func TestWorkflowsCommand_Name(t *testing.T) {
	c := &WorkflowsCommand{}
	if c.Name() != "workflows" {
		t.Errorf("expected name 'workflows', got %q", c.Name())
	}
}

func TestWorkflowsCommand_Aliases(t *testing.T) {
	c := &WorkflowsCommand{}
	aliases := c.Aliases()
	if len(aliases) != 0 {
		t.Errorf("expected no aliases (/w was removed), got %v", aliases)
	}
}

func TestWorkflowsCommand_ShortHelp(t *testing.T) {
	c := &WorkflowsCommand{}
	if c.ShortHelp() == "" {
		t.Error("ShortHelp should not be empty")
	}
}

func TestWorkflowsCommand_LongHelp(t *testing.T) {
	c := &WorkflowsCommand{}
	if c.LongHelp() == "" {
		t.Error("LongHelp should not be empty")
	}
}

func TestWorkflowsCommand_List_NoRegistry(t *testing.T) {
	c := &WorkflowsCommand{}
	ctx := core.Context{}

	err := c.Run(ctx, []string{"list"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWorkflowsCommand_List_Empty(t *testing.T) {
	fs := prompts.EmbeddedFS()
	pr := prompts.NewRegistry(fs, "", "")
	wr := multiagent.NewWorkflowRegistry(pr)
	c := &WorkflowsCommand{}
	ctx := core.Context{WorkflowRegistry: wr}

	var buf strings.Builder
	ctx.OutputBuffer = &buf

	err := c.Run(ctx, []string{"list"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No workflows") {
		t.Errorf("expected 'No workflows' in output, got %q", buf.String())
	}
}

func TestWorkflowsCommand_List_WithWorkflows(t *testing.T) {
	fs := prompts.EmbeddedFS()
	pr := prompts.NewRegistry(fs, "", "")
	wr := multiagent.NewWorkflowRegistry(pr)
	wr.Register(multiagent.Pipeline{
		ID: "test", Name: "Test Workflow",
		Stages: []multiagent.PipelineStage{
			{ID: "s1", Agent: "coder", Prompt: "do it"},
		},
	})
	c := &WorkflowsCommand{}
	ctx := core.Context{WorkflowRegistry: wr}

	var buf strings.Builder
	ctx.OutputBuffer = &buf

	err := c.Run(ctx, []string{"list"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "Test Workflow") {
		t.Errorf("expected workflow name in output, got %q", buf.String())
	}
}

func TestWorkflowsCommand_Show_Missing(t *testing.T) {
	fs := prompts.EmbeddedFS()
	pr := prompts.NewRegistry(fs, "", "")
	wr := multiagent.NewWorkflowRegistry(pr)
	c := &WorkflowsCommand{}
	ctx := core.Context{WorkflowRegistry: wr}

	var buf strings.Builder
	ctx.OutputBuffer = &buf

	err := c.Run(ctx, []string{"show", "missing"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "not found") {
		t.Errorf("expected 'not found' in output, got %q", buf.String())
	}
}

func TestWorkflowsCommand_Show_Found(t *testing.T) {
	fs := prompts.EmbeddedFS()
	pr := prompts.NewRegistry(fs, "", "")
	wr := multiagent.NewWorkflowRegistry(pr)
	wr.Register(multiagent.Pipeline{
		ID: "test", Name: "Test",
		Description: "desc",
		Stages: []multiagent.PipelineStage{
			{ID: "s1", Name: "Stage 1", Agent: "coder", Prompt: "do it"},
		},
	})
	c := &WorkflowsCommand{}
	ctx := core.Context{WorkflowRegistry: wr}

	var buf strings.Builder
	ctx.OutputBuffer = &buf

	err := c.Run(ctx, []string{"show", "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "Test") {
		t.Errorf("expected 'Test' in output, got %q", buf.String())
	}
}

func TestWorkflowsCommand_Show_NoArgs(t *testing.T) {
	c := &WorkflowsCommand{}
	ctx := core.Context{}

	err := c.Run(ctx, []string{"show"})
	if err == nil {
		t.Error("expected error with missing name")
	}
}

func TestWorkflowsCommand_Run_NoArgs(t *testing.T) {
	c := &WorkflowsCommand{}
	ctx := core.Context{}

	err := c.Run(ctx, []string{"run"})
	if err == nil {
		t.Error("expected error with missing workflow name")
	}
}

func TestWorkflowsCommand_Run_NoOrchestrator(t *testing.T) {
	fs := prompts.EmbeddedFS()
	pr := prompts.NewRegistry(fs, "", "")
	wr := multiagent.NewWorkflowRegistry(pr)
	wr.Register(multiagent.Pipeline{
		ID: "test", Name: "Test",
		Stages: []multiagent.PipelineStage{
			{ID: "s1", Agent: "coder", Prompt: "do it"},
		},
	})
	c := &WorkflowsCommand{}
	ctx := core.Context{WorkflowRegistry: wr}

	var buf strings.Builder
	ctx.OutputBuffer = &buf

	err := c.Run(ctx, []string{"run", "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "Orchestrator not available") {
		t.Errorf("expected orchestrator error, got %q", buf.String())
	}
}

func TestWorkflowsCommand_Run_MissingWorkflow(t *testing.T) {
	fs := prompts.EmbeddedFS()
	pr := prompts.NewRegistry(fs, "", "")
	wr := multiagent.NewWorkflowRegistry(pr)
	c := &WorkflowsCommand{}
	pool := multiagent.NewAgentPool(testCmdModel(), provider.StreamOptions{}, nil)
	orch := multiagent.NewForegroundOrchestrator(pool)
	ctx := core.Context{WorkflowRegistry: wr, ForegroundOrchestrator: orch}

	var buf strings.Builder
	ctx.OutputBuffer = &buf

	err := c.Run(ctx, []string{"run", "missing"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "not found") {
		t.Errorf("expected 'not found' in output, got %q", buf.String())
	}
}

func TestWorkflowsCommand_Run_Success(t *testing.T) {
	fs := prompts.EmbeddedFS()
	pr := prompts.NewRegistry(fs, "", "")
	wr := multiagent.NewWorkflowRegistry(pr)
	wr.Register(multiagent.Pipeline{
		ID: "test", Name: "Test",
		Stages: []multiagent.PipelineStage{
			{ID: "s1", Name: "Stage 1", Agent: "coder", Prompt: "do it"},
		},
	})
	c := &WorkflowsCommand{}
	pool := multiagent.NewAgentPool(testCmdModel(), provider.StreamOptions{}, nil)
	orch := multiagent.NewForegroundOrchestrator(pool)
	ctx := core.Context{
		WorkflowRegistry:       wr,
		ForegroundOrchestrator: orch,
	}

	var buf strings.Builder
	ctx.OutputBuffer = &buf

	err := c.Run(ctx, []string{"run", "test", "input"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "Test") {
		t.Errorf("expected 'Test' in output, got %q", buf.String())
	}
}

func TestWorkflowsCommand_BareNameIsRunShorthand(t *testing.T) {
	// Bare words that aren't a known subcommand are treated as workflow name shorthand
	c := &WorkflowsCommand{}
	ctx := core.Context{}

	// Should not error — will try to run a workflow named "unknown"
	err := c.Run(ctx, []string{"unknown"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWorkflowsCommand_CompleteArgs(t *testing.T) {
	c := &WorkflowsCommand{}
	ctx := core.Context{}

	comps := c.CompleteArgs(ctx, "")
	if len(comps) != 3 {
		t.Fatalf("expected 3 completions, got %d", len(comps))
	}

	// Test filtering
	comps = c.CompleteArgs(ctx, "li")
	if len(comps) != 1 || comps[0].Value != "list" {
		t.Errorf("expected ['list'], got %v", comps)
	}
}

func TestWorkflowsCommand_CompleteArgs_Run(t *testing.T) {
	fs := prompts.EmbeddedFS()
	pr := prompts.NewRegistry(fs, "", "")
	wr := multiagent.NewWorkflowRegistry(pr)
	wr.Register(multiagent.Pipeline{
		ID: "implement-feature", Name: "Implement Feature",
		Stages: []multiagent.PipelineStage{{ID: "s1", Agent: "coder", Prompt: "do it"}},
	})
	wr.Register(multiagent.Pipeline{
		ID: "review-changes", Name: "Review Changes",
		Stages: []multiagent.PipelineStage{{ID: "s1", Agent: "reviewer", Prompt: "review"}},
	})

	c := &WorkflowsCommand{}
	ctx := core.Context{WorkflowRegistry: wr}

	// Test run:<prefix> completion
	comps := c.CompleteArgs(ctx, "run:")
	if len(comps) != 2 {
		t.Fatalf("expected 2 run completions, got %d: %v", len(comps), comps)
	}
	hasImplement := false
	hasReview := false
	for _, comp := range comps {
		if comp.Value == "run:implement-feature" {
			hasImplement = true
		}
		if comp.Value == "run:review-changes" {
			hasReview = true
		}
	}
	if !hasImplement {
		t.Error("expected 'run:implement-feature' in completions")
	}
	if !hasReview {
		t.Error("expected 'run:review-changes' in completions")
	}

	// Test filtered completion
	comps = c.CompleteArgs(ctx, "run:implement")
	if len(comps) != 1 || comps[0].Value != "run:implement-feature" {
		t.Errorf("expected 1 filtered completion, got %v", comps)
	}
}

func TestWorkflowsCommand_Run_WithColonSyntax(t *testing.T) {
	fs := prompts.EmbeddedFS()
	pr := prompts.NewRegistry(fs, "", "")
	wr := multiagent.NewWorkflowRegistry(pr)
	wr.Register(multiagent.Pipeline{
		ID: "test", Name: "Test",
		Stages: []multiagent.PipelineStage{{ID: "s1", Agent: "coder", Prompt: "do it"}},
	})

	c := &WorkflowsCommand{}
	pool := multiagent.NewAgentPool(testCmdModel(), provider.StreamOptions{}, nil)
	orch := multiagent.NewForegroundOrchestrator(pool)
	ctx := core.Context{
		WorkflowRegistry:       wr,
		ForegroundOrchestrator: orch,
	}

	var buf strings.Builder
	ctx.OutputBuffer = &buf

	err := c.Run(ctx, []string{"run:test", "input"})
	if err != nil {
		t.Fatalf("Run with colon syntax: %v", err)
	}
	if !strings.Contains(buf.String(), "Test") {
		t.Errorf("expected workflow name in output, got %q", buf.String())
	}
}

func TestWorkflowsCommand_Run_WithColonSyntax_ShorthandOnly(t *testing.T) {
	fs := prompts.EmbeddedFS()
	pr := prompts.NewRegistry(fs, "", "")
	wr := multiagent.NewWorkflowRegistry(pr)
	wr.Register(multiagent.Pipeline{
		ID: "test", Name: "Test",
		Stages: []multiagent.PipelineStage{{ID: "s1", Agent: "coder", Prompt: "do it"}},
	})

	c := &WorkflowsCommand{}
	pool := multiagent.NewAgentPool(testCmdModel(), provider.StreamOptions{}, nil)
	orch := multiagent.NewForegroundOrchestrator(pool)
	ctx := core.Context{
		WorkflowRegistry:       wr,
		ForegroundOrchestrator: orch,
	}

	var buf strings.Builder
	ctx.OutputBuffer = &buf

	err := c.Run(ctx, []string{":test", "input"})
	if err != nil {
		t.Fatalf("Run with :name shorthand: %v", err)
	}
	if !strings.Contains(buf.String(), "Test") {
		t.Errorf("expected workflow name in output, got %q", buf.String())
	}
}

func TestWorkflowsCommand_CompleteArgs_WithWorkflows(t *testing.T) {
	fs := prompts.EmbeddedFS()
	pr := prompts.NewRegistry(fs, "", "")
	wr := multiagent.NewWorkflowRegistry(pr)
	wr.Register(multiagent.Pipeline{
		ID: "implement-feature", Name: "Implement Feature",
		Stages: []multiagent.PipelineStage{{ID: "s1", Agent: "coder", Prompt: "do it"}},
	})
	wr.Register(multiagent.Pipeline{
		ID: "review-changes", Name: "Review Changes",
		Stages: []multiagent.PipelineStage{{ID: "s1", Agent: "reviewer", Prompt: "review"}},
	})

	c := &WorkflowsCommand{}
	ctx := core.Context{WorkflowRegistry: wr}

	comps := c.CompleteArgs(ctx, "")
	if len(comps) != 3 {
		t.Fatalf("expected 3 subcommand completions, got %d", len(comps))
	}
}

func TestWorkflowsCommand_List_Formatting(t *testing.T) {
	fs := prompts.EmbeddedFS()
	pr := prompts.NewRegistry(fs, "", "")
	wr := multiagent.NewWorkflowRegistry(pr)
	wr.Register(multiagent.Pipeline{
		ID: "test", Name: "Test Workflow", Description: "A test workflow",
		Stages: []multiagent.PipelineStage{
			{ID: "s1", Name: "Plan", Agent: "planner", Prompt: "plan"},
			{ID: "s2", Name: "Code", Agent: "coder", Prompt: "code"},
		},
	})

	c := &WorkflowsCommand{}
	ctx := core.Context{WorkflowRegistry: wr}

	var buf strings.Builder
	ctx.OutputBuffer = &buf

	err := c.Run(ctx, []string{"list"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Test Workflow") {
		t.Errorf("expected 'Test Workflow' in output, got %q", output)
	}
	if !strings.Contains(output, "Plan → Code") {
		t.Errorf("expected stage arrow format, got %q", output)
	}
	if !strings.Contains(output, "/workflows:run:test") {
		t.Errorf("expected runnable ID /workflows:run:test, got %q", output)
	}
}
