// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
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

// TestSessionStartDistinctIDs covers Issue 8: every new session must get a
// FRESH conversation id so provider-side cache (prompt_cache_key / previous_response_id /
// session-affinity) is cleared and the new conversation does not inherit stale affinity.
func TestSessionStartDistinctIDs(t *testing.T) {
	dir, cleanup := setupTestSession(t)
	defer cleanup()

	ss := NewSessionStore(dir)
	id1 := ss.StartSession()
	id2 := ss.StartSession()
	if id1 == "" || id2 == "" {
		t.Fatalf("StartSession returned empty id: %q, %q", id1, id2)
	}
	if id1 == id2 {
		t.Fatalf("consecutive StartSession calls reused id %q; cache would not be cleared", id1)
	}
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
	ss.WriteEvent(agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.User, Text: "Hello"})
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

// findCompactEvent returns the first compact event in the loaded session log,
// or nil when none was written.
func findCompactEvent(events []agentic.OutputEvent) *agentic.OutputEvent {
	for i := range events {
		if events[i].Type == agentic.EventCompact {
			return &events[i]
		}
	}
	return nil
}

// TestSessionCompactEventRoundTrip verifies an EventCompact carrying the
// structured CompactionInfo payload survives the JSONL write/read round-trip,
// so the session log documents compressions ("context compressions
// are invisible").
func TestSessionCompactEventRoundTrip(t *testing.T) {
	dir, cleanup := setupTestSession(t)
	defer cleanup()

	ss := NewSessionStore(dir)
	ss.StartSession()
	// A real session mixes conversation content with compact events; a lone
	// compact event (no user/assistant turn) is hidden from listings as an
	// empty transcript, so seed one user message first.
	ss.WriteEvent(agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.User, Text: "hi"})
	wantCompaction := &agentic.CompactionInfo{
		Strategy:    "ceiling",
		BeforePct:   95,
		AfterPct:    43,
		FreedTokens: 105689,
		Removed:     238,
	}
	ss.WriteEvent(agentic.OutputEvent{Type: agentic.EventCompact, Text: "ceiling", Compaction: wantCompaction})
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
	events, err := ss.LoadSession(sessions[0].Name)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	got := findCompactEvent(events)
	if got == nil {
		t.Fatal("no compact event found after round-trip")
	}
	if got.Compaction == nil {
		t.Fatal("Compaction payload did not survive the JSONL round-trip")
	}
	// The structured payload must survive byte-for-byte semantics, not just
	// the free-text summary.
	if !reflect.DeepEqual(got.Compaction, wantCompaction) {
		t.Errorf("Compaction = %+v, want ceiling 95→43 freed=105689 removed=238", got.Compaction)
	}
}

// TestSessionCompactionProvenanceRoundTrip covers the CX4 durability
// contract: the compaction provenance triple (compaction_start →
// compaction_summary → compaction_end) rides the same observer→SessionStore
// pipeline as every other agent event, so it must survive the JSONL
// write/read round-trip with the CompactionTx payload (shared id, shadowed
// range, provider/model, usage) intact. A second, deliberately unterminated
// transaction (the process "killed" between start and end) must be flagged
// by FindOrphanedCompactions on the reloaded log — the next-boot orphan
// detection.
func TestSessionCompactionProvenanceRoundTrip(t *testing.T) {
	dir, cleanup := setupTestSession(t)
	defer cleanup()

	ss := NewSessionStore(dir)
	ss.StartSession()
	ss.WriteEvent(agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.User, Text: "hi"})

	// Completed transaction.
	ss.WriteEvent(agentic.OutputEvent{Type: agentic.EventCompactionStart, CompactionTx: &agentic.CompactionTx{ID: "cx-done-1"}})
	ss.WriteEvent(agentic.OutputEvent{Type: agentic.EventCompactionSummary, CompactionTx: &agentic.CompactionTx{
		ID:             "cx-done-1",
		ShadowedEnd:    42,
		ShadowedCount:  42,
		ShadowedTokens: 12345,
		FreedTokens:    12000,
		Provider:       "deepseek",
		Model:          "deepseek-v4",
		Usage:          &agenticprovider.Usage{InputTokens: 12300, OutputTokens: 345},
	}})
	ss.WriteEvent(agentic.OutputEvent{Type: agentic.EventCompactionEnd, CompactionTx: &agentic.CompactionTx{ID: "cx-done-1"}})

	// Orphaned transaction: start (and summary) but no end — the crash
	// window. No end is ever written for cx-crashed-2.
	ss.WriteEvent(agentic.OutputEvent{Type: agentic.EventCompactionStart, CompactionTx: &agentic.CompactionTx{ID: "cx-crashed-2"}})
	ss.WriteEvent(agentic.OutputEvent{Type: agentic.EventCompactionSummary, CompactionTx: &agentic.CompactionTx{ID: "cx-crashed-2", ShadowedCount: 42}})

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
	events, err := ss.LoadSession(sessions[0].Name)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}

	assertReloadedProvenanceTriple(t, events)
	assertReloadedProvenanceOrphans(t, events)
}

