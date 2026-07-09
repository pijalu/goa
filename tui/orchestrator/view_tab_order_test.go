// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

import (
	"testing"
)

// TestView_TabOrder_Bug_ReportedOrder is a regression test for the simplified
// tab bar: only the Stats and Conversation bookends remain; per-agent tabs are
// replaced by ctrl-x steering targets.
func TestView_TabOrder_Bug_ReportedOrder(t *testing.T) {
	v := NewMultiAgentView("orchestration")
	v.ApplyEvent(AgentViewEvent{Kind: EvSourceStarted})
	v.ApplyEvent(AgentViewEvent{Kind: EvAgentStarted, AgentID: "orch-1", Role: "orchestrator"})
	v.ApplyEvent(AgentViewEvent{Kind: EvAgentStarted, AgentID: "coder-1", Role: "coder"})
	v.ApplyEvent(AgentViewEvent{Kind: EvAgentStarted, AgentID: "reviewer-1", Role: "reviewer"})

	want := []string{"stats", "conversation"}
	got := tabKeys(v.Tabs())
	if len(got) != len(want) {
		t.Fatalf("tabs = %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("tab %d = %q, want %q", i, got[i], w)
		}
	}

	// Steering targets still include all agents, with orchestrator first.
	targets := v.SteerTargets()
	wantTargets := []string{"all", "orch-1", "coder-1", "reviewer-1"}
	if len(targets) != len(wantTargets) {
		t.Fatalf("SteerTargets = %v, want %v", targets, wantTargets)
	}
	for i, w := range wantTargets {
		if targets[i] != w {
			t.Errorf("SteerTargets[%d] = %q, want %q", i, targets[i], w)
		}
	}
}
