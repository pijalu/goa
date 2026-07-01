// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package multiagent

import (
	"context"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

func TestPipelineRunner_SetAgentPool(t *testing.T) {
	r := NewPipelineRunner()
	pool := NewAgentPool(testModel("test-model"), provider.StreamOptions{}, nil)
	r.SetAgentPool(pool)
	if r.pool != pool {
		t.Error("SetAgentPool did not store pool reference")
	}
}

func TestPipelineRunner_ExecuteStage_NoPool_ReturnsError(t *testing.T) {
	r := NewPipelineRunner()
	stage := PipelineStage{ID: "test", Name: "Test", Agent: "coder", Prompt: "do something"}

	err := r.executeStage(context.Background(), stage)
	if err == nil {
		t.Fatal("expected error when no agent pool set")
	}
}

func TestPipelineRunner_ExecuteStage_WithPool_CallsAgentRun(t *testing.T) {
	r := NewPipelineRunner()
	pool := NewAgentPool(testModel("test-model"), provider.StreamOptions{}, nil)
	r.SetAgentPool(pool)

	stage := PipelineStage{ID: "test_stage", Name: "Test Stage", Agent: "coder", Prompt: "do something"}

	// Should succeed because mockProvider returns immediately
	err := r.executeStage(context.Background(), stage)
	if err != nil {
		t.Fatalf("executeStage failed: %v", err)
	}
}

func TestPipelineRunner_ExecuteStage_EmptyPrompt(t *testing.T) {
	r := NewPipelineRunner()
	pool := NewAgentPool(testModel("test-model"), provider.StreamOptions{}, nil)
	r.SetAgentPool(pool)

	stage := PipelineStage{ID: "empty", Name: "Empty Prompt Stage", Agent: "reviewer", Prompt: ""}

	err := r.executeStage(context.Background(), stage)
	if err != nil {
		t.Fatalf("executeStage with empty prompt: %v", err)
	}
}

func TestPipelineRunner_Run_CompletesAllStages(t *testing.T) {
	r := NewPipelineRunner()
	pool := NewAgentPool(testModel("test-model"), provider.StreamOptions{}, nil)
	r.SetAgentPool(pool)

	pipeline := &Pipeline{
		ID:   "test-pipeline",
		Name: "Test Pipeline",
		Stages: []PipelineStage{
			{ID: "s1", Name: "Stage 1", Agent: "coder", Prompt: "step 1"},
			{ID: "s2", Name: "Stage 2", Agent: "reviewer", Prompt: "step 2"},
		},
	}

	run := NewPipelineRun(pipeline)
	err := r.Run(run)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if run.Status != PipelineCompleted {
		t.Errorf("expected PipelineCompleted, got %v", run.Status)
	}
}

func TestPipelineRunner_Run_FailsOnStageError(t *testing.T) {
	r := NewPipelineRunner()
	// No pool set — should fail on first stage

	pipeline := &Pipeline{
		ID:   "failing-pipeline",
		Name: "Failing Pipeline",
		Stages: []PipelineStage{
			{ID: "s1", Name: "Stage 1", Agent: "coder", Prompt: "step 1"},
		},
	}

	run := NewPipelineRun(pipeline)
	err := r.Run(run)
	if err == nil {
		t.Fatal("expected error when no agent pool set")
	}
	if run.Status != PipelineFailed {
		t.Errorf("expected PipelineFailed, got %v", run.Status)
	}
}
