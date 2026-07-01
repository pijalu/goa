// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package perms

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSoloGuard_FileTool(t *testing.T) {
	base := t.TempDir()
	g := NewSoloGuard(base)

	if err := g.Validate("read", `{"path":"foo.txt"}`); err != nil {
		t.Errorf("expected read inside base to be allowed, got %v", err)
	}
	if err := g.Validate("write", `{"path":"/etc/passwd"}`); err == nil {
		t.Error("expected write outside base to be rejected")
	}
	if err := g.Validate("edit", `{"path":"../outside.txt"}`); err == nil {
		t.Error("expected edit outside base to be rejected")
	}
}

func TestSoloGuard_Bash(t *testing.T) {
	base := t.TempDir()
	g := NewSoloGuard(base)

	if err := g.Validate("bash", "ls -la"); err != nil {
		t.Errorf("expected simple ls to be allowed, got %v", err)
	}
	if err := g.Validate("bash", "cat foo.txt"); err != nil {
		t.Errorf("expected relative path to be allowed, got %v", err)
	}
	if err := g.Validate("bash", "cd /tmp"); err == nil {
		t.Error("expected cd outside base to be rejected")
	}
	if err := g.Validate("bash", "cat /etc/passwd"); err == nil {
		t.Error("expected absolute outside path to be rejected")
	}
}

func TestSoloGuard_Git(t *testing.T) {
	base := t.TempDir()
	g := NewSoloGuard(base)

	if err := g.Validate("git", "commit -m 'x'"); err != nil {
		t.Errorf("expected git commit to be allowed, got %v", err)
	}
	if err := g.Validate("git", "diff"); err != nil {
		t.Errorf("expected git diff to be allowed, got %v", err)
	}
	if err := g.Validate("git", "push origin main"); err == nil {
		t.Error("expected git push to be rejected")
	}
	if err := g.Validate("git", "checkout main"); err == nil {
		t.Error("expected git checkout to be rejected")
	}
}

func TestSoloGuard_AllowsRelativePathsInsideBase(t *testing.T) {
	base := t.TempDir()
	sub := filepath.Join(base, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	g := NewSoloGuard(base)

	if err := g.Validate("read", `{"path":"sub/file.txt"}`); err != nil {
		t.Errorf("expected subpath to be allowed, got %v", err)
	}
}
