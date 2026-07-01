// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package multiagent

import (
	"sync"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// TestForegroundOrchestrator_ModeConcurrent exercises concurrent reads and
// writes of the workflow mode. The race detector flags unprotected access.
func TestForegroundOrchestrator_ModeConcurrent(t *testing.T) {
	pool := NewAgentPool(testModel("test-model"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			orch.SetMode(WorkflowCompanionMinor)
		}()
		go func() {
			defer wg.Done()
			_ = orch.Mode()
			_ = orch.ModeLabel()
		}()
	}
	wg.Wait()
}

// TestForegroundOrchestrator_ActiveRunConcurrent exercises concurrent access
// to active-run state from workflow-like goroutines and status readers.
func TestForegroundOrchestrator_ActiveRunConcurrent(t *testing.T) {
	pool := NewAgentPool(testModel("test-model"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			orch.SuspendCompanion()
		}()
		go func() {
			defer wg.Done()
			orch.ResumeCompanion()
		}()
		go func() {
			defer wg.Done()
			_ = orch.ActiveRun()
			_ = orch.ActivePipeline()
			_ = orch.AccumulatedContext()
		}()
	}
	wg.Wait()
}

// TestForegroundOrchestrator_StageToolCountConcurrent exercises the atomic
// tool-count tracking used by WorkflowNextTool.
func TestForegroundOrchestrator_StageToolCountConcurrent(t *testing.T) {
	pool := NewAgentPool(testModel("test-model"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)

	orch.SetStageToolCount(0)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			orch.stageToolCount.Add(1)
		}()
	}
	wg.Wait()

	if got := orch.StageToolCount(); got != 100 {
		t.Errorf("StageToolCount = %d, want 100", got)
	}
}
