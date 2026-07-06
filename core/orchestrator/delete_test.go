// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDeleteRun(t *testing.T) {
	dir := t.TempDir()
	s := NewFileEventStore(dir, "run-1")
	_ = s.Append(Event{Type: EventRunStarted, Payload: map[string]any{"name": "happy.hare"}})

	if err := DeleteRun(dir, "run-1"); err != nil {
		t.Fatalf("DeleteRun: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "run-1")); !os.IsNotExist(err) {
		t.Errorf("run-1 directory still exists after delete")
	}
}

func TestDeleteAllRuns(t *testing.T) {
	dir := t.TempDir()
	for _, id := range []string{"run-1", "run-2"} {
		s := NewFileEventStore(dir, id)
		_ = s.Append(Event{Type: EventRunStarted, Payload: map[string]any{}})
	}
	deleted, err := DeleteAllRuns(dir)
	if err != nil {
		t.Fatalf("DeleteAllRuns: %v", err)
	}
	if deleted != 2 {
		t.Errorf("deleted = %d, want 2", deleted)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("expected empty dir, got %d entries", len(entries))
	}
}

func TestExpiredRuns(t *testing.T) {
	dir := t.TempDir()
	old := NewFileEventStore(dir, "run-old")
	_ = old.Append(Event{Type: EventRunStarted, Payload: map[string]any{}, Timestamp: time.Now().AddDate(0, 0, -10)})
	_ = old.Append(Event{Type: EventRunFinished, Timestamp: time.Now().AddDate(0, 0, -9)})

	fresh := NewFileEventStore(dir, "run-fresh")
	_ = fresh.Append(Event{Type: EventRunStarted, Payload: map[string]any{}, Timestamp: time.Now().AddDate(0, 0, -1)})
	_ = fresh.Append(Event{Type: EventRunFinished, Timestamp: time.Now()})

	ids, err := ExpiredRuns(dir, 7)
	if err != nil {
		t.Fatalf("ExpiredRuns: %v", err)
	}
	if len(ids) != 1 || ids[0] != "run-old" {
		t.Errorf("expired = %v, want [run-old]", ids)
	}
}

func TestCleanupExpiredRuns(t *testing.T) {
	dir := t.TempDir()
	old := NewFileEventStore(dir, "run-old")
	_ = old.Append(Event{Type: EventRunStarted, Payload: map[string]any{}, Timestamp: time.Now().AddDate(0, 0, -10)})
	_ = old.Append(Event{Type: EventRunFinished, Timestamp: time.Now().AddDate(0, 0, -9)})

	deleted, err := CleanupExpiredRuns(dir, 7)
	if err != nil {
		t.Fatalf("CleanupExpiredRuns: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1", deleted)
	}
}
