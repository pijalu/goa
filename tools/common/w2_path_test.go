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

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory available")
	}

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"bare tilde", "~", home},
		{"tilde slash", "~/dev/project/file.go", filepath.Join(home, "dev/project/file.go")},
		{"no tilde", "/etc/passwd", "/etc/passwd"},
		{"relative path", "src/main.go", "src/main.go"},
		{"tilde mid-path", "foo/~/bar", "foo/~/bar"},
		{"other user home left untouched", "~root/x", "~root/x"},
		{"empty", "", ""},
	}
	if runtime.GOOS == "windows" {
		// Backslash is a path separator only on Windows; on Unix a
		// backslash is an ordinary filename character and must not
		// trigger home expansion (matching shell behavior).
		cases = append(cases, struct {
			name string
			in   string
			want string
		}{"tilde backslash", `~\dev\file.go`, filepath.Join(home, "dev", "file.go")})
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExpandHome(tc.in); got != tc.want {
				t.Errorf("ExpandHome(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestResolveToolPath_ExpandsHomeDir is the regression test for the reported
// bug: `edit ~/dev/.../file.go` failed with file_not_found because "~" was
// never expanded to the user's home directory.
func TestResolveToolPath_ExpandsHomeDir(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory available")
	}

	project := t.TempDir()
	wm := internal.NewWorktreeManager(project, internal.WorktreeAlways)

	resolved, err := ResolveToolPath(wm, "~/dev/creaves.project/creaves-console/actions/dashboard.go")
	if err != nil {
		t.Fatalf("ResolveToolPath returned error: %v", err)
	}
	want := filepath.Join(home, "dev/creaves.project/creaves-console/actions/dashboard.go")
	if resolved != want {
		t.Errorf("resolved = %q, want %q", resolved, want)
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
