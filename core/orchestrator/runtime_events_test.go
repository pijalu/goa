// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
)

// TestRuntimeRecordAgentEvents_ForwardThinkingToolCallResult asserts that
// RecordAgentThinking, RecordAgentToolCall, and RecordAgentToolResult emit
// the right event types with payload fields and no-op on nil/empty inputs.
func TestRuntimeRecordAgentEvents_ForwardThinkingToolCallResult(t *testing.T) {
	cfg := testRuntimeConfig()
	pool := NewBoundedAgentPool(cfg, func(role, model string) (*AgentHandle, error) {
		return NewAgentHandle("fake-"+role, role, model), nil
	})
	rt, err := NewRuntime(cfg, pool, nil, "")
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}

	var got []Event
	ch := rt.Events()
	done := make(chan struct{})
	go func() {
		for ev := range ch {
			got = append(got, ev)
		}
		close(done)
	}()

	h := NewAgentHandle("h-1", "coder", "gemma")

	rt.RecordAgentThinking(nil, "text")
	rt.RecordAgentThinking(h, "")
	rt.RecordAgentThinking(h, "reasoning")
	rt.RecordAgentToolCall(h, "writefile", `{"path":"x.txt"}`, "tc1")
	rt.RecordAgentToolResult(h, "tc1", "written", true)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	rt.Cancel()
	_ = rt.Run(ctx, "noop")
	<-done

	assertEventType := func(t *testing.T, got []Event, want EventType) {
		t.Helper()
		for _, ev := range got {
			if ev.Type == want {
				return
			}
		}
		t.Errorf("missing event type %s in %v", want, eventTypesFor(got))
	}
	assertEventType(t, got, EventAgentThinking)
	assertEventType(t, got, EventAgentToolCall)
	assertEventType(t, got, EventAgentToolResult)

	for _, ev := range got {
		if ev.Type == EventAgentThinking || ev.Type == EventAgentToolCall || ev.Type == EventAgentToolResult {
			if ev.Role != "coder" || ev.AgentID != "h-1" {
				t.Errorf("event %s has wrong role/agent: %+v", ev.Type, ev)
			}
		}
	}

	for _, ev := range got {
		if ev.Type == EventAgentToolResult {
			if ok, okok := ev.Payload["ok"].(bool); !okok || !ok {
				t.Errorf("EventAgentToolResult ok = %v, want true", ok)
			}
			if text, _ := ev.Payload["text"].(string); text != "written" {
				t.Errorf("EventAgentToolResult text = %q, want written", text)
			}
		}
	}
}

func testRuntimeConfig() config.OrchestratorConfig {
	return config.OrchestratorConfig{Roles: map[string]config.OrchestratorRole{"coder": {Model: "gemma"}}}
}

func eventTypesFor(evts []Event) []EventType {
	out := make([]EventType, len(evts))
	for i, ev := range evts {
		out[i] = ev.Type
	}
	return out
}
