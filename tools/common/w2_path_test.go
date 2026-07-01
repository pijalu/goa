// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package common

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/pijalu/goa/internal"
)

func TestResolveToolPath_ResolvesOutsidePaths(t *testing.T) {
	project := t.TempDir()
	wm := internal.NewWorktreeManager(project, internal.WorktreeAlways)

	cases := []struct {
		name string
		path string
	}{
		{"relative parent escape", filepath.Join("..", "escape.txt")},
		{"absolute outside root", "/etc/passwd"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolved, err := ResolveToolPath(wm, tc.path)
			if err != nil {
				t.Errorf("expected path resolution for %q, got error: %v", tc.path, err)
			}
			if resolved == "" {
				t.Errorf("expected non-empty resolved path for %q", tc.path)
			}
		})
	}
}

func TestResolveToolPath_NewFileUnderSymlinkedRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink test skipped on windows")
	}

	realDir := t.TempDir()
	linkDir := filepath.Join(realDir, "link")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	wm := internal.NewWorktreeManager(linkDir, internal.WorktreeAlways)
	target := filepath.Join(linkDir, "new.go")
	resolved, err := ResolveToolPath(wm, target)
	if err != nil {
		t.Fatalf("ResolveToolPath returned error: %v", err)
	}
	if resolved == "" {
		t.Fatal("expected non-empty resolved path")
	}
}
