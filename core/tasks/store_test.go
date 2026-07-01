// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tasks

import (
	"os"
	"path/filepath"
	"testing"
)

func TestJSONLStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := NewJSONLStore(filepath.Join(dir, "tasks.jsonl"))

	t1 := Task{ID: "a", Type: "agent", Description: "first", Status: StatusCompleted}
	t2 := Task{ID: "b", Type: "bash", Description: "second", Status: StatusFailed}
	if err := store.Save(t1); err != nil {
		t.Fatalf("save t1: %v", err)
	}
	if err := store.Save(t2); err != nil {
		t.Fatalf("save t2: %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("loaded = %d, want 2", len(loaded))
	}
	if loaded[0].ID != "a" || loaded[1].ID != "b" {
		t.Errorf("ids = %v, %v", loaded[0].ID, loaded[1].ID)
	}
}

func TestJSONLStoreLoadMissing(t *testing.T) {
	dir := t.TempDir()
	store := NewJSONLStore(filepath.Join(dir, "missing", "tasks.jsonl"))
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded) != 0 {
		t.Errorf("loaded = %d, want 0", len(loaded))
	}
}

func TestJSONLStoreSkipsBadLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.jsonl")
	if err := os.WriteFile(path, []byte("{bad json}\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	store := NewJSONLStore(path)
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded) != 0 {
		t.Errorf("loaded = %d, want 0", len(loaded))
	}
}

// TestJSONLStoreLoadDedupsByID guards against CORE-BUG-4: the append-only JSONL
// log records every state transition as a separate row, so a naive Load()
// returned N records for the same task (all but the last stale). After a
// restart, consumers must see exactly one record per ID with the final state.
func TestJSONLStoreLoadDedupsByID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.jsonl")
	store := NewJSONLStore(path)

	task := Task{ID: "x", Type: "agent", Description: "work", Status: StatusPending}
	mustSave := func() {
		t.Helper()
		if err := store.Save(task); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	task.Status = StatusRunning
	mustSave()
	task.Status = StatusCompleted
	task.Result = "ok"
	mustSave()

	// Simulate a reopen: a fresh store reading the same file.
	reopened := NewJSONLStore(path)
	loaded, err := reopened.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("loaded = %d tasks, want exactly 1 (deduplicated)", len(loaded))
	}
	if loaded[0].ID != "x" {
		t.Errorf("id = %q, want %q", loaded[0].ID, "x")
	}
	if loaded[0].Status != StatusCompleted {
		t.Errorf("status = %q, want %q", loaded[0].Status, StatusCompleted)
	}
	if loaded[0].Result != "ok" {
		t.Errorf("result = %q, want %q", loaded[0].Result, "ok")
	}
}
