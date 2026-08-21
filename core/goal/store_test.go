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
	assertHandoverDecode(t, `{"type":"goal.create","goalId":"g1","objective":"next goal","handoff":"evidence from old session"}`, "evidence from old session")
	assertHandoverDecode(t, `{"type":"goal.create","goalId":"g1","objective":"next goal","handover":"evidence from new session"}`, "evidence from new session")
	assertHandoverDecode(t, `{"type":"goal.create","goalId":"g1","objective":"next goal","handoff":"legacy","handover":"explicit"}`, "explicit")
	data, err := json.Marshal(GoalEventRecord{Type: GoalEventCreate, Handoff: strPtr("note")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"handover":"note"`) || strings.Contains(string(data), `"handoff"`) {
		t.Errorf("marshal must use handover: %s", data)
	}
}

func assertHandoverDecode(t *testing.T, raw, want string) {
	var record GoalEventRecord
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		t.Fatal(err)
	}
	if record.Handoff == nil || *record.Handoff != want {
		t.Errorf("handover = %+v, want %q", record.Handoff, want)
	}
	if record.Objective == nil || *record.Objective != "next goal" {
		t.Errorf("objective = %+v", record.Objective)
	}
}
