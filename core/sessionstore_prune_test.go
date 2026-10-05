// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// seedSessionFile writes a session log with an explicit mtime.
func seedSessionFile(t *testing.T, dir, name string, age time.Duration, body []byte) string {
	t.Helper()
	sessionDir := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sessionDir, name)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("%s was removed, want it kept", filepath.Base(path))
	}
}

func mustBeGone(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("%s still exists, want it pruned", filepath.Base(path))
	}
}

// TestPruneSessions_RemovesOnlyExpiredLogs pins the window semantics the request
// asked for: a log whose last write is older than the window goes, one that is
// inside it stays. The boundary is judged against the cutoff taken at prune
// time, so the two cases sit just either side of it (a file seeded exactly at
// now-window is already strictly older by the time the sweep runs).
func TestPruneSessions_RemovesOnlyExpiredLogs(t *testing.T) {
	dir := t.TempDir()
	window := 7 * 24 * time.Hour
	outside := seedSessionFile(t, dir, "1700000000_old.jsonl", window+time.Minute, []byte("{}"))
	inside := seedSessionFile(t, dir, "1700000001_recent.jsonl", window-time.Minute, []byte("{}"))
	older := seedSessionFile(t, dir, "1700000002_older.jsonl", 30*24*time.Hour, []byte("{}"))

	if removed := NewSessionStore(dir).PruneSessions(window); removed != 2 {
		t.Errorf("PruneSessions removed %d logs, want the two outside the window", removed)
	}
	mustBeGone(t, outside)
	mustBeGone(t, older)
	mustExist(t, inside)
}

// TestPruneSessions_DisabledKeepsEverything: a zero (or negative) window is the
// documented "keep forever" setting, and it must not even read the directory.
func TestPruneSessions_DisabledKeepsEverything(t *testing.T) {
	dir := t.TempDir()
	old := seedSessionFile(t, dir, "1700000000_ancient.jsonl", 90*24*time.Hour, []byte("{}"))

	store := NewSessionStore(dir)
	if removed := store.PruneSessions(0); removed != 0 {
		t.Errorf("PruneSessions(0) removed %d logs, want none", removed)
	}
	if removed := store.PruneSessions(-time.Hour); removed != 0 {
		t.Errorf("PruneSessions(negative) removed %d logs, want none", removed)
	}
	mustExist(t, old)
}

// TestPruneSessions_SparesTheOpenSession is the guard the mtime alone cannot
// give: the writer keeps its file open for the whole run, so a long turn leaves
// an old mtime on a file that is very much in use.
func TestPruneSessions_SparesTheOpenSession(t *testing.T) {
	dir := t.TempDir()
	store := NewSessionStore(dir)
	id := store.StartSession()
	defer func() { _ = store.Close() }()

	active := filepath.Join(dir, "sessions", id+".jsonl")
	if _, err := os.Stat(active); err != nil {
		t.Fatalf("active session file missing: %v", err)
	}
	// Back-date it far past the window: only the active-session guard can keep
	// this file alive.
	when := time.Now().Add(-90 * 24 * time.Hour)
	if err := os.Chtimes(active, when, when); err != nil {
		t.Fatal(err)
	}
	other := seedSessionFile(t, dir, "1700000000_other.jsonl", 30*24*time.Hour, []byte("{}"))

	if removed := store.PruneSessions(7 * 24 * time.Hour); removed != 1 {
		t.Errorf("PruneSessions removed %d logs, want just the other one", removed)
	}
	mustExist(t, active)
	mustBeGone(t, other)
}

// TestPruneSessions_IgnoresNonSessionsAndMissingDir keeps the sweep from
// touching anything it does not own (the same directory holds the input history
// subdirectory), and from failing when the directory does not exist yet.
func TestPruneSessions_IgnoresNonSessionsAndMissingDir(t *testing.T) {
	dir := t.TempDir()
	inputs := filepath.Join(dir, "sessions", "inputs")
	if err := os.MkdirAll(inputs, 0o755); err != nil {
		t.Fatal(err)
	}
	notALog := filepath.Join(dir, "sessions", "notes.txt")
	if err := os.WriteFile(notALog, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-90 * 24 * time.Hour)
	if err := os.Chtimes(notALog, when, when); err != nil {
		t.Fatal(err)
	}

	if removed := NewSessionStore(dir).PruneSessions(24 * time.Hour); removed != 0 {
		t.Errorf("PruneSessions removed %d entries, want none", removed)
	}
	mustExist(t, notALog)
	if _, err := os.Stat(inputs); err != nil {
		t.Errorf("input history directory was touched: %v", err)
	}

	empty := NewSessionStore(t.TempDir())
	if removed := empty.PruneSessions(24 * time.Hour); removed != 0 {
		t.Errorf("PruneSessions on a store with no sessions dir removed %d, want 0", removed)
	}
}
