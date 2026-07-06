// SPDX-License-Identifier: GPL-3.0-or-later

package orchestrator

import (
	"testing"
)

// TestMultiAgentView_PerAgentTabs is the RED→GREEN test for R4: each started
// agent gets its own filter tab (keyed by agentID, labelled by disambiguated
// role), ordered after the Conversation/Stats bookends, and ActiveAgentID
// targets the tab's own agent for steering.
func TestMultiAgentView_PerAgentTabs(t *testing.T) {
	v := NewMultiAgentView("orchestration")
	v.ApplyEvent(AgentViewEvent{Kind: EvSourceStarted})
	v.ApplyEvent(AgentViewEvent{Kind: EvAgentStarted, AgentID: "coder-1", Role: "coder"})
	v.ApplyEvent(AgentViewEvent{Kind: EvAgentStarted, AgentID: "reviewer-1", Role: "reviewer"})
	v.ApplyEvent(AgentViewEvent{Kind: EvAgentStarted, AgentID: "coder-2", Role: "coder"})

	tabs := v.Tabs()
	// Conversation, Stats, then one per agent.
	if len(tabs) != 5 {
		t.Fatalf("tabs = %d, want 5 (conversation+stats+3 agents): %+v", len(tabs), tabs)
	}
	if tabs[0].Kind != TabConversation || tabs[1].Kind != TabStats {
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
	// Disambiguation: second coder is coder·2.
	if byKey["coder-2"].Label != "coder·2" {
		t.Errorf("coder-2 label = %q, want coder·2", byKey["coder-2"].Label)
	}

	// Selecting a per-agent tab targets that agent for steering.
	if !v.SelectByKey("reviewer-1") {
		t.Fatal("SelectByKey(reviewer-1) failed")
	}
	if got := v.ActiveAgentID(); got != "reviewer-1" {
		t.Errorf("ActiveAgentID on reviewer tab = %q, want reviewer-1", got)
	}

	// Conversation tab targets the most-recently-started agent.
	v.SelectByKey("conversation")
	if got := v.ActiveAgentID(); got != "coder-2" {
		t.Errorf("ActiveAgentID on conversation = %q, want coder-2 (last started)", got)
	}

	// Stats tab broadcasts steering (empty target).
	v.SelectByKey("stats")
	if got := v.ActiveAgentID(); got != "" {
		t.Errorf("ActiveAgentID on stats = %q, want empty (broadcast)", got)
	}
}
