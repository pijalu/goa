// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

import (
	"strings"
	"testing"
)

// buildLifecycleView feeds a full NEUTRAL event sequence (no
// core/orchestrator import) through MultiAgentView and returns the resulting
// state. Each focused test reads one aspect from the same prepared view — this
// is the proof the view is source-agnostic, since its tests know nothing about
// orchestration.
func buildLifecycleView(t *testing.T) *MultiAgentView {
	t.Helper()
	v := NewMultiAgentView("orchestration")
	events := []AgentViewEvent{
		{Kind: EvSourceStarted, Meta: map[string]string{"objective": "ship it", "topology": "hub"}},
		{Kind: EvAgentStarted, AgentID: "orch-1", Role: "orchestrator", Model: "qwen", Provider: "lmstudio", Thinking: "medium"},
		{Kind: EvAgentStarted, AgentID: "coder-1", Role: "coder", Model: "gemma", Provider: "google", Thinking: "off"},
		{Kind: EvAgentStats, AgentID: "coder-1", Role: "coder", Status: "running", Stats: &AgentStatsDelta{
			Turns: 1, TokensIn: 40, TokensOut: 12, CacheRead: 1024, ToolCalls: 2}},
		{Kind: EvAgentSteered, AgentID: "coder-1", Role: "coder", Text: "use bcrypt"},
		{Kind: EvAgentFinished, AgentID: "coder-1", Role: "coder", Status: "ok"},
		{Kind: EvSourceFinished, Status: "ok"},
	}
	for _, ev := range events {
		v.ApplyEvent(ev)
	}
	return v
}

// TestView_TabsAndOrdering asserts the tab bar only contains the Conversation
// and Stats bookends; per-agent filter tabs were removed in favor of ctrl-x
// steering targets.
func TestView_TabsAndOrdering(t *testing.T) {
	v := buildLifecycleView(t)
	keys := tabKeys(v.Tabs())
	if len(keys) != 2 {
		t.Fatalf("tabs = %v, want 2 (stats + conversation)", keys)
	}
	if keys[0] != "stats" || keys[1] != "conversation" {
		t.Errorf("bookend tabs = %v, want stats,conversation", keys)
	}
	if active, _ := v.ActiveTab(); active.Key != "stats" {
		t.Errorf("active = %q, want stats", active.Key)
	}
	if got, want := v.TabIndex(), "1/2"; got != want {
		t.Errorf("TabIndex = %q, want %q", got, want)
	}
}

// TestView_Navigation exercises Cycle, SelectByKey (string + numeric), and the
// unknown-key rejection with only two tabs.
func TestView_Navigation(t *testing.T) {
	v := buildLifecycleView(t)
	v.Cycle(1)
	if active, _ := v.ActiveTab(); active.Key != "conversation" {
		t.Errorf("after Cycle(1) active = %q, want conversation", active.Key)
	}
	if !v.SelectByKey("stats") {
		t.Fatal("SelectByKey(stats) returned false")
	}
	if active, _ := v.ActiveTab(); active.Key != "stats" {
		t.Errorf("after SelectByKey(stats) active = %q, want stats", active.Key)
	}
	if got, want := v.TabIndex(), "1/2"; got != want {
		t.Errorf("TabIndex = %q, want %q", got, want)
	}
	if !v.SelectByKey("2") {
		t.Error("SelectByKey(2) returned false")
	}
	if active, _ := v.ActiveTab(); active.Key != "conversation" {
		t.Errorf("after SelectByKey(2) active = %q, want conversation", active.Key)
	}
	if v.SelectByKey("nope") {
		t.Error("SelectByKey(unknown) should return false")
	}
}

