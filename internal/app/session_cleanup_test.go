// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
)

// seedAppSessionLog writes a session log in the project's .goa/sessions with an
// explicit mtime, the way a previous run would have left it.
func seedAppSessionLog(t *testing.T, projectDir, name string, age time.Duration) string {
	t.Helper()
	sessionDir := filepath.Join(projectDir, ".goa", "sessions")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sessionDir, name)
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestRunSessionLogCleanup_UsesTheConfiguredWindow drives the startup sweep
// through the real subsystems path with the real config type: an 8-day-old log is
// removed, a 6-day-old log survives, and the whole sweep is skipped when the
// retention flag is off — the two behaviours the request asked for.
func TestRunSessionLogCleanup_UsesTheConfiguredWindow(t *testing.T) {
	projectDir := t.TempDir()
	expired := seedAppSessionLog(t, projectDir, "1700000000_expired.jsonl", 8*24*time.Hour)
	recent := seedAppSessionLog(t, projectDir, "1700000001_recent.jsonl", 6*24*time.Hour)

	subs := &subsystems{
		projectDir:   projectDir,
		cfg:          &config.Config{}, // unset → the 7-day default
		sessionStore: core.NewSessionStore(filepath.Join(projectDir, ".goa")),
	}
	subs.runSessionLogCleanup()

	if _, err := os.Stat(expired); !os.IsNotExist(err) {
		t.Errorf("expired session log survived the default sweep (%v)", err)
	}
	if _, err := os.Stat(recent); err != nil {
		t.Errorf("recent session log was removed by the default sweep: %v", err)
	}

	off := false
	disabled := &subsystems{
		projectDir:   projectDir,
		cfg:          &config.Config{Sessions: config.SessionsConfig{Retention: config.SessionsRetentionConfig{Enabled: &off}}},
		sessionStore: core.NewSessionStore(filepath.Join(projectDir, ".goa")),
	}
	fresh := seedAppSessionLog(t, projectDir, "1700000002_old.jsonl", 30*24*time.Hour)
	disabled.runSessionLogCleanup()
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("session log was pruned with retention disabled: %v", err)
	}
}
