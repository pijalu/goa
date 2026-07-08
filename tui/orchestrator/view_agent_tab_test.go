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

// TestMultiAgentView_PerAgentTabsCreated is the RED regression for the tab
// order bug: each started agent gets its own filter tab, ordered after the
// Stats/Conversation bookends, with disambiguated labels.
func TestMultiAgentView_PerAgentTabsCreated(t *testing.T) {
	v := buildMultiAgentView(t)
	tabs := v.Tabs()
	if len(tabs) != 5 {
		t.Fatalf("tabs = %d, want 5 (stats+conversation+3 agents): %+v", len(tabs), tabs)
	}
	if tabs[0].Kind != TabStats || tabs[1].Kind != TabConversation {
		t.Errorf("bookend tabs wrong: %+v %+v", tabs[0], tabs[1])
	}

	byKey := map[string]AgentTab{}
	for _, tb := range tabs {
		byKey[tb.Key] = tb
	}
	if byKey["coder-1"].Kind != TabAgent || byKey["coder-1"].Label != "coder" {
		t.Errorf("coder-1 tab = %+v, want TabAgent labelled coder", byKey["coder-1"])
	}
	if byKey["reviewer-1"].Label != "reviewer" {
		t.Errorf("reviewer-1 label = %q", byKey["reviewer-1"].Label)
	}
	if byKey["coder-2"].Label != "coder·2" {
		t.Errorf("coder-2 label = %q, want coder·2", byKey["coder-2"].Label)
	}
}

// TestMultiAgentView_TabSteeringTarget asserts that selecting a per-agent tab
// targets that agent for steering, the Conversation tab targets the most
// recently started agent, and the Stats tab broadcasts to all agents.
func TestMultiAgentView_TabSteeringTarget(t *testing.T) {
	v := buildMultiAgentView(t)

	if !v.SelectByKey("reviewer-1") {
		t.Fatal("SelectByKey(reviewer-1) failed")
	}
	if got := v.ActiveAgentID(); got != "reviewer-1" {
		t.Errorf("ActiveAgentID on reviewer tab = %q, want reviewer-1", got)
	}

	v.SelectByKey("conversation")
	if got := v.ActiveAgentID(); got != "coder-2" {
		t.Errorf("ActiveAgentID on conversation = %q, want coder-2 (last started)", got)
	}

	v.SelectByKey("stats")
	if got := v.ActiveAgentID(); got != "" {
		t.Errorf("ActiveAgentID on stats = %q, want empty (broadcast)", got)
	}
}
