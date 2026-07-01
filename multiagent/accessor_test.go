// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package multiagent

import (
	"context"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

func TestForegroundOrchestrator_Context(t *testing.T) {
	pool := NewAgentPool(testModel("test-model"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)
	if ctx := orch.Context(); ctx == nil {
		t.Fatal("Context() returned nil")
	}
}

func TestForegroundOrchestrator_ActiveRun(t *testing.T) {
	pool := NewAgentPool(testModel("test-model"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)
	if run := orch.ActiveRun(); run != nil {
		t.Error("expected nil ActiveRun initially")
	}
}

func TestForegroundOrchestrator_ActivePipeline(t *testing.T) {
	pool := NewAgentPool(testModel("test-model"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)
	if p := orch.ActivePipeline(); p != nil {
		t.Error("expected nil ActivePipeline initially")
	}
}

func TestForegroundOrchestrator_AccumulatedContext(t *testing.T) {
	pool := NewAgentPool(testModel("test-model"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)
	if ctx := orch.AccumulatedContext(); ctx != "" {
		t.Errorf("expected empty, got %q", ctx)
	}
}

func TestForegroundOrchestrator_StageToolCount_Init(t *testing.T) {
	pool := NewAgentPool(testModel("test-model"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)
	if n := orch.StageToolCount(); n != 0 {
		t.Errorf("expected 0, got %d", n)
	}
}

func TestForegroundOrchestrator_SetStageToolCount(t *testing.T) {
	pool := NewAgentPool(testModel("test-model"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)
	orch.SetStageToolCount(42)
	if n := orch.StageToolCount(); n != 42 {
		t.Errorf("expected 42, got %d", n)
	}
}

func TestForegroundOrchestrator_ModeLabel(t *testing.T) {
	pool := NewAgentPool(testModel("test-model"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)
	orch.SetMode(WorkflowCompanionMinor)
	if label := orch.ModeLabel(); label == "" {
		t.Error("expected non-empty ModeLabel")
	}
}

func TestForegroundOrchestrator_SetOutputHandler(t *testing.T) {
	pool := NewAgentPool(testModel("test-model"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)
	orch.SetOutputHandler(func(agentName, text string) {})
}

func TestForegroundOrchestrator_SetPromptRegistry(t *testing.T) {
	pool := NewAgentPool(testModel("test-model"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)
	orch.SetPromptRegistry(nil)
}

func TestForegroundOrchestrator_Cancel_Idle(t *testing.T) {
	pool := NewAgentPool(testModel("test-model"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)
	orch.Cancel()
}

func TestForegroundOrchestrator_StartRunContext_CancelsPrevious(t *testing.T) {
	pool := NewAgentPool(testModel("test-model"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)
	ctx1 := orch.startRunContext(context.Background())
	select {
	case <-ctx1.Done():
		t.Fatal("ctx1 should not be cancelled yet")
	default:
	}
	ctx2 := orch.startRunContext(context.Background())
	select {
	case <-ctx1.Done():
	default:
		t.Error("expected ctx1 to be cancelled after new startRunContext")
	}
	_ = ctx2
}

func TestAgentPool_SetOrchestrator(t *testing.T) {
	pool := NewAgentPool(testModel("test-model"), provider.StreamOptions{}, nil)
	pool.SetOrchestrator(nil)
}

func TestAgentPool_SetAgentBus(t *testing.T) {
	pool := NewAgentPool(testModel("test-model"), provider.StreamOptions{}, nil)
	pool.SetAgentBus(nil)
}

func TestPipelineRunner_Events(t *testing.T) {
	runner := NewPipelineRunner()
	if ch := runner.Events(); ch == nil {
		t.Fatal("Events() returned nil channel")
	}
}
