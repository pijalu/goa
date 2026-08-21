// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package multiagent

import (
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/provider"
	gorole "github.com/pijalu/goa/internal/role"
)

// TestDelegateTool_RelaysOutputForAllRoles is the regression test for team UI
// bug RC-3: previously only the companion role's output was relayed back to
// the main agent, so planner/coder delegate results vanished and the caller
// kept re-delegating the same question. Every role's final output must arrive
// on the main agent's bus inbox, labelled with the producing role.
func TestDelegateTool_RelaysOutputForAllRoles(t *testing.T) {
	bus := agentic.NewAgentBus()
	mainInbox, err := bus.Register(gorole.Main)
	if err != nil {
		t.Fatalf("register main: %v", err)
	}

	pool := NewAgentPool(testModel("default"), provider.StreamOptions{}, nil)
	pool.SetAgentBus(bus)
	orch := NewForegroundOrchestrator(pool)
	pool.SetOrchestrator(orch)

	tool := &DelegateTool{Orchestrator: orch, Pool: pool, Enabled: true}

	if _, err := tool.Execute(`{"agent":"planner","task":"analyze X"}`); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// The mock provider answers with "Test response from mock provider." — the
	// relay must land on main's inbox with the planner label.
	select {
	case msg := <-mainInbox:
		if msg.From != gorole.Planner {
			t.Errorf("relay From = %q, want planner", msg.From)
		}
		if !strings.Contains(msg.Content, "Message from planner:") {
			t.Errorf("relay content missing role label: %q", msg.Content)
		}
		if !strings.Contains(msg.Content, "Test response from mock provider.") {
			t.Errorf("relay content missing sub-agent output: %q", msg.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no relay message on main inbox for planner delegate")
	}
}

func TestRequestReviewTool_Schema(t *testing.T) {
	tool := &RequestReviewTool{}
	schema := tool.Schema()
	if schema.Name != "request_review" {
		t.Errorf("expected name 'request_review', got %q", schema.Name)
	}
	if schema.Description == "" {
		t.Error("expected non-empty description")
	}
}

func TestRequestReviewTool_Execute_Disabled(t *testing.T) {
	tool := &RequestReviewTool{Enabled: false}
	_, err := tool.Execute(`{"content":"test"}`)
	if err == nil {
		t.Fatal("expected error when disabled")
	}
	if !strings.Contains(err.Error(), "agent-driven workflows are disabled") {
		t.Errorf("expected disabled message, got %v", err)
	}
}

func TestRequestReviewTool_Execute_MissingContent(t *testing.T) {
	tool := &RequestReviewTool{Enabled: true}
	_, err := tool.Execute(`{}`)
	if err == nil {
		t.Fatal("expected error for missing content")
	}
	if !strings.Contains(err.Error(), "content is required") {
		t.Errorf("expected 'content is required', got %v", err)
	}
}

func TestRequestReviewTool_Execute_InvalidJSON(t *testing.T) {
	tool := &RequestReviewTool{}
	_, err := tool.Execute(`not-json`)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestRequestReviewTool_Execute_NoPool(t *testing.T) {
	tool := &RequestReviewTool{Enabled: true}
	_, err := tool.Execute(`{"content":"test code"}`)
	if err == nil {
		t.Fatal("expected error with nil pool")
	}
	if !strings.Contains(err.Error(), "agent pool not configured") {
		t.Errorf("expected 'agent pool not configured', got %v", err)
	}
}

func TestDelegateTool_Schema(t *testing.T) {
	tool := &DelegateTool{}
	schema := tool.Schema()
	if schema.Name != "delegate_to" {
		t.Errorf("expected name 'delegate_to', got %q", schema.Name)
	}
	if schema.Description == "" {
		t.Error("expected non-empty description")
	}
}

func TestDelegateTool_Execute_MissingAgent(t *testing.T) {
	tool := &DelegateTool{}
	_, err := tool.Execute(`{"task":"do something"}`)
	if err == nil {
		t.Fatal("expected error for missing agent")
	}
}

func TestDelegateTool_Execute_MissingTask(t *testing.T) {
	tool := &DelegateTool{}
	_, err := tool.Execute(`{"agent":"coder"}`)
	if err == nil {
		t.Fatal("expected error for missing task")
	}
}

func TestDelegateTool_Execute_InvalidJSON(t *testing.T) {
	tool := &DelegateTool{}
	_, err := tool.Execute(`bad json`)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestDelegateTool_Execute_Disabled(t *testing.T) {
	tool := &DelegateTool{Enabled: false}
	_, err := tool.Execute(`{"agent":"coder","task":"do something"}`)
	if err == nil {
		t.Fatal("expected error when disabled")
	}
	if !strings.Contains(err.Error(), "agent-driven workflows are disabled") {
		t.Errorf("expected disabled message, got %v", err)
	}
}

func TestDelegateTool_Execute_NoPool(t *testing.T) {
	tool := &DelegateTool{Enabled: true}
	_, err := tool.Execute(`{"agent":"coder","task":"do something"}`)
	if err == nil {
		t.Fatal("expected error with nil pool")
	}
	if !strings.Contains(err.Error(), "agent pool not configured") {
		t.Errorf("expected 'agent pool not configured', got %v", err)
	}
}

func TestAgentDrivenTools_ReturnsBothTools(t *testing.T) {
	tools := AgentDrivenTools(nil, nil)
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}
	if tools[0].Schema().Name != "request_review" {
		t.Errorf("expected first tool to be request_review, got %q", tools[0].Schema().Name)
	}
	if tools[1].Schema().Name != "delegate_to" {
		t.Errorf("expected second tool to be delegate_to, got %q", tools[1].Schema().Name)
	}
}

func TestAgentDrivenTools_AreRetryableReturnsFalse(t *testing.T) {
	rt := &RequestReviewTool{}
	if rt.IsRetryable(nil) {
		t.Error("RequestReviewTool should not be retryable")
	}

	dt := &DelegateTool{}
	if dt.IsRetryable(nil) {
		t.Error("DelegateTool should not be retryable")
	}
}

func TestAgentDrivenTools_ImplementToolInterface(t *testing.T) {
	var _ agentic.Tool = (*RequestReviewTool)(nil)
	var _ agentic.Tool = (*DelegateTool)(nil)
}
