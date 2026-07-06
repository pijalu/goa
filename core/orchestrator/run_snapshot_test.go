// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

import (
	"os"
	"testing"
)

func TestParseTopology(t *testing.T) {
	cases := []struct {
		in, fb string
		want   Topology
		err    bool
	}{
		{"", "", TopologyHub, false},
		{"", "fanout", TopologyFanout, false},
		{"hub", "", TopologyHub, false},
		{"FANOUT", "", TopologyFanout, false},
		{"pipeline", "", TopologyPipeline, false},
		{"star", "", "", true},
	}
	for _, c := range cases {
		got, err := ParseTopology(c.in, c.fb)
		if c.err {
			if err == nil {
				t.Errorf("ParseTopology(%q,%q) = %q, want error", c.in, c.fb, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseTopology(%q,%q) err: %v", c.in, c.fb, err)
		}
		if got != c.want {
			t.Errorf("ParseTopology(%q,%q) = %q, want %q", c.in, c.fb, got, c.want)
		}
	}
}

func TestReplaySnapshot_RebuildsAgentsAndStats(t *testing.T) {
	dir := t.TempDir()
	s := NewFileEventStore(dir, "run-x")
	buildReplaySnapshotEvents(t, s)

	snap, err := ReplaySnapshot(s)
	if err != nil {
		t.Fatalf("ReplaySnapshot: %v", err)
	}
	assertReplaySnapshot(t, snap)
}

func buildReplaySnapshotEvents(t *testing.T, s *FileEventStore) {
	t.Helper()
	must := func(e Event) {
		if err := s.Append(e); err != nil {
			t.Fatalf("Append %s: %v", e.Type, err)
		}
	}
	must(Event{Type: EventRunStarted, RunID: "run-x", Payload: map[string]any{
		"objective": "test obj", "topology": "fanout", "goal_id": "g1", "name": "happy.hare",
	}})
	must(Event{Type: EventAgentStarted, RunID: "run-x", AgentID: "coder-1", Role: "coder", Model: "m1"})
	must(Event{Type: EventAgentMessage, RunID: "run-x", AgentID: "coder-1", Payload: map[string]any{"text": "hi"}})
	must(Event{Type: EventAgentStats, RunID: "run-x", AgentID: "coder-1", Payload: map[string]any{
		"turns": 2, "tokens_in": 50, "tool_calls": 3,
	}})
	must(Event{Type: EventAgentSteered, RunID: "run-x", AgentID: "coder-1", Payload: map[string]any{"text": "go"}})
	must(Event{Type: EventAgentFinished, RunID: "run-x", AgentID: "coder-1", Payload: map[string]any{"outcome": "ok"}})
	must(Event{Type: EventAgentStarted, RunID: "run-x", AgentID: "crashed-1", Role: "reviewer", Model: "m2"})
	must(Event{Type: EventAgentFinished, RunID: "run-x", AgentID: "crashed-1", Payload: map[string]any{"outcome": "crashed"}})
}

func assertReplaySnapshot(t *testing.T, snap *RunSnapshot) {
	t.Helper()
	assertReplaySnapshotMeta(t, snap)
	assertReplaySnapshotAgents(t, snap)
}

func assertReplaySnapshotMeta(t *testing.T, snap *RunSnapshot) {
	t.Helper()
	if snap.Name != "happy.hare" {
		t.Errorf("snap.Name = %q, want happy.hare", snap.Name)
	}
	if !snap.Started || snap.Finished {
		t.Errorf("started=%v finished=%v, want true/false", snap.Started, snap.Finished)
	}
	if snap.Objective != "test obj" || snap.GoalID != "g1" || snap.Topology != TopologyFanout {
		t.Errorf("run meta mismatch: %+v", snap)
	}
	if len(snap.Agents) != 2 {
		t.Fatalf("agents = %d, want 2", len(snap.Agents))
	}
}

func assertReplaySnapshotAgents(t *testing.T, snap *RunSnapshot) {
	t.Helper()
	c := snap.Agents["coder-1"]
	if c.Role != "coder" || c.Model != "m1" || c.Status != AgentFinished ||
		c.Turns != 2 || c.TokensIn != 50 || c.ToolCalls != 3 ||
		len(c.PendingSteering) != 1 || c.PendingSteering[0] != "go" ||
		len(c.Messages) != 1 || c.Messages[0] != "hi" {
		t.Errorf("coder-1 wrong: %+v", c)
	}
	cr := snap.Agents["crashed-1"]
	if cr.Status != AgentCrashed {
		t.Errorf("crashed-1 status = %q, want crashed", cr.Status)
	}
}

func TestListRuns(t *testing.T) {
	dir := t.TempDir()

	// run-1: finished hub run.
	s1 := NewFileEventStore(dir, "run-1")
	_ = s1.Append(Event{Type: EventRunStarted, Payload: map[string]any{"topology": "hub", "objective": "a"}})
	_ = s1.Append(Event{Type: EventRunFinished})

	// run-2: in-flight fanout run.
	s2 := NewFileEventStore(dir, "run-2")
	_ = s2.Append(Event{Type: EventRunStarted, Payload: map[string]any{"topology": "fanout", "objective": "b", "name": "busy.bee"}})
	_ = s2.Append(Event{Type: EventAgentStarted, AgentID: "x-1", Role: "x"})

	// junk dir without events — must be skipped.
	if err := os.MkdirAll(dir+"/leftover", 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	summaries, err := ListRuns(dir)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(summaries) != 2 {
		t.Fatalf("got %d runs, want 2: %+v", len(summaries), summaries)
	}
	// run-2 updated last → first.
	if summaries[0].RunID != "run-2" {
		t.Errorf("order: first = %q, want run-2", summaries[0].RunID)
	}
	r2 := summaries[0]
	if r2.Name != "busy.bee" {
		t.Errorf("run-2 name = %q, want busy.bee", r2.Name)
	}
	if r2.Finished || r2.Topology != TopologyFanout || r2.AgentCount != 1 || r2.Objective != "b" {
		t.Errorf("run-2 summary wrong: %+v", r2)
	}
	r1 := summaries[1]
	if !r1.Finished || r1.Topology != TopologyHub {
		t.Errorf("run-1 summary wrong: %+v", r1)
	}
}

func TestResolveRunID(t *testing.T) {
	dir := t.TempDir()
	s1 := NewFileEventStore(dir, "run-1")
	_ = s1.Append(Event{Type: EventRunStarted, Payload: map[string]any{"name": "happy.hare"}})
	s2 := NewFileEventStore(dir, "run-2")
	_ = s2.Append(Event{Type: EventRunStarted, Payload: map[string]any{"name": "busy.bee"}})

	cases := []struct {
		id      string
		want    string
		wantErr bool
	}{
		{"happy.hare", "run-1", false},
		{"run-2", "run-2", false},
		{"missing", "", true},
		{"", "", true},
	}
	for _, tc := range cases {
		got, err := ResolveRunID(dir, tc.id)
		if (err != nil) != tc.wantErr {
			t.Errorf("ResolveRunID(%q) err = %v, wantErr=%v", tc.id, err, tc.wantErr)
			continue
		}
		if got != tc.want {
			t.Errorf("ResolveRunID(%q) = %q, want %q", tc.id, got, tc.want)
		}
	}
}

func TestGenerateRunName_Unique(t *testing.T) {
	dir := t.TempDir()
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		name := GenerateRunName(dir)
		if seen[name] {
			t.Fatalf("collision on %q", name)
		}
		seen[name] = true
		s := NewFileEventStore(dir, "run-"+name)
		_ = s.Append(Event{Type: EventRunStarted, Payload: map[string]any{"name": name}})
	}
}
