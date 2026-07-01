// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"os"
	"path/filepath"
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
