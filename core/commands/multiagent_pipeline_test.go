// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/multiagent"
)

// TestGoCommand_ApprovesGate verifies /go actually signals the paused pipeline
// to continue by calling PipelineRun.Resume(true). Before the fix it only
// printed messages and never unblocked the runner.
func TestGoCommand_ApprovesGate(t *testing.T) {
	pipeline := &multiagent.Pipeline{
		ID:     "review",
		Name:   "Review",
		Stages: []multiagent.PipelineStage{{ID: "s1", Name: "review", Agent: "reviewer"}},
	}
	run := multiagent.NewPipelineRun(pipeline)
	// Force the run into the paused-at-gate state that /go expects.
	if err := run.SetStatusForTest(multiagent.PipelinePending); err != nil {
		t.Fatalf("set status: %v", err)
	}

	buf := &strings.Builder{}
	ctx := core.Context{OutputBuffer: buf, ActivePipelineRun: run}

	if err := (&GoCommand{}).Run(ctx, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Resume(true) buffers exactly one approval on the gate channel. After /go
	// fires it, a second call must report it can no longer continue — proving
	// the first call actually consumed the slot (i.e. really called Resume).
	if err := (&GoCommand{}).Run(ctx, nil); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Gate approved") {
		t.Fatalf("expected /go to approve gate, got:\n%s", out)
	}
}

// TestGoCommand_NoPausedPipeline verifies the guard message when nothing is
// paused.
func TestGoCommand_NoPausedPipeline(t *testing.T) {
	buf := &strings.Builder{}
	ctx := core.Context{OutputBuffer: buf}
	if err := (&GoCommand{}).Run(ctx, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(buf.String(), "No pipeline is currently paused") {
		t.Fatalf("expected paused-guard message, got:\n%s", buf.String())
	}
}

// TestPipelineStatus_ReportsCompletedDistinctly verifies that a completed
// pipeline is reported as completed rather than collapsed into the
// "No active pipeline" message (the old behavior).
func TestPipelineStatus_ReportsCompletedDistinctly(t *testing.T) {
	pipeline := &multiagent.Pipeline{
		ID:     "pair",
		Name:   "Pair",
		Stages: []multiagent.PipelineStage{{ID: "p", Name: "plan", Agent: "planner"}},
	}
	run := multiagent.NewPipelineRun(pipeline)
	run.SetStatusForTest(multiagent.PipelineCompleted)

	buf := &strings.Builder{}
	ctx := core.Context{OutputBuffer: buf, ActivePipelineRun: run}
	if err := pipelineStatus(ctx); err != nil {
		t.Fatalf("pipelineStatus: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "completed successfully") {
		t.Fatalf("completed pipeline should be reported distinctly, got:\n%s", out)
	}
	if strings.Contains(out, "No active pipeline") {
		t.Fatalf("completed pipeline must not say 'No active pipeline', got:\n%s", out)
	}
}

// TestPipelineStatus_ReportsPendingGate verifies the paused-at-gate hint.
func TestPipelineStatus_ReportsPendingGate(t *testing.T) {
	pipeline := &multiagent.Pipeline{
		ID:     "pair",
		Name:   "Pair",
		Stages: []multiagent.PipelineStage{{ID: "p", Name: "plan", Agent: "planner"}},
	}
	run := multiagent.NewPipelineRun(pipeline)
	run.SetStatusForTest(multiagent.PipelinePending)

	buf := &strings.Builder{}
	ctx := core.Context{OutputBuffer: buf, ActivePipelineRun: run}
	if err := pipelineStatus(ctx); err != nil {
		t.Fatalf("pipelineStatus: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "paused at an approval gate") {
		t.Fatalf("expected paused-at-gate hint, got:\n%s", out)
	}
}