// assertReloadedProvenanceTriple checks the completed transaction survived
// the JSONL round-trip in order with its CompactionTx payload intact.
func assertReloadedProvenanceTriple(t *testing.T, events []agentic.OutputEvent) {
	t.Helper()
	var triple []agentic.OutputEvent
	for _, ev := range events {
		if ev.CompactionTx != nil && ev.CompactionTx.ID == "cx-done-1" {
			triple = append(triple, ev)
		}
	}
	if len(triple) != 3 {
		t.Fatalf("reloaded log holds %d events for the completed transaction, want 3", len(triple))
	}
	wantTypes := []agentic.EventType{agentic.EventCompactionStart, agentic.EventCompactionSummary, agentic.EventCompactionEnd}
	for i, want := range wantTypes {
		if triple[i].Type != want {
			t.Errorf("triple[%d] type = %q after round-trip, want %q", i, triple[i].Type, want)
		}
	}
	assertReloadedProvenanceFacts(t, triple[1].CompactionTx)
}

// assertReloadedProvenanceFacts checks the compaction_summary facts and
// usage survived the JSONL round-trip.
func assertReloadedProvenanceFacts(t *testing.T, sum *agentic.CompactionTx) {
	t.Helper()
	if sum.ShadowedEnd != 42 || sum.ShadowedCount != 42 || sum.ShadowedTokens != 12345 ||
		sum.FreedTokens != 12000 || sum.Provider != "deepseek" || sum.Model != "deepseek-v4" {
		t.Errorf("compaction_summary facts did not survive the round-trip: %+v", sum)
	}
	if sum.Usage == nil || sum.Usage.InputTokens != 12300 || sum.Usage.OutputTokens != 345 {
		t.Errorf("compaction_summary usage did not survive the round-trip: %+v", sum.Usage)
	}
}

// assertReloadedProvenanceOrphans checks next-boot orphan detection on the
// reloaded log: only the killed transaction is flagged.
func assertReloadedProvenanceOrphans(t *testing.T, events []agentic.OutputEvent) {
	t.Helper()
	orphans := agentic.FindOrphanedCompactions(events)
	if len(orphans) != 1 || orphans[0] != "cx-crashed-2" {
		t.Errorf("FindOrphanedCompactions on the reloaded log = %v, want [cx-crashed-2]", orphans)
	}
}
func TestSessionMultipleSessions(t *testing.T) {
	dir, cleanup := setupTestSession(t)
	defer cleanup()

	ss := NewSessionStore(dir)

	ss.StartSession()
	ss.WriteEvent(agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.User, Text: "first"})
	if err := ss.Close(); err != nil {
		t.Fatalf("Close first session: %v", err)
	}

	ss.StartSession()
	ss.WriteEvent(agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.User, Text: "second"})
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

// TestSessionListSessions_FiltersEmptySessions verifies that sessions with no
// user/assistant conversation (only system/stats/progress events, or none)
// are hidden from listings — restoring them would show a blank transcript.
func TestSessionListSessions_FiltersEmptySessions(t *testing.T) {
	dir, cleanup := setupTestSession(t)
	defer cleanup()

	ss := NewSessionStore(dir)

	// Empty-ish session: stats + system notification only, no conversation.
	ss.StartSession()
	ss.WriteEvent(agentic.OutputEvent{Type: agentic.EventProgress, Text: "Sending request..."})
	ss.WriteEvent(agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.System, Text: "note"})
	if err := ss.Close(); err != nil {
		t.Fatalf("Close empty session: %v", err)
	}

	// Real conversation: user text + assistant reply.
	ss.StartSession()
	ss.WriteEvent(agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.User, Text: "hi"})
	ss.WriteEvent(agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.Assistant, Text: "hello"})
	if err := ss.Close(); err != nil {
		t.Fatalf("Close conversation session: %v", err)
	}

	sessions, err := ss.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("Expected 1 listed session (empty filtered out), got %d", len(sessions))
	}
	if sessions[0].FirstMessage != "hi" {
		t.Errorf("listed session should be the conversation, got FirstMessage %q", sessions[0].FirstMessage)
	}
}

