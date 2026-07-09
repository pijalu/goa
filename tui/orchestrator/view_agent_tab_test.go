// SPDX-License-Identifier: GPL-3.0-or-later

package orchestrator

import (
	"testing"
)

func buildMultiAgentView(t *testing.T) *MultiAgentView {
	t.Helper()
	v := NewMultiAgentView("orchestration")
	v.ApplyEvent(AgentViewEvent{Kind: EvSourceStarted})
	v.ApplyEvent(AgentViewEvent{Kind: EvAgentStarted, AgentID: "coder-1", Role: "coder"})
	v.ApplyEvent(AgentViewEvent{Kind: EvAgentStarted, AgentID: "reviewer-1", Role: "reviewer"})
	v.ApplyEvent(AgentViewEvent{Kind: EvAgentStarted, AgentID: "coder-2", Role: "coder"})
	return v
}

// TestMultiAgentView_NoPerAgentTabsCreated asserts that the tab bar only
// contains the Stats and Conversation bookends. Per-agent filter tabs were
// removed in favor of ctrl-x steering targets.
func TestMultiAgentView_NoPerAgentTabsCreated(t *testing.T) {
	v := buildMultiAgentView(t)
	tabs := v.Tabs()
	if len(tabs) != 2 {
		t.Fatalf("tabs = %d, want 2 (stats + conversation): %+v", len(tabs), tabs)
	}
	if tabs[0].Kind != TabStats || tabs[1].Kind != TabConversation {
		t.Errorf("bookend tabs wrong: %+v %+v", tabs[0], tabs[1])
	}
}

// TestMultiAgentView_SteerTargetsCreated asserts that steering targets include
// "all" first, followed by every started agent in first-seen order.
func TestMultiAgentView_SteerTargetsCreated(t *testing.T) {
	v := buildMultiAgentView(t)
	want := []string{"all", "coder-1", "reviewer-1", "coder-2"}
	got := v.SteerTargets()
	if len(got) != len(want) {
		t.Fatalf("SteerTargets = %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("SteerTargets[%d] = %q, want %q", i, got[i], w)
		}
	}
}

// TestMultiAgentView_SteerTargetCycling verifies that CycleSteerTarget advances
// and wraps through "all" and every agent, including negative directions.
func TestMultiAgentView_SteerTargetCycling(t *testing.T) {
	v := buildMultiAgentView(t)
	want := []string{"all", "coder-1", "reviewer-1", "coder-2"}

	for i, w := range want {
		if got := v.SteerTarget(); got != w {
			t.Errorf("cycle %d: SteerTarget = %q, want %q", i, got, w)
		}
		v.CycleSteerTarget(1)
	}
	if got := v.SteerTarget(); got != "all" {
		t.Errorf("after full cycle SteerTarget = %q, want all", got)
	}

	// Negative direction wraps backward.
	v.SetSteerTarget("coder-2")
	v.CycleSteerTarget(-1)
	if got := v.SteerTarget(); got != "reviewer-1" {
		t.Errorf("cycle back SteerTarget = %q, want reviewer-1", got)
	}
}
