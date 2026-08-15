// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package common

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSpillStore_Save_CreatesFileInSessionDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spill", "session-1")
	store := NewSpillStore(dir)

	content := "full oversized tool result body"
	path, err := store.Save("bash", content)
	if err != nil {
		t.Fatalf("Save should succeed: %v", err)
	}
	if !strings.HasPrefix(path, dir+string(os.PathSeparator)) {
		t.Errorf("spill path %q should live under session dir %q", path, dir)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("spill file should be readable: %v", err)
	}
	if string(got) != content {
		t.Errorf("spill file must hold the content verbatim, got %q", string(got))
	}
}

func TestSpillStore_Save_UniqueNames(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spill", "session-1")
	store := NewSpillStore(dir)

	p1, err := store.Save("bash", "first")
	if err != nil {
		t.Fatalf("first Save failed: %v", err)
	}
	p2, err := store.Save("bash", "second")
	if err != nil {
		t.Fatalf("second Save failed: %v", err)
	}
	if p1 == p2 {
		t.Errorf("two spills of the same tool must not collide: %q", p1)
	}
}

func TestSpillStore_Save_PrivatePermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spill", "session-1")
	store := NewSpillStore(dir)

	path, err := store.Save("bash", "secret output")
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat spill file: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("spill file should be owner-only (0600), got %o", perm)
	}
	di, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat spill dir: %v", err)
	}
	if perm := di.Mode().Perm(); perm != 0o700 {
		t.Errorf("spill dir should be private (0700), got %o", perm)
	}
}

func TestSpillStore_Save_SanitizesSuggestedName(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spill", "session-1")
	store := NewSpillStore(dir)

	// A hostile suggested name must not escape the session dir.
	path, err := store.Save("../../etc/passwd", "payload")
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	if !strings.HasPrefix(path, dir+string(os.PathSeparator)) {
		t.Errorf("spill path %q escaped session dir %q", path, dir)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("spill file should exist: %v", err)
	}
}

func TestSpillStore_Save_EmptyDirFails(t *testing.T) {
	store := NewSpillStore("")
	if _, err := store.Save("bash", "x"); err == nil {
		t.Error("Save with empty dir should fail so callers keep the inline result")
	}
}

func TestSessionSpillDir(t *testing.T) {
	got := SessionSpillDir("/home/u/.goa", "123_abc")
	want := filepath.Join("/home/u/.goa", "spill", "123_abc")
	if got != want {
		t.Errorf("SessionSpillDir = %q, want %q", got, want)
	}
	// Path-traversal session IDs are neutralized to a single safe segment.
	got = SessionSpillDir("/home/u/.goa", "../../evil")
	seg := filepath.Base(got)
	if seg == ".." || seg == "." || strings.ContainsRune(seg, os.PathSeparator) {
		t.Errorf("SessionSpillDir segment must be traversal-safe, got %q (full %q)", seg, got)
	}
	if !strings.HasPrefix(got, filepath.Join("/home/u/.goa", "spill")+string(os.PathSeparator)) {
		t.Errorf("SessionSpillDir escaped spill root: %q", got)
	}
}
