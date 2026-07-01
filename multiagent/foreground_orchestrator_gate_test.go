// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package multiagent

import (
	"context"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

func TestForegroundOrchestrator_GateApproval_Approve(t *testing.T) {
	pool := NewAgentPool(testModel("test-model"), provider.StreamOptions{}, nil)
	o := NewForegroundOrchestrator(pool)

	go func() {
		time.Sleep(10 * time.Millisecond)
		o.SubmitGateDecision(GateDecision{Action: "approve"})
	}()

	decision := o.waitForGateApproval()
	if decision != "approve" {
		t.Errorf("expected 'approve', got %q", decision)
	}
}

func TestForegroundOrchestrator_GateApproval_Skip(t *testing.T) {
	pool := NewAgentPool(testModel("test-model"), provider.StreamOptions{}, nil)
	o := NewForegroundOrchestrator(pool)

	go func() {
		time.Sleep(10 * time.Millisecond)
		o.SubmitGateDecision(GateDecision{Action: "skip"})
	}()

	decision := o.waitForGateApproval()
	if decision != "skip" {
		t.Errorf("expected 'skip', got %q", decision)
	}
}

func TestForegroundOrchestrator_Stop_UnblocksGate(t *testing.T) {
	pool := NewAgentPool(testModel("test-model"), provider.StreamOptions{}, nil)
	o := NewForegroundOrchestrator(pool)

	go func() {
		time.Sleep(10 * time.Millisecond)
		o.Stop()
	}()

	// Stop should unblock the gate with "skip"
	decision := o.waitForGateApproval()
	if decision != "skip" {
		t.Errorf("expected 'skip' after stop, got %q", decision)
	}
}

func TestForegroundOrchestrator_Progress_Initial(t *testing.T) {
	pool := NewAgentPool(testModel("test-model"), provider.StreamOptions{}, nil)
	o := NewForegroundOrchestrator(pool)

	p := o.Progress()
	if p.Status != "" {
		t.Errorf("expected empty status, got %q", p.Status)
	}
}

func TestForegroundOrchestrator_handleStageGate_NoGate(t *testing.T) {
	pool := NewAgentPool(testModel("test-model"), provider.StreamOptions{}, nil)
	o := NewForegroundOrchestrator(pool)

	stage := PipelineStage{ID: "s1", Name: "Test", Agent: "coder", Prompt: "x"}
	// Should not block or emit anything for non-gated stage
	o.handleStageGate(stage)

	select {
	case <-o.Events():
		t.Error("expected no events for non-gated stage")
	default:
	}
}

func TestForegroundOrchestrator_RunWorkflow_NotFound(t *testing.T) {
	pool := NewAgentPool(testModel("test-model"), provider.StreamOptions{}, nil)
	o := NewForegroundOrchestrator(pool)
	reg := NewWorkflowRegistry(nil)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := o.RunWorkflow(ctx, reg, "missing", "input")
	if err == nil {
		t.Error("expected error for missing workflow")
	}
}
