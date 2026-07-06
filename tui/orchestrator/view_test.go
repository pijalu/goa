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
		{Kind: EvAgentMessage, AgentID: "coder-1", Role: "coder", Text: "writing tests"},
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

// TestView_TabsAndOrdering asserts the bookend + per-agent tabs and the default
// active selection.
func TestView_TabsAndOrdering(t *testing.T) {
	v := buildLifecycleView(t)
	keys := tabKeys(v.Tabs())
	want := []string{"stats", "orch-1", "coder-1", "all"}
	if !equalSlice(keys, want) {
		t.Errorf("tabs = %v, want %v", keys, want)
	}
	if active, _ := v.ActiveTab(); active.Key != "stats" {
		t.Errorf("active = %q, want stats", active.Key)
	}
	if got, want := v.TabIndex(), "1/4"; got != want {
		t.Errorf("TabIndex = %q, want %q", got, want)
	}
	ordered := v.OrderedLogs()
	if len(ordered) != 2 || ordered[0].AgentID != "orch-1" || ordered[1].AgentID != "coder-1" {
		t.Errorf("OrderedLogs = %+v", ordered)
	}
}

// TestView_Navigation exercises Cycle, SelectByKey (string + numeric), and the
// unknown-key rejection.
func TestView_Navigation(t *testing.T) {
	v := buildLifecycleView(t)
	v.Cycle(1)
	if active, _ := v.ActiveTab(); active.Key != "orch-1" {
		t.Errorf("after Cycle(1) active = %q, want orch-1", active.Key)
	}
	if !v.SelectByKey("all") {
		t.Fatal("SelectByKey(all) returned false")
	}
	if active, _ := v.ActiveTab(); active.Key != "all" {
		t.Errorf("after SelectByKey(all) active = %q, want all", active.Key)
	}
	if got, want := v.TabIndex(), "4/4"; got != want {
		t.Errorf("TabIndex = %q, want %q", got, want)
	}
	if !v.SelectByKey("1") {
		t.Error("SelectByKey(1) returned false")
	}
	if active, _ := v.ActiveTab(); active.Key != "stats" {
		t.Errorf("after SelectByKey(1) active = %q, want stats", active.Key)
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
	in, out, ch, turns := v.AggregateTokens()
	if in != 40 || out != 12 || ch != 1024 || turns != 1 {
		t.Errorf("aggregate = in %d out %d ch %d turns %d", in, out, ch, turns)
	}
}

// TestView_TranscriptAndMarkers checks the streamed text and the steer/finish
// markers were captured into the agent log.
func TestView_TranscriptAndMarkers(t *testing.T) {
	v := buildLifecycleView(t)
	log := v.LogFor("coder-1")
	if log == nil {
		t.Fatal("missing coder log")
	}
	lines := log.Lines()
	for _, want := range []string{"writing tests", "[steer] use bcrypt", "[finished]"} {
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

// TestView_ActiveAgentID verifies steering-target resolution: agent tabs return
// their AgentID; Stats/All return "" (meaning "steer all").
func TestView_ActiveAgentID(t *testing.T) {
	v := NewMultiAgentView("orchestration")
	v.ApplyEvent(AgentViewEvent{Kind: EvSourceStarted})
	v.ApplyEvent(AgentViewEvent{Kind: EvAgentStarted, AgentID: "coder-1", Role: "coder"})

	wantFor := func(sel, want string) {
		t.Helper()
		v.SelectByKey(sel)
		if got := v.ActiveAgentID(); got != want {
			t.Errorf("active %q AgentID = %q, want %q", sel, got, want)
		}
	}
	wantFor("stats", "")
	wantFor("coder-1", "coder-1")
	wantFor("all", "")
}

// TestView_LateAgentKeepsActiveTab verifies inserting a new agent tab (which
// shifts the All tab right) keeps the active selection stable.
func TestView_LateAgentKeepsActiveTab(t *testing.T) {
	v := NewMultiAgentView("orchestration")
	v.ApplyEvent(AgentViewEvent{Kind: EvSourceStarted})
	v.ApplyEvent(AgentViewEvent{Kind: EvAgentStarted, AgentID: "a-1", Role: "a"})
	v.SelectByKey("all")
	v.ApplyEvent(AgentViewEvent{Kind: EvAgentStarted, AgentID: "b-1", Role: "b"})
	if active, _ := v.ActiveTab(); active.Key != "all" {
		t.Errorf("late insert moved active to %q, want all", active.Key)
	}
	if got, want := v.TabIndex(), "4/4"; got != want {
		t.Errorf("TabIndex = %q, want %q", got, want)
	}
}

// TestView_DisambiguatesDuplicateRoles asserts that when the same role
// recurs (hub delegating to "coder" twice), the second agent gets a ·2 suffix
// on BOTH its tab label and its stats row label, so tabs stay distinguishable.
func TestView_DisambiguatesDuplicateRoles(t *testing.T) {
	v := NewMultiAgentView("orchestration")
	v.ApplyEvent(AgentViewEvent{Kind: EvSourceStarted})
	v.ApplyEvent(AgentViewEvent{Kind: EvAgentStarted, AgentID: "c-1", Role: "coder", Provider: "p", Model: "m"})
	v.ApplyEvent(AgentViewEvent{Kind: EvAgentStarted, AgentID: "c-2", Role: "coder", Provider: "p", Model: "m"})

	labels := make([]string, 0, 2)
	for _, tab := range v.Tabs() {
		if tab.Kind == TabAgent {
			labels = append(labels, tab.Label)
		}
	}
	if !equalSlice(labels, []string{"coder", "coder·2"}) {
		t.Errorf("agent tab labels = %v, want [coder coder·2]", labels)
	}
	for _, row := range v.Rows() {
		want := "coder"
		if row.AgentID == "c-2" {
			want = "coder·2"
		}
		if row.Label != want {
			t.Errorf("row %s label = %q, want %q", row.AgentID, row.Label, want)
		}
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

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func containsJoin(lines []AgentLogLine, want string) bool {
	for _, l := range lines {
		if strings.Contains(l.Text, want) {
			return true
		}
	}
	return false
}
