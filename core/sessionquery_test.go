// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic"
)

// writeSessionFile writes a raw JSONL session file directly so tests can
// control exact line numbers (seq) and content without going through the
// async writer.
func writeSessionFile(t *testing.T, dir, sessionID string, events []agentic.OutputEvent) {
	t.Helper()
	sessionDir := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	path := filepath.Join(sessionDir, sessionID+".jsonl")
	var b strings.Builder
	for _, ev := range events {
		data, err := json.Marshal(ev)
		if err != nil {
			t.Fatalf("marshal event: %v", err)
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestListSessionIDs_NewestFirst(t *testing.T) {
	dir, cleanup := setupTestSession(t)
	defer cleanup()

	ss := NewSessionStore(dir)

	// Create session files with distinct mtimes by writing and touching.
	writeSessionFile(t, dir, "old_session", []agentic.OutputEvent{{Type: agentic.EventContent, Text: "old"}})
	writeSessionFile(t, dir, "new_session", []agentic.OutputEvent{{Type: agentic.EventContent, Text: "new"}})

	oldPath := filepath.Join(dir, "sessions", "old_session.jsonl")
	oldTime := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	ids, err := ss.ListSessionIDs()
	if err != nil {
		t.Fatalf("ListSessionIDs: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 sessions, got %d: %v", len(ids), ids)
	}
	if ids[0] != "new_session" || ids[1] != "old_session" {
		t.Fatalf("expected newest first [new_session old_session], got %v", ids)
	}
}

func TestListSessionIDs_MissingDirReturnsEmpty(t *testing.T) {
	dir, cleanup := setupTestSession(t)
	defer cleanup()

	ss := NewSessionStore(filepath.Join(dir, "nonexistent"))
	ids, err := ss.ListSessionIDs()
	if err != nil {
		t.Fatalf("ListSessionIDs: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("expected no sessions, got %v", ids)
	}
}

func TestListSessionIDs_IgnoresNonSessionFiles(t *testing.T) {
	dir, cleanup := setupTestSession(t)
	defer cleanup()

	ss := NewSessionStore(dir)
	writeSessionFile(t, dir, "real_session", []agentic.OutputEvent{{Type: agentic.EventContent, Text: "x"}})
	// Non-jsonl files and directories must be ignored.
	os.WriteFile(filepath.Join(dir, "sessions", "notes.txt"), []byte("nope"), 0644)
	os.MkdirAll(filepath.Join(dir, "sessions", "subdir"), 0755)

	ids, err := ss.ListSessionIDs()
	if err != nil {
		t.Fatalf("ListSessionIDs: %v", err)
	}
	if len(ids) != 1 || ids[0] != "real_session" {
		t.Fatalf("expected only [real_session], got %v", ids)
	}
}

func TestScanSessionEvents_Sequential(t *testing.T) {
	dir, cleanup := setupTestSession(t)
	defer cleanup()

	ss := NewSessionStore(dir)
	writeSessionFile(t, dir, "s1", []agentic.OutputEvent{
		{Type: agentic.EventContent, Role: agentic.User, Text: "hello"},
		{Type: agentic.EventContent, Role: agentic.Assistant, Text: "world"},
		{Type: agentic.EventToolCall, ToolName: "bash", ToolInput: `{"command":"ls"}`},
	})

	var got []int
	seqs := make(map[int]string)
	count, err := ss.ScanSessionEvents("s1", func(seq int, ev agentic.OutputEvent) bool {
		got = append(got, seq)
		seqs[seq] = ev.Text + ev.ToolName
		return true
	})
	if err != nil {
		t.Fatalf("ScanSessionEvents: %v", err)
	}
	if count != 3 {
		t.Fatalf("expected 3 events, got %d", count)
	}
	if len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Fatalf("expected seqs [1 2 3], got %v", got)
	}
	if seqs[2] != "world" {
		t.Fatalf("expected seq 2 text 'world', got %q", seqs[2])
	}
	if seqs[3] != "bash" {
		t.Fatalf("expected seq 3 tool bash, got %q", seqs[3])
	}
}

func TestScanSessionEvents_StopsEarly(t *testing.T) {
	dir, cleanup := setupTestSession(t)
	defer cleanup()

	ss := NewSessionStore(dir)
	writeSessionFile(t, dir, "s1", []agentic.OutputEvent{
		{Type: agentic.EventContent, Text: "a"},
		{Type: agentic.EventContent, Text: "b"},
		{Type: agentic.EventContent, Text: "c"},
	})

	count, err := ss.ScanSessionEvents("s1", func(seq int, ev agentic.OutputEvent) bool {
		return seq < 2
	})
	if err != nil {
		t.Fatalf("ScanSessionEvents: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected scan to stop at 2, got %d", count)
	}
}

func TestScanSessionEvents_MissingSession(t *testing.T) {
	dir, cleanup := setupTestSession(t)
	defer cleanup()

	ss := NewSessionStore(dir)
	_, err := ss.ScanSessionEvents("nope", func(seq int, ev agentic.OutputEvent) bool { return true })
	if err == nil {
		t.Fatal("expected error for missing session")
	}
}

func TestScanSessionEvents_SkipsBadLines(t *testing.T) {
	dir, cleanup := setupTestSession(t)
	defer cleanup()

	sessionDir := filepath.Join(dir, "sessions")
	os.MkdirAll(sessionDir, 0755)
	content := "not json\n" +
		`{"Type":"content","Role":"user","Text":"valid"}` + "\n"
	if err := os.WriteFile(filepath.Join(sessionDir, "s1.jsonl"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	ss := NewSessionStore(dir)
	var got []string
	count, err := ss.ScanSessionEvents("s1", func(seq int, ev agentic.OutputEvent) bool {
		got = append(got, ev.Text)
		return true
	})
	if err != nil {
		t.Fatalf("ScanSessionEvents: %v", err)
	}
	// The bad line occupies seq 1 (not visited); the valid line is seq 2.
	if count != 2 {
		t.Fatalf("expected 2 lines scanned, got %d", count)
	}
	if len(got) != 1 || got[0] != "valid" {
		t.Fatalf("expected only the valid line to be visited, got %v", got)
	}
}

func TestSessionModifiedTime(t *testing.T) {
	dir, cleanup := setupTestSession(t)
	defer cleanup()

	ss := NewSessionStore(dir)
	writeSessionFile(t, dir, "s1", []agentic.OutputEvent{{Type: agentic.EventContent, Text: "x"}})

	mt, ok := ss.SessionModifiedTime("s1")
	if !ok {
		t.Fatal("expected session s1 to exist")
	}
	if mt.IsZero() {
		t.Fatal("expected non-zero mtime")
	}

	if _, ok := ss.SessionModifiedTime("missing"); ok {
		t.Fatal("expected missing session to report false")
	}
}

func TestSessionFilePath_RejectsTraversal(t *testing.T) {
	dir, cleanup := setupTestSession(t)
	defer cleanup()

	ss := NewSessionStore(dir)
	for _, evil := range []string{"../evil", "a/b", "..", "", `..\evil`} {
		if path := ss.sessionFilePath(evil); path != "" {
			t.Fatalf("expected traversal id %q to be rejected, got path %q", evil, path)
		}
	}
	if path := ss.sessionFilePath("1786527447_1bsonzb9"); path == "" {
		t.Fatal("expected valid id to produce a path")
	}
}
