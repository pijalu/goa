// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package multiagent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/provider"
	gorole "github.com/pijalu/goa/internal/role"
)

// drainAllMessages drains the orchestrator's buffered events (non-blocking).
func drainAllMessages(orch *ForegroundOrchestrator) []OrchestratorMessage {
	var out []OrchestratorMessage
	for {
		select {
		case m := <-orch.Events():
			out = append(out, m)
		default:
			return out
		}
	}
}

// delegationStates returns the delegation_state messages for id in emission
// order.
func delegationStates(msgs []OrchestratorMessage, id string) []OrchestratorMessage {
	var out []OrchestratorMessage
	for _, m := range msgs {
		if m.Kind == "delegation_state" && m.DelegationID == id {
			out = append(out, m)
		}
	}
	return out
}

// stateOf extracts the state field of a delegation_state message's
// "<state>|<detail>" content.
func stateOf(m OrchestratorMessage) string {
	state, _, _ := strings.Cut(m.Content, "|")
	return state
}

// noRetryStreamOptions makes a failing provider surface immediately instead
// of burning the real retry backoff schedule (empty Codes = never eligible).
// Same pattern as TestAfterMainTurn_FailureEmitsCompanionStreamEnd.
var noRetryStreamOptions = provider.StreamOptions{
	RetryPolicy: &provider.RetryPolicy{Mode: provider.RetryModeNormal, MaxRetries: 1, Codes: []string{}},
}

// T4: a delegate_to run must bracket its stream with delegation lifecycle
// messages — running BEFORE the run starts (so the TUI can spawn the tab on
// creation) and completed after it succeeds — all carrying the minted id.
func TestDelegateTool_EmitsLifecycleStates(t *testing.T) {
	pool := NewAgentPool(testModel("default"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)
	tool := &DelegateTool{Orchestrator: orch, Pool: pool, Enabled: true}

	out, err := tool.Execute(fmt.Sprintf(`{"agent":%q,"task":"do work"}`, gorole.Coder))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	id := ackID(t, out)

	states := delegationStates(drainAllMessages(orch), id)
	if len(states) != 2 {
		t.Fatalf("expected exactly 2 delegation_state messages (running, completed), got %d", len(states))
	}
	if got := stateOf(states[0]); got != DelegationRunning {
		t.Errorf("first lifecycle state = %q, want %q", got, DelegationRunning)
	}
	if got := stateOf(states[1]); got != DelegationCompleted {
		t.Errorf("terminal lifecycle state = %q, want %q", got, DelegationCompleted)
	}
	for _, m := range states {
		if m.From != gorole.Coder {
			t.Errorf("lifecycle message From=%q, want %q", m.From, gorole.Coder)
		}
	}
}

// T4 (bug-2): a FAILED delegation must leave a visible terminal marker — the
// failed lifecycle state carries the error detail so the TUI can mark the tab
// and render an error card. Previously a failed run produced no stream output
// at all (delegate_to was invisible).
func TestDelegateTool_FailedRunEmitsFailedState(t *testing.T) {
	pool := NewAgentPool(failingModel("delegate"), noRetryStreamOptions, nil)
	orch := NewForegroundOrchestrator(pool)
	tool := &DelegateTool{Orchestrator: orch, Pool: pool, Enabled: true}

	_, err := tool.Execute(fmt.Sprintf(`{"agent":%q,"task":"do work"}`, gorole.Coder))
	if err == nil {
		t.Fatal("Execute should fail with a failing provider")
	}

	msgs := drainAllMessages(orch)
	// The minted id is not returned on failure, so locate the lifecycle
	// messages by kind: exactly one delegation ran.
	var states []OrchestratorMessage
	for _, m := range msgs {
		if m.Kind == "delegation_state" {
			states = append(states, m)
		}
	}
	if len(states) != 2 {
		t.Fatalf("expected running+failed lifecycle messages, got %d (%v)", len(states), msgs)
	}
	if got := stateOf(states[0]); got != DelegationRunning {
		t.Errorf("first lifecycle state = %q, want %q", got, DelegationRunning)
	}
	if got := stateOf(states[1]); got != DelegationFailed {
		t.Errorf("terminal lifecycle state = %q, want %q", got, DelegationFailed)
	}
	_, detail, _ := strings.Cut(states[1].Content, "|")
	if detail == "" {
		t.Error("failed lifecycle state must carry the error detail for the error card")
	}
	if states[1].DelegationID == "" || states[0].DelegationID != states[1].DelegationID {
		t.Errorf("lifecycle messages must share one non-empty delegation id, got %q then %q",
			states[0].DelegationID, states[1].DelegationID)
	}
}

// T4: request_review must mint a delegation id, return it in the ack JSON,
// and stamp it onto the companion's streamed messages so its stream lands in
// its own per-delegation transcript (not the main interleave).
func TestRequestReviewTool_CarriesDelegationID(t *testing.T) {
	pool := NewAgentPool(testModel("default"), provider.StreamOptions{}, nil)
	bus := agentic.NewAgentBus()
	if _, err := bus.Register(gorole.Main); err != nil {
		t.Fatalf("register main on bus: %v", err)
	}
	pool.SetAgentBus(bus)
	orch := NewForegroundOrchestrator(pool)
	tool := &RequestReviewTool{Orchestrator: orch, Pool: pool, Enabled: true}

	out, err := tool.Execute(`{"content":"review this"}`)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	id := ackID(t, out)
	if !strings.HasPrefix(id, "dlg-companion-") {
		t.Errorf("ack.id %q is not a dlg-companion-* id", id)
	}

	msgs := drainAllMessages(orch)
	// Stream messages from the companion role during the review must carry
	// the minted id.
	var streamed int
	for _, m := range msgs {
		if m.From == gorole.Companion && m.Kind != "delegation_state" {
			if m.DelegationID != id {
				t.Errorf("review stream message kind=%q DelegationID=%q, want %q", m.Kind, m.DelegationID, id)
			}
			streamed++
		}
	}
	if streamed == 0 {
		t.Fatal("no companion stream messages observed; DelegationID propagation untested")
	}

	// Lifecycle bracket present and terminal state is completed.
	states := delegationStates(msgs, id)
	if len(states) != 2 || stateOf(states[0]) != DelegationRunning || stateOf(states[1]) != DelegationCompleted {
		t.Errorf("request_review lifecycle states wrong: %v", states)
	}

	// A second review mints a DIFFERENT id.
	out2, err := tool.Execute(`{"content":"review again"}`)
	if err != nil {
		t.Fatalf("second Execute failed: %v", err)
	}
	if id2 := ackID(t, out2); id2 == id {
		t.Errorf("two reviews returned the same id %q", id)
	}
}

// T4 (bug-2): a failed request_review must emit the failed lifecycle state so
// the tab is marked rather than silently absent.
func TestRequestReviewTool_FailedRunEmitsFailedState(t *testing.T) {
	pool := NewAgentPool(failingModel("review"), noRetryStreamOptions, nil)
	orch := NewForegroundOrchestrator(pool)
	tool := &RequestReviewTool{Orchestrator: orch, Pool: pool, Enabled: true}

	if _, err := tool.Execute(`{"content":"review this"}`); err == nil {
		t.Fatal("Execute should fail with a failing provider")
	}

	var states []OrchestratorMessage
	for _, m := range drainAllMessages(orch) {
		if m.Kind == "delegation_state" {
			states = append(states, m)
		}
	}
	if len(states) != 2 || stateOf(states[0]) != DelegationRunning || stateOf(states[1]) != DelegationFailed {
		t.Errorf("request_review failure lifecycle states wrong: %v", states)
	}
}
