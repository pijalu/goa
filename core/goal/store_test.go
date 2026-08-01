// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFileEventStore_AppendAndReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "goal.log")
	store := NewFileEventStore(path)
	obj := "test"
	if err := store.Append(GoalEventRecord{
		Type:      GoalEventCreate,
		Timestamp: time.Now(),
		GoalID:    strPtr("g1"),
		Objective: &obj,
	}); err != nil {
		t.Fatal(err)
	}
	records, err := store.Replay()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].GoalID == nil || *records[0].GoalID != "g1" {
		t.Errorf("records = %v", records)
	}
}

func TestFileEventStore_ReplayMissing(t *testing.T) {
	store := NewFileEventStore(filepath.Join(t.TempDir(), "missing.log"))
	records, err := store.Replay()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Errorf("records = %d", len(records))
	}
}

func TestFileEventStore_ReplaySkipsCorruptLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "goal.log")
	if err := os.WriteFile(path, []byte("not-json\n"), 0644); err != nil {
		t.Fatal(err)
	}
	store := NewFileEventStore(path)
	records, err := store.Replay()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Errorf("records = %d", len(records))
	}
}

// TestGoalEventRecord_UnmarshalHandoverKeys guards the backwards-compatible
// surface rename: records written with the legacy "handoff" key (before the
// rename to "handover") must still decode their continuity note, and new
// records marshal the "handover" key.
func TestGoalEventRecord_UnmarshalHandoverKeys(t *testing.T) {
	obj := "next goal"
	// Legacy persisted line: "handoff" key.
	var legacy GoalEventRecord
	if err := json.Unmarshal([]byte(`{"type":"goal.create","goalId":"g1","objective":"next goal","handoff":"evidence from old session"}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.Handoff == nil || *legacy.Handoff != "evidence from old session" {
		t.Errorf("legacy handoff key not decoded: %+v", legacy.Handoff)
	}
	if legacy.Objective == nil || *legacy.Objective != obj {
		t.Errorf("other fields must still decode: %+v", legacy.Objective)
	}
	// New surface: "handover" key.
	var current GoalEventRecord
	if err := json.Unmarshal([]byte(`{"type":"goal.create","goalId":"g1","objective":"next goal","handover":"evidence from new session"}`), &current); err != nil {
		t.Fatal(err)
	}
	if current.Handoff == nil || *current.Handoff != "evidence from new session" {
		t.Errorf("handover key not decoded: %+v", current.Handoff)
	}
	// Explicit "handover" wins over a legacy "handoff" in the same record.
	var both GoalEventRecord
	if err := json.Unmarshal([]byte(`{"type":"goal.create","goalId":"g1","handoff":"legacy","handover":"explicit"}`), &both); err != nil {
		t.Fatal(err)
	}
	if both.Handoff == nil || *both.Handoff != "explicit" {
		t.Errorf("handover key must win over legacy handoff: %+v", both.Handoff)
	}
	// Marshal writes the new surface key only.
	data, err := json.Marshal(GoalEventRecord{Type: GoalEventCreate, Handoff: strPtr("note")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"handover":"note"`) || strings.Contains(string(data), `"handoff"`) {
		t.Errorf("marshal must use handover surface: %s", data)
	}
}
