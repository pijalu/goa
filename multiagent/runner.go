// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package multiagent

import (
	"context"
	"fmt"
	"sync"
)

// PipelineRunner executes pipeline stages sequentially, honouring approval
// gates between stages. It is the lower-level engine used by the /pipeline
// command; workflow users typically go through ForegroundOrchestrator.RunWorkflow
// instead.
type PipelineRunner struct {
	mu     sync.Mutex
	events chan PipelineEvent
	pool   *AgentPool
}

// SetAgentPool sets the agent pool for executing pipeline stages.
func (r *PipelineRunner) SetAgentPool(pool *AgentPool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pool = pool
}

// PipelineEvent is emitted when pipeline state changes.
type PipelineEvent struct {
	PipelineID string
	StageID    string
	Status     string
	Error      error
}

// NewPipelineRunner creates a pipeline runner.
func NewPipelineRunner() *PipelineRunner {
	return &PipelineRunner{
		events: make(chan PipelineEvent, 100),
	}
}

// Events returns a channel of pipeline events.
func (r *PipelineRunner) Events() <-chan PipelineEvent {
	return r.events
}

// Run executes all stages of a pipeline sequentially. It preserves the
// original signature for backward compatibility with existing callers (e.g.
// the /pipeline command) and uses the run's own context for cancellation.
// Callers that own a cancellation context should prefer RunWithContext.
func (r *PipelineRunner) Run(run *PipelineRun) error {
	run.mu.Lock()
	ctx := run.ctx
	run.mu.Unlock()
	return r.RunWithContext(ctx, run)
}

// RunWithContext executes all stages of a pipeline sequentially, honouring the
// caller's context for mid-stage cancellation (AP-07). When a stage has a gate
// with RequireApproval, the runner emits StagePaused, sets the run to
// PipelinePending, and BLOCKS until PipelineRun.Resume is called or ctx is
// cancelled — it no longer falls through silently (STUB-02). A "skip" or
// cancelled decision aborts the run.
func (r *PipelineRunner) RunWithContext(ctx context.Context, run *PipelineRun) error {
	run.mu.Lock()
	run.Status = PipelineRunning
	run.mu.Unlock()

	for i, stage := range run.Pipeline.Stages {
		if err := ctx.Err(); err != nil {
			r.failRun(run, stage, err)
			return err
		}

		run.mu.Lock()
		run.Current = i
		run.Stages[stage.ID] = StageRunning
		run.mu.Unlock()

		r.emit(PipelineEvent{
			PipelineID: run.Pipeline.ID,
			StageID:    stage.ID,
			Status:     string(StageRunning),
		})

		if err := r.executeStage(ctx, stage); err != nil {
			r.failRun(run, stage, err)
			return err
		}

		run.mu.Lock()
		run.Stages[stage.ID] = StageCompleted
		run.mu.Unlock()

		r.emit(PipelineEvent{
			PipelineID: run.Pipeline.ID,
			StageID:    stage.ID,
			Status:     string(StageCompleted),
		})

		// Handle gate: block for approval instead of falling through (STUB-02).
		if stage.Gate.RequireApproval {
			run.mu.Lock()
			run.Status = PipelinePending // paused at gate
			run.mu.Unlock()

			r.emit(PipelineEvent{
				PipelineID: run.Pipeline.ID,
				StageID:    stage.ID,
				Status:     string(StagePaused),
			})

			if err := run.waitGateApproval(); err != nil {
				r.failRun(run, stage, err)
				return err
			}
		}
	}

	run.mu.Lock()
	run.Status = PipelineCompleted
	run.mu.Unlock()

	r.emit(PipelineEvent{
		PipelineID: run.Pipeline.ID,
		Status:     string(PipelineCompleted),
	})
	return nil
}

// failRun marks the run and stage as failed and emits the failure event.
func (r *PipelineRunner) failRun(run *PipelineRun, stage PipelineStage, err error) {
	run.mu.Lock()
	run.Status = PipelineFailed
	run.Stages[stage.ID] = StageFailed
	run.Err = err
	run.mu.Unlock()

	r.emit(PipelineEvent{
		PipelineID: run.Pipeline.ID,
		StageID:    stage.ID,
		Status:     string(PipelineFailed),
		Error:      err,
	})
}

func (r *PipelineRunner) agentPool() *AgentPool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pool
}

// executeStage runs a single stage's agent. It is no longer flagged
// "simplified": it forwards ctx so the stage is cancellable mid-run (AP-07).
func (r *PipelineRunner) executeStage(ctx context.Context, stage PipelineStage) error {
	pool := r.agentPool()
	if pool == nil {
		return fmt.Errorf("pipeline runner has no agent pool: cannot execute stage %q", stage.ID)
	}

	agent, err := pool.GetOrCreate(stage.Agent)
	if err != nil {
		return fmt.Errorf("create agent for stage %q: %w", stage.ID, err)
	}

	prompt := stage.Prompt
	if prompt == "" {
		prompt = "Execute the pipeline stage: " + stage.Name
	}

	return agent.Run(ctx, prompt)
}

func (r *PipelineRunner) emit(event PipelineEvent) {
	select {
	case r.events <- event:
	default:
	}
}
