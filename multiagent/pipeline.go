// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package multiagent

import (
	"context"
	"fmt"
	"sync"
)

// StageStatus represents the current state of a pipeline stage.
type StageStatus string

const (
	StagePending   StageStatus = "pending"
	StageRunning   StageStatus = "running"
	StageCompleted StageStatus = "completed"
	StageFailed    StageStatus = "failed"
	StagePaused    StageStatus = "paused"
)

// PipelineStatus represents the current state of the entire pipeline.
type PipelineStatus string

const (
	PipelinePending   PipelineStatus = "pending"
	PipelineRunning   PipelineStatus = "running"
	PipelineCompleted PipelineStatus = "completed"
	PipelineFailed    PipelineStatus = "failed"
	PipelineCancelled PipelineStatus = "cancelled"
)

// GateConfig controls approval gates between pipeline stages.
type GateConfig struct {
	RequireApproval bool   `yaml:"require_approval"`
	Prompt          string `yaml:"prompt"`
}

// LoopConfig controls iteration limits for repeating stages.
type LoopConfig struct {
	MaxIterations int  `yaml:"max_iterations"`
	UntilApproved bool `yaml:"until_approved"`
}

// PipelineStage defines a single stage in a multi-agent pipeline.
type PipelineStage struct {
	ID          string     `yaml:"id"`
	Name        string     `yaml:"name"`
	Agent       string     `yaml:"agent"` // "coder", "planner", "reviewer"
	Prompt      string     `yaml:"prompt"`
	Gate        GateConfig `yaml:"gate,omitempty"`
	Loop        LoopConfig `yaml:"loop,omitempty"`
	Tools       []string   `yaml:"tools,omitempty"`
	OutputFiles []string   `yaml:"output_files,omitempty"`
}

// Pipeline defines a multi-agent workflow.
type Pipeline struct {
	ID          string          `yaml:"id"`
	Name        string          `yaml:"name"`
	Description string          `yaml:"description"`
	Stages      []PipelineStage `yaml:"stages"`

	// Dir is the directory containing the workflow definition and prompts.
	// Set automatically by LoadWorkflowTree. Used by ResolvePromptWithDir
	// to resolve relative prompt paths.
	Dir string `yaml:"-"`
}

// PipelineRun tracks the execution state of a pipeline.
type PipelineRun struct {
	mu       sync.Mutex
	Pipeline *Pipeline
	Status   PipelineStatus
	Stages   map[string]StageStatus
	Current  int
	Err      error
	ctx      context.Context
	cancel   context.CancelFunc

	// gateCh carries an approval decision for the current gate. It is created
	// lazily (or in NewPipelineRun) and consumed by PipelineRunner when it
	// blocks at RequireApproval, so the runner no longer falls through
	// silently (STUB-02). Resume sends on it.
	gateCh chan bool
}

// NewPipelineRun creates a new pipeline execution.
func NewPipelineRun(pipeline *Pipeline) *PipelineRun {
	ctx, cancel := context.WithCancel(context.Background())
	run := &PipelineRun{
		Pipeline: pipeline,
		Status:   PipelinePending,
		Stages:   make(map[string]StageStatus),
		Current:  -1,
		ctx:      ctx,
		cancel:   cancel,
		gateCh:   make(chan bool, 1),
	}
	for _, stage := range pipeline.Stages {
		run.Stages[stage.ID] = StagePending
	}
	return run
}

// SetStatusForTest force-sets the run status. Test-only: used to arrange
// paused/completed states for command tests without running real stages.
func (r *PipelineRun) SetStatusForTest(status PipelineStatus) error {
	r.mu.Lock()
	r.Status = status
	r.mu.Unlock()
	return nil
}

// Cancel stops the pipeline execution.
func (r *PipelineRun) Cancel() {
	r.cancel()
	r.mu.Lock()
	r.Status = PipelineCancelled
	r.mu.Unlock()
}

// StatusSnapshot returns a thread-safe copy of the run's current state.
func (r *PipelineRun) StatusSnapshot() (PipelineStatus, int, map[string]StageStatus) {
	r.mu.Lock()
	defer r.mu.Unlock()
	stages := make(map[string]StageStatus, len(r.Stages))
	for id, s := range r.Stages {
		stages[id] = s
	}
	return r.Status, r.Current, stages
}

// NextStage advances to the next stage in the pipeline.
// Returns the stage and true if there are more stages, false if complete.
// Marks the previous stage as completed and the new stage as running.
func (r *PipelineRun) NextStage() (PipelineStage, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.Status == PipelineCancelled {
		return PipelineStage{}, false
	}

	nextIdx := r.Current + 1
	if nextIdx >= len(r.Pipeline.Stages) {
		r.Status = PipelineCompleted
		return PipelineStage{}, false
	}

	// Mark previous stage as completed
	if r.Current >= 0 {
		prevID := r.Pipeline.Stages[r.Current].ID
		r.Stages[prevID] = StageCompleted
	}

	// Advance to next stage
	r.Current = nextIdx
	nextStage := r.Pipeline.Stages[nextIdx]
	r.Stages[nextStage.ID] = StageRunning
	r.Status = PipelineRunning

	return nextStage, true
}

// CompleteStage marks a specific stage as completed.
func (r *PipelineRun) CompleteStage(stageID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.Stages[stageID]; ok {
		r.Stages[stageID] = StageCompleted
	}
}

// Resume submits an approval decision for the current gate. approved=true
// continues the pipeline; approved=false aborts it. It is safe to call from
// any goroutine and is a no-op if the run's context is already done. The
// decision is buffered so Resume may be called slightly before the runner
// reaches the gate.
func (r *PipelineRun) Resume(approved bool) bool {
	r.mu.Lock()
	ch := r.gateCh
	ctx := r.ctx
	r.mu.Unlock()
	if ch == nil {
		return false
	}
	select {
	case ch <- approved:
		return true
	case <-ctx.Done():
		return false
	default:
		return false
	}
}

// waitGateApproval blocks until Resume is called (returns nil) or the run's
// context is cancelled (returns the context error). A disapproval (approved
// == false) is treated as a cancellation.
func (r *PipelineRun) waitGateApproval() error {
	r.mu.Lock()
	ch := r.gateCh
	ctx := r.ctx
	r.mu.Unlock()
	select {
	case approved := <-ch:
		if approved {
			return nil
		}
		return fmt.Errorf("gate denied for stage")
	case <-ctx.Done():
		return ctx.Err()
	}
}
