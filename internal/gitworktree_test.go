// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package internal

import (
	"os"
	"testing"
)

// TestWorktreeManagerShouldUseWorktree verifies all mode combinations.
func TestWorktreeManagerShouldUseWorktree(t *testing.T) {
	tests := []struct {
		mode         WorktreeMode
		isMultiAgent bool
		want         bool
	}{
		{WorktreeAlways, false, true},
		{WorktreeAlways, true, true},
		{WorktreeMultiAgent, false, false},
		{WorktreeMultiAgent, true, true},
		{"", false, false},
		{"", true, false},
	}
	for _, tt := range tests {
		wm := NewWorktreeManager("/tmp", tt.mode)
		got := wm.ShouldUseWorktree(tt.isMultiAgent)
		if got != tt.want {
			t.Errorf("ShouldUseWorktree(%v, %v) = %v, want %v", tt.mode, tt.isMultiAgent, got, tt.want)
		}
	}
}

// TestWorktreeManagerResolvePath verifies path resolution with and without worktree.
func TestWorktreeManagerResolvePath(t *testing.T) {
	wm := NewWorktreeManager("/main", WorktreeAlways)

	// Without worktree: path unchanged
	got := wm.ResolvePath("", "src/main.go")
	if got != "src/main.go" {
		t.Errorf("ResolvePath('', 'src/main.go') = %q, want %q", got, "src/main.go")
	}

	// With worktree: relative path resolved
	got = wm.ResolvePath("/main/.goa/worktrees/session1", "src/main.go")
	if got != "/main/.goa/worktrees/session1/src/main.go" {
		t.Errorf("ResolvePath with worktree = %q, want %q", got, "/main/.goa/worktrees/session1/src/main.go")
	}

	// Absolute path: unchanged even with worktree
	got = wm.ResolvePath("/main/.goa/worktrees/session1", "/absolute/path.go")
	if got != "/absolute/path.go" {
		t.Errorf("Absolute path should pass through: %q", got)
	}
}

// TestWorktreeManagerWorktreeDir verifies worktree directory path.
func TestWorktreeManagerWorktreeDir(t *testing.T) {
	wm := NewWorktreeManager("/project", WorktreeAlways)
	expected := "/project/.goa/worktrees"
	if wm.WorktreeDir() != expected {
		t.Errorf("WorktreeDir = %q, want %q", wm.WorktreeDir(), expected)
	}
}

// TestWorktreeManagerCurrentWorktree verifies active worktree tracking.
func TestWorktreeManagerCurrentWorktree(t *testing.T) {
	wm := NewWorktreeManager("/tmp", WorktreeAlways)

	// Initially empty
	if wm.CurrentWorktree() != "" {
		t.Error("CurrentWorktree should be empty initially")
	}
}

// TestWorktreeManagerList verifies listing with no worktrees.
func TestWorktreeManagerList(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "goa-worktree-test-*")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	wm := NewWorktreeManager(tmpDir, WorktreeAlways)

	// No worktrees should return empty list
	trees, err := wm.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(trees) != 0 {
		t.Errorf("Expected 0 worktrees, got %d", len(trees))
	}
}

// TestWorktreeManagerCleanup verifies cleanup with no worktrees doesn't error.
func TestWorktreeManagerCleanup(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "goa-worktree-cleanup-*")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	wm := NewWorktreeManager(tmpDir, WorktreeAlways)

	if err := wm.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
}
