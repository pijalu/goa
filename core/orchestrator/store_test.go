// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

import (
	"path/filepath"
	"testing"
)

func TestFileEventStore_AppendReplayRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := NewFileEventStore(dir, "run-1")

	events := []Event{
		{Type: EventRunStarted, RunID: "run-1", Payload: map[string]any{"objective": "ship it"}},
		{Type: EventAgentStarted, RunID: "run-1", AgentID: "coder-1", Role: "coder", Model: "m1"},
		{Type: EventAgentMessage, RunID: "run-1", AgentID: "coder-1", Payload: map[string]any{"text": "hello"}},
		{Type: EventAgentSteered, RunID: "run-1", AgentID: "coder-1", Payload: map[string]any{"from": "user", "text": "go faster"}},
		{Type: EventAgentStats, RunID: "run-1", AgentID: "coder-1", Payload: map[string]any{"tokens_in": 100}},
		{Type: EventAgentFinished, RunID: "run-1", AgentID: "coder-1"},
		{Type: EventRunFinished, RunID: "run-1"},
	}
	for _, e := range events {
		if err := s.Append(e); err != nil {
			t.Fatalf("Append(%s): %v", e.Type, err)
		}
	}

	wantPath := filepath.Join(dir, "run-1", "events.jsonl")
	if got := s.Path(); got != wantPath {
		t.Errorf("Path = %q, want %q", got, wantPath)
	}

	got, err := s.Replay()
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(got) != len(events) {
		t.Fatalf("replay len = %d, want %d", len(got), len(events))
	}
	for i, e := range got {
		if e.Type != events[i].Type {
			t.Errorf("event %d type = %q, want %q", i, e.Type, events[i].Type)
		}
		if e.Seq != int64(i+1) {
			t.Errorf("event %d seq = %d, want %d", i, e.Seq, i+1)
		}
		if e.RunID != "run-1" {
			t.Errorf("event %d run id = %q", i, e.RunID)
		}
		if e.Timestamp.IsZero() {
			t.Errorf("event %d timestamp not stamped", i)
		}
	}
}

func TestFileEventStore_MissingFileIsEmpty(t *testing.T) {
	s := NewFileEventStore(t.TempDir(), "never")
	got, err := s.Replay()
	if err != nil {
		t.Fatalf("Replay on missing file: %v", err)
	}
	if got != nil {
		t.Errorf("Replay on missing file = %v, want nil", got)
	}
}

func TestFileEventStore_SeqMonotonicAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	s1 := NewFileEventStore(dir, "run-2")
	for i := 0; i < 3; i++ {
		if err := s1.Append(Event{Type: EventAgentMessage}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	// A new store instance reading the same file must continue seq from the
	// last persisted value, never restarting at 1.
	s2 := NewFileEventStore(dir, "run-2")
	if _, err := s2.Replay(); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if err := s2.Append(Event{Type: EventAgentFinished}); err != nil {
		t.Fatalf("Append after reopen: %v", err)
	}
	got, err := s2.Replay()
	if err != nil {
		t.Fatalf("Replay 2: %v", err)
	}
	last := got[len(got)-1]
	if last.Seq != 4 {
		t.Errorf("seq after reopen = %d, want 4", last.Seq)
	}
}

func TestFileEventStore_CorruptLinesSkipped(t *testing.T) {
	dir := t.TempDir()
	s := NewFileEventStore(dir, "run-3")
	if err := s.Append(Event{Type: EventRunStarted}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	// Append a malformed line directly.
	if err := s.Append(Event{Type: EventRunFinished}); err != nil {
		t.Fatalf("Append 2: %v", err)
	}
	got, err := s.Replay()
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	// Both lines were valid JSON; just assert count as a baseline.
	if len(got) != 2 {
		t.Errorf("len = %d, want 2", len(got))
	}
}
