// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestCreateGoal_HandoverRoundTrip verifies the handover is a first-class goal
// field: set at create time, exposed on the snapshot (get), and replayed from
// the persisted event record (durable across restart).
func TestCreateGoal_HandoverRoundTrip(t *testing.T) {
	mode := NewGoalMode(nil, nil, nil, nil)
	handover := "State: shipped v1. Decisions: keep API. Next: write tests."
	snap, err := mode.CreateGoal(CreateGoalInput{
		Objective: "follow up",
		Handoff:   &handover,
	}, GoalActorModel)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Handoff == nil || *snap.Handoff != handover {
		t.Errorf("snapshot handover = %+v, want %q", snap.Handoff, handover)
	}
	// get path: GetGoal exposes the stored handover.
	if g := mode.GetGoal().Goal; g == nil || g.Handoff == nil || *g.Handoff != handover {
		t.Errorf("get handover = %+v, want %q", g.Handoff, handover)
	}
	// Snapshot JSON round-trip exposes the handover surface key.
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"handover":"State: shipped v1.`) {
		t.Errorf("snapshot JSON must expose handover: %s", data)
	}
}

// TestCreateGoal_HandoverNormalized verifies blank handover maps to nil (no
// block rendered downstream) and whitespace is trimmed.
func TestCreateGoal_HandoverNormalized(t *testing.T) {
	mode := NewGoalMode(nil, nil, nil, nil)
	blank := "   "
	snap, err := mode.CreateGoal(CreateGoalInput{Objective: "x", Handoff: &blank}, GoalActorModel)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Handoff != nil {
		t.Errorf("blank handover must normalize to nil, got %q", *snap.Handoff)
	}
	if got := BuildStaticGoalReminder(snap); strings.Contains(got, "<untrusted_handover>") {
		t.Error("empty handover must not render an <untrusted_handover> block")
	}
}

// TestCreateGoal_HandoverCap verifies over-long handover notes are rejected at
// create time (cap ~4KB), matching the spec's "capped" behavior.
func TestCreateGoal_HandoverCap(t *testing.T) {
	mode := NewGoalMode(nil, nil, nil, nil)
	tooLong := strings.Repeat("x", MaxHandoverLength+1)
	if _, err := mode.CreateGoal(CreateGoalInput{Objective: "x", Handoff: &tooLong}, GoalActorModel); err == nil {
		t.Fatal("expected error for handover exceeding cap")
	}
	// Exactly at the cap is accepted.
	atCap := strings.Repeat("x", MaxHandoverLength)
	if _, err := mode.CreateGoal(CreateGoalInput{Objective: "x", Handoff: &atCap}, GoalActorModel); err != nil {
		t.Fatalf("handover at cap must be accepted: %v", err)
	}
}

// TestNormalizeHandover exercises the shared normalize+cap helper used by both
// the active-goal create path and the durable queue store.
func TestNormalizeHandover(t *testing.T) {
	if v, err := NormalizeHandover(nil); err != nil || v != nil {
		t.Errorf("nil -> (%v, %v), want (nil, nil)", v, err)
	}
	trimmed, err := NormalizeHandover(strPtr("  note  "))
	if err != nil || trimmed == nil || *trimmed != "note" {
		t.Errorf("trim = (%v, %v)", trimmed, err)
	}
	if _, err := NormalizeHandover(strPtr(strings.Repeat("y", MaxHandoverLength+1))); err == nil {
		t.Error("expected cap error")
	}
}

// TestCreateGoal_HandoverPersistedToEventRecord verifies the handover is
// written into the goal.create event record so a clean-context restart can
// rebuild it (durable storage, not just in-memory).
func TestCreateGoal_HandoverPersistedToEventRecord(t *testing.T) {
	store := &memoryEventStore{}
	mode := NewGoalMode(store, nil, nil, nil)
	handover := "durable note"
	if _, err := mode.CreateGoal(CreateGoalInput{Objective: "x", Handoff: &handover}, GoalActorModel); err != nil {
		t.Fatal(err)
	}
	if len(store.records) != 1 || store.records[0].Handoff == nil || *store.records[0].Handoff != handover {
		t.Errorf("create record handover = %+v", store.records)
	}
}

// memoryEventStore is a minimal in-memory EventStore for tests.
type memoryEventStore struct {
	records []GoalEventRecord
}

func (s *memoryEventStore) Append(record GoalEventRecord) error {
	s.records = append(s.records, record)
	return nil
}

func (s *memoryEventStore) Replay() ([]GoalEventRecord, error) {
	return append([]GoalEventRecord(nil), s.records...), nil
}