// TestSessionListSessions_HasModelTurn verifies the model-turn marker: a
// session with a user message but no assistant reply is still listed (the
// store keeps it for export/dream flows) but reports HasModelTurn=false, so
// the /session picker can hide it ("Session command: must not list
// sessions without an actual model turn").
func TestSessionListSessions_HasModelTurn(t *testing.T) {
	dir, cleanup := setupTestSession(t)
	defer cleanup()

	ss := NewSessionStore(dir)

	// User-only session (model call failed / abandoned before first reply).
	ss.StartSession()
	ss.WriteEvent(agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.User, Text: "hi"})
	if err := ss.Close(); err != nil {
		t.Fatalf("Close user-only session: %v", err)
	}

	// Full turn: user + assistant.
	ss.StartSession()
	ss.WriteEvent(agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.User, Text: "hi"})
	ss.WriteEvent(agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.Assistant, Text: "hello"})
	if err := ss.Close(); err != nil {
		t.Fatalf("Close full-turn session: %v", err)
	}

	sessions, err := ss.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected both sessions listed at store level, got %d", len(sessions))
	}
	var userOnly, fullTurn *bool
	for _, s := range sessions {
		if s.EventCount == 1 {
			v := s.HasModelTurn
			userOnly = &v
		}
		if s.EventCount == 2 {
			v := s.HasModelTurn
			fullTurn = &v
		}
	}
	if userOnly == nil || fullTurn == nil {
		t.Fatalf("could not identify sessions: %+v", sessions)
	}
	if *userOnly {
		t.Errorf("user-only session must report HasModelTurn=false")
	}
	if !*fullTurn {
		t.Errorf("full-turn session must report HasModelTurn=true")
	}
}

// TestSessionListSessions_NewestFirst verifies restore listings are ordered
// from most recent to oldest, with a deterministic tiebreak.
func TestSessionListSessions_NewestFirst(t *testing.T) {
	dir, cleanup := setupTestSession(t)
	defer cleanup()

	ss := NewSessionStore(dir)
	names := []string{"aaa", "bbb", "ccc"}
	for _, n := range names {
		ss.StartSession()
		ss.WriteEvent(agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.User, Text: n})
		if err := ss.Close(); err != nil {
			t.Fatalf("Close session %s: %v", n, err)
		}
	}

	// Pin distinct ModTimes so ordering does not depend on filesystem
	// timestamp granularity: first-written = oldest, last-written = newest.
	sessionDir := filepath.Join(dir, "sessions")
	entries, err := os.ReadDir(sessionDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	base := time.Now().Add(-time.Hour)
	for i, e := range entries {
		p := filepath.Join(sessionDir, e.Name())
		if err := os.Chtimes(p, base.Add(time.Duration(i)*time.Minute), base.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("Chtimes: %v", err)
		}
	}

	sessions, err := ss.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 3 {
		t.Fatalf("Expected 3 sessions, got %d", len(sessions))
	}
	for i := 1; i < len(sessions); i++ {
		if sessions[i-1].Date.Before(sessions[i].Date) {
			t.Errorf("sessions not newest-first: [%d]=%v before [%d]=%v",
				i-1, sessions[i-1].Date, i, sessions[i].Date)
		}
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

func TestSessionSaveCurrent_Empty(t *testing.T) {
	dir, cleanup := setupTestSession(t)
	defer cleanup()

	ss := NewSessionStore(dir)
	ss.StartSession()
	// No events written.

	err := ss.SaveCurrent("empty-session")
	if !errors.Is(err, ErrEmptySession) {
		t.Fatalf("SaveCurrent empty session = %v, want ErrEmptySession", err)
	}

	// No file should be left behind.
	sessionDir := filepath.Join(dir, "sessions")
	entries, err := os.ReadDir(sessionDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected no session files, got %d", len(entries))
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

// TestSessionWriteEvent_SkipsToolCallDeltas guards against SESSION-BUG-1:
// streamed tool-call deltas carry the full accumulated arguments, so persisting
// them for a single streamed call writes the same content many times and
// creates a session file that grows quadratically (observed: 6.4GB).
// Only the final completed tool-call event needs to be replayed.
func TestSessionWriteEvent_SkipsToolCallDeltas(t *testing.T) {
	dir, cleanup := setupTestSession(t)
	defer cleanup()

	ss := NewSessionStore(dir)
	ss.StartSession()

	// Content deltas are small and must still be persisted for replay.
	ss.WriteEvent(agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.Assistant, Text: "hello", IsDelta: true})
	// The completed tool-call event is the one we need to replay.
	ss.WriteEvent(agentic.OutputEvent{
		Type:      agentic.EventToolCall,
		ToolName:  "bash",
		ToolInput: `{"command":"ls"}`,
		IsDelta:   false,
	})
	// Streaming tool-call deltas must NOT be persisted.
	ss.WriteEvent(agentic.OutputEvent{
		Type:      agentic.EventToolCall,
		ToolName:  "write",
		ToolInput: strings.Repeat("x", 100000),
		IsDelta:   true,
	})

	if err := ss.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	sessions, err := ss.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].EventCount != 2 {
		t.Errorf("EventCount = %d, want 2 (tool-call delta not persisted)", sessions[0].EventCount)
	}

	events, err := ss.LoadSession(sessions[0].Name)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Type != agentic.EventContent {
		t.Errorf("events[0].Type = %v, want EventContent", events[0].Type)
	}
	if events[1].Type != agentic.EventToolCall || events[1].IsDelta {
		t.Errorf("events[1] should be a completed tool call, got %+v", events[1])
	}
}
