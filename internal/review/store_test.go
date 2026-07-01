// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package review

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStore_SaveLoad(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	s := &Session{ID: "abc123", ProjectDir: dir, BaseRef: "HEAD^1", HeadRef: "def"}
	s.AddComment("main.go", 1, "looks good")

	if err := store.Save(s); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	path := filepath.Join(store.SessionDir("abc123"), "session.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("session file not created: %v", err)
	}

	loaded, err := store.Load("abc123")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.ID != s.ID {
		t.Errorf("ID mismatch: %q", loaded.ID)
	}
	if len(loaded.Comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(loaded.Comments))
	}
	if loaded.Comments[0].Content != "looks good" {
		t.Errorf("unexpected comment content: %q", loaded.Comments[0].Content)
	}
}

func TestStore_List(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	store.Save(&Session{ID: "one", ProjectDir: dir})
	store.Save(&Session{ID: "two", ProjectDir: dir})

	ids, err := store.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(ids) != 2 {
		t.Errorf("expected 2 sessions, got %d", len(ids))
	}
}