// TestView_StatsRow verifies the stats row carries provider/thinking/cache and
// the finished outcome.
func TestView_StatsRow(t *testing.T) {
	v := buildLifecycleView(t)
	coder := rowFor(v, "coder-1")
	if coder == nil {
		t.Fatal("missing coder row")
	}
	checks := []struct{ name, got, want string }{
		{"provider", coder.Provider, "google"},
		{"thinking", coder.Thinking, "off"},
		{"status", coder.Status, "ok"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("coder %s = %q, want %q", c.name, c.got, c.want)
		}
	}
	if coder.CacheRead != 1024 || coder.TokensOut != 12 {
		t.Errorf("coder counters = ch %d out %d, want 1024/12", coder.CacheRead, coder.TokensOut)
	}
	in, out, ch, _, turns := v.AggregateTokens()
	if in != 40 || out != 12 || ch != 1024 || turns != 1 {
		t.Errorf("aggregate = in %d out %d ch %d turns %d", in, out, ch, turns)
	}
}

// TestView_TranscriptAndMarkers checks the steer/finish markers were captured
// into the agent log even though they are no longer rendered as transcript tabs.
func TestView_TranscriptAndMarkers(t *testing.T) {
	v := buildLifecycleView(t)
	log := v.LogFor("coder-1")
	if log == nil {
		t.Fatal("missing coder log")
	}
	lines := log.Lines()
	for _, want := range []string{"[steer] use bcrypt", "[finished]"} {
		if !containsJoin(lines, want) {
			t.Errorf("coder log missing %q: %+v", want, lines)
		}
	}
	if !v.Finished() {
		t.Error("view not marked finished")
	}
}

// TestView_FailedRun sets the failed flag when EvSourceFinished reports failure.
func TestView_FailedRun(t *testing.T) {
	v := NewMultiAgentView("orchestration")
	v.ApplyEvent(AgentViewEvent{Kind: EvSourceStarted})
	v.ApplyEvent(AgentViewEvent{Kind: EvSourceFinished, Status: "failed"})
	if !v.Finished() || !v.Failed() {
		t.Errorf("Finished=%v Failed=%v, want true/true", v.Finished(), v.Failed())
	}
}

// TestView_ActiveAgentID verifies the steering target is independent of the
// active tab. By default it broadcasts to all (empty string); after cycling it
// points at the selected agent.
func TestView_ActiveAgentID(t *testing.T) {
	v := NewMultiAgentView("orchestration")
	v.ApplyEvent(AgentViewEvent{Kind: EvSourceStarted})
	v.ApplyEvent(AgentViewEvent{Kind: EvAgentStarted, AgentID: "coder-1", Role: "coder"})

	if got := v.ActiveAgentID(); got != "" {
		t.Errorf("default ActiveAgentID = %q, want empty (all)", got)
	}
	v.CycleSteerTarget(1)
	if got := v.ActiveAgentID(); got != "coder-1" {
		t.Errorf("after CycleSteerTarget ActiveAgentID = %q, want coder-1", got)
	}
	v.CycleSteerTarget(-1)
	if got := v.ActiveAgentID(); got != "" {
		t.Errorf("after cycling back ActiveAgentID = %q, want empty (all)", got)
	}
}

// TestView_DisambiguatesDuplicateRoles asserts that the DisambiguateLabel rule
// still produces "coder", "coder·2" for repeated roles.
func TestView_DisambiguatesDuplicateRoles(t *testing.T) {
	v := NewMultiAgentView("orchestration")
	if got := v.DisambiguateLabel("coder"); got != "coder" {
		t.Errorf("first label = %q, want coder", got)
	}
	if got := v.DisambiguateLabel("coder"); got != "coder·2" {
		t.Errorf("second label = %q, want coder·2", got)
	}
}

func tabKeys(tabs []AgentTab) []string {
	out := make([]string, len(tabs))
	for i, t := range tabs {
		out[i] = t.Key
	}
	return out
}

func rowFor(v *MultiAgentView, id string) *AgentEnhancedRow {
	for i := range v.rows {
		if v.rows[i].AgentID == id {
			return &v.rows[i]
		}
	}
	return nil
}

func containsJoin(lines []AgentLogLine, want string) bool {
	for _, l := range lines {
		if strings.Contains(l.Text, want) {
			return true
		}
	}
	return false
}
