// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

import (
	"testing"
)

// TestView_TabOrder_Bug_ReportedOrder is a RED test for the tab-order bug
// reported in bugs.md: the expected order is Stats, Conversation,
// Orchestrator, then other agents.
func TestView_TabOrder_Bug_ReportedOrder(t *testing.T) {
	v := NewMultiAgentView("orchestration")
	v.ApplyEvent(AgentViewEvent{Kind: EvSourceStarted})
	v.ApplyEvent(AgentViewEvent{Kind: EvAgentStarted, AgentID: "orch-1", Role: "orchestrator"})
	v.ApplyEvent(AgentViewEvent{Kind: EvAgentStarted, AgentID: "coder-1", Role: "coder"})
	v.ApplyEvent(AgentViewEvent{Kind: EvAgentStarted, AgentID: "reviewer-1", Role: "reviewer"})

	want := []string{"stats", "conversation", "orch-1", "coder-1", "reviewer-1"}
	got := tabKeys(v.Tabs())
	for i, w := range want {
		if i >= len(got) {
			t.Fatalf("tabs truncated at %d: got %v", i, got)
		}
		if got[i] != w {
			t.Errorf("tab %d = %q, want %q (got %v)", i, got[i], w, got)
		}
	}
}
