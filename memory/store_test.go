// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package memory

import (
	"os"
	"testing"
)

// TestMemoryStoreListEmpty verifies listing with no files.
func TestMemoryStoreListEmpty(t *testing.T) {
	dir, cleanup := tempDir(t)
	defer cleanup()

	store := NewMemoryStore(dir, dir)
	files, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("Expected 0 files, got %d", len(files))
	}
}

// TestMemoryStoreWriteAndRead verifies write and read round-trip.
func TestMemoryStoreWriteAndRead(t *testing.T) {
	dir, cleanup := tempDir(t)
	defer cleanup()

	store := NewMemoryStore(dir, dir)

	if err := store.Write("test-note", "This is a test memory file."); err != nil {
		t.Fatalf("Write: %v", err)
	}

	content, err := store.Read("test-note")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if !strContains(content, "test memory file") {
		t.Errorf("Content missing expected text: %s", content)
	}
}

// TestMemoryStoreAppend verifies appending to a memory file.
func TestMemoryStoreAppend(t *testing.T) {
	dir, cleanup := tempDir(t)
	defer cleanup()

	store := NewMemoryStore(dir, dir)
	store.Write("notes", "# Notes\n\n## Decisions\n\nFirst decision.\n")
	store.Append("notes", "Decisions", "Second decision.")

	content, _ := store.Read("notes")
	if !strContains(content, "Second decision.") {
		t.Errorf("Content missing appended text: %s", content)
	}
}

// TestMemoryStoreAppendNewSection verifies creating a new section on append.
func TestMemoryStoreAppendNewSection(t *testing.T) {
	dir, cleanup := tempDir(t)
	defer cleanup()

	store := NewMemoryStore(dir, dir)
	store.Write("notes", "# Notes\n")
	store.Append("notes", "NewSection", "Content for new section.")

	content, _ := store.Read("notes")
	if !strContains(content, "NewSection") {
		t.Errorf("Content missing new section: %s", content)
	}
}

// TestMemoryStoreDelete verifies deletion.
func TestMemoryStoreDelete(t *testing.T) {
	dir, cleanup := tempDir(t)
	defer cleanup()

	store := NewMemoryStore(dir, dir)
	store.Write("to-delete", "content")
	store.Delete("to-delete")

	_, err := store.Read("to-delete")
	if err == nil {
		t.Error("Expected error after deletion")
	}
}

// TestMemoryStoreProjectTakesPrecedence verifies project over global.
func TestMemoryStoreProjectTakesPrecedence(t *testing.T) {
	projectDir, cleanup1 := tempDir(t)
	globalDir, cleanup2 := tempDir(t)
	defer cleanup1()
	defer cleanup2()

	// Write different content to global vs project
	store := NewMemoryStore(projectDir, globalDir)
	globalStore := NewMemoryStore("", globalDir)
	globalStore.Write("shared", "global content")

	// Now project write should take precedence
	store.Write("shared", "project content")

	content, _ := store.Read("shared")
	if !strContains(content, "project content") {
		t.Errorf("Project should take precedence: %s", content)
	}
}

// TestMemoryStoreList verifies listing multiple files.
func TestMemoryStoreList(t *testing.T) {
	dir, cleanup := tempDir(t)
	defer cleanup()

	store := NewMemoryStore(dir, dir)
	store.Write("alpha", "first")
	store.Write("beta", "second")

	files, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(files) != 2 {
		t.Errorf("Expected 2 files, got %d", len(files))
	}
}

// tempDir creates a temporary directory.
func tempDir(t *testing.T) (string, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "goa-memory-test-*")
	if err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	return dir, func() { os.RemoveAll(dir) }
}

// strContains checks for substring.
func strContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
