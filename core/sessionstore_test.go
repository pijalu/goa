// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
)

// setupTestSession creates a temporary directory for session testing.
func setupTestSession(t *testing.T) (dir string, cleanup func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "goa-session-test-*")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	return dir, func() { os.RemoveAll(dir) }
}

// TestSessionStartAndWrite verifies session creation and event writing.
func TestSessionStartAndWrite(t *testing.T) {
	dir, cleanup := setupTestSession(t)
	defer cleanup()

	ss := NewSessionStore(dir)
	sessionID := ss.StartSession()
	if sessionID == "" {
		t.Fatal("StartSession returned empty ID")
	}

	// Write an event
	ss.WriteEvent(agentic.OutputEvent{
		Type: agentic.EventContent,
		Text: "Hello",
	})

	// Verify file exists
	sessionDir := filepath.Join(dir, "sessions")
	entries, err := os.ReadDir(sessionDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("Expected 1 session file, got %d", len(entries))
	}

	// Close flushes the async writer.
	if err := ss.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestSessionListAndLoad verifies session listing and loading.
func TestSessionListAndLoad(t *testing.T) {
	dir, cleanup := setupTestSession(t)
	defer cleanup()

	ss := NewSessionStore(dir)
	ss.StartSession()
	ss.WriteEvent(agentic.OutputEvent{Type: agentic.EventContent, Text: "Hello"})
	ss.WriteEvent(agentic.OutputEvent{Type: agentic.EventEnd})

	if err := ss.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	sessions, err := ss.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("Expected 1 session, got %d", len(sessions))
	}

	if sessions[0].EventCount != 2 {
		t.Errorf("EventCount = %d, want 2", sessions[0].EventCount)
	}

	// Load the session
	events, err := ss.LoadSession(sessions[0].Name)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("Expected 2 events, got %d", len(events))
	}
	if events[0].Text != "Hello" {
		t.Errorf("Event[0].Text = %q, want %q", events[0].Text, "Hello")
	}
}

// TestSessionMultipleSessions verifies multiple sessions are independent.
func TestSessionMultipleSessions(t *testing.T) {
	dir, cleanup := setupTestSession(t)
	defer cleanup()

	ss := NewSessionStore(dir)

	ss.StartSession()
	ss.WriteEvent(agentic.OutputEvent{Type: agentic.EventEnd})
	if err := ss.Close(); err != nil {
		t.Fatalf("Close first session: %v", err)
	}

	ss.StartSession()
	ss.WriteEvent(agentic.OutputEvent{Type: agentic.EventEnd})
	if err := ss.Close(); err != nil {
		t.Fatalf("Close second session: %v", err)
	}

	sessions, err := ss.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("Expected 2 sessions, got %d", len(sessions))
	}
}

// TestSessionStartSession_LogsCreationError verifies that a failing session
// file creation is reported through the configured logger instead of being
// silently swallowed.
func TestSessionStartSession_LogsCreationError(t *testing.T) {
	dir, cleanup := setupTestSession(t)
	defer cleanup()

	// Create a regular file where StartSession expects the sessions directory.
	if err := os.WriteFile(filepath.Join(dir, "sessions"), []byte("block"), 0644); err != nil {
		t.Fatalf("setup block file: %v", err)
	}

	var buf strings.Builder
	stdLogger := log.New(&buf, "", 0)
	logger := agentic.NewLoggerWithStdLogger(stdLogger, agentic.Error)

	ss := NewSessionStore(dir)
	ss.SetLogger(logger)
	ss.StartSession()

	output := buf.String()
	if !strings.Contains(output, "create session file") {
		t.Fatalf("expected logger to report session file creation error, got %q", output)
	}
}

// TestSessionNoDir verifies session store handles missing directories.
func TestSessionNoDir(t *testing.T) {
	dir, cleanup := setupTestSession(t)
	defer cleanup()

	ss := NewSessionStore(dir)
	// Before any session, list should return empty
	sessions, err := ss.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("Expected 0 sessions, got %d", len(sessions))
	}
}

// TestRandomID_Uniqueness guards against CORE-BUG-6: the previous LCG seeded
// only by time.Now().UnixNano() produced identical IDs when two calls landed
// in the same nanosecond. The crypto/rand-backed generator must not collide.
func TestRandomID_Uniqueness(t *testing.T) {
	const n = 5000
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		id := randomID(10)
		if len(id) != 10 {
			t.Fatalf("randomID length = %d, want 10", len(id))
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("collision after %d IDs: %q", i, id)
		}
		seen[id] = struct{}{}
	}
}

// TestStartSession_LogsMkdirError guards against CORE-BUG-8: os.MkdirAll's
// error was discarded; it must now be routed through the logger so a
// misconfigured/locked project dir is diagnosable.
func TestStartSession_LogsMkdirError(t *testing.T) {
	dir, cleanup := setupTestSession(t)
	defer cleanup()

	// Place a regular file where StartSession needs the "sessions" directory.
	blockPath := filepath.Join(dir, "sessions")
	if err := os.WriteFile(blockPath, []byte("block"), 0644); err != nil {
		t.Fatalf("setup block file: %v", err)
	}

	var buf strings.Builder
	stdLogger := log.New(&buf, "", 0)
	logger := agentic.NewLoggerWithStdLogger(stdLogger, agentic.Error)

	ss := NewSessionStore(dir)
	ss.SetLogger(logger)
	ss.StartSession()

	output := buf.String()
	if !strings.Contains(output, "create sessions dir") {
		t.Fatalf("expected logger to report 'create sessions dir', got %q", output)
	}
}

// TestStartSession_RotationClosesPriorWriter guards CORE-BUG-9: when starting
// a new session, the previous writer must be closed (not leaked) and any close
// error routed through the logger rather than discarded via `_ =`. This test
// exercises the rotation path end-to-end; the prior writer closes cleanly here,
// and events continue to flow to the new writer.
func TestStartSession_RotationClosesPriorWriter(t *testing.T) {
	dir, cleanup := setupTestSession(t)
	defer cleanup()

	ss := NewSessionStore(dir)
	first := ss.StartSession()
	ss.WriteEvent(agentic.OutputEvent{Type: agentic.EventContent, Text: "first"})

	second := ss.StartSession()
	if second == first {
		t.Fatal("StartSession returned the same ID on rotation")
	}
	ss.WriteEvent(agentic.OutputEvent{Type: agentic.EventEnd})
	if err := ss.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Both session files should exist and be independently loadable.
	firstEvents, err := ss.LoadSession(first)
	if err != nil {
		t.Fatalf("LoadSession(first): %v", err)
	}
	if len(firstEvents) != 1 || firstEvents[0].Text != "first" {
		t.Errorf("first session events = %+v", firstEvents)
	}
	secondEvents, err := ss.LoadSession(second)
	if err != nil {
		t.Fatalf("LoadSession(second): %v", err)
	}
	if len(secondEvents) != 1 || secondEvents[0].Type != agentic.EventEnd {
		t.Errorf("second session events = %+v", secondEvents)
	}
}
