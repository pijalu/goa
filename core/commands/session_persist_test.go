// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/agentic"
)

func TestListSessions_NoStore(t *testing.T) {
	w := newWriter()
	err := listSessions(w, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(w.Text(), "Session store not available") {
		t.Errorf("expected store-unavailable message, got: %s", w.Text())
	}
}

func TestListSessions_Empty(t *testing.T) {
	w := newWriter()
	store := newSessionStore(nil)

	err := listSessions(w, store)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(w.Text(), "No saved sessions found") {
		t.Errorf("expected empty message, got: %s", w.Text())
	}
}

func TestListSessions_WithSessions(t *testing.T) {
	w := newWriter()
	store := newSessionStore([]core.SessionInfo{
		{Name: "work-v2", Date: time.Date(2026, 6, 15, 10, 30, 0, 0, time.UTC), EventCount: 42, TokenTotal: 5000},
		{Name: "debug-session", Date: time.Date(2026, 6, 14, 14, 0, 0, 0, time.UTC), EventCount: 10, TokenTotal: 1200},
	})

	err := listSessions(w, store)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := w.Text()
	if !strings.Contains(text, "Available sessions:") {
		t.Errorf("expected header, got: %s", text)
	}
	if !strings.Contains(text, "work-v2") {
		t.Errorf("expected work-v2, got: %s", text)
	}
	if !strings.Contains(text, "debug-session") {
		t.Errorf("expected debug-session, got: %s", text)
	}
	if !strings.Contains(text, "42 events") {
		t.Errorf("expected event count, got: %s", text)
	}
}

func TestListSessions_Error(t *testing.T) {
	w := newWriter()
	store := newSessionStore(nil)
	store.err = assertError("disk full")

	err := listSessions(w, store)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(w.Text(), "Error listing sessions") {
		t.Errorf("expected error message, got: %s", w.Text())
	}
}

func TestRestoreSession_NoStore(t *testing.T) {
	w := newWriter()
	es := &fakeEventSink{}

	err := restoreSession(w, es, nil, "my-session")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(w.Text(), "Session store not available") {
		t.Errorf("expected store-unavailable message, got: %s", w.Text())
	}
}

func TestRestoreSession_Success(t *testing.T) {
	w := newWriter()
	es := &fakeEventSink{}
	store := newSessionStore(nil)
	store.AddEvents("my-session", []agentic.OutputEvent{
		{Type: agentic.EventContent}, {Type: agentic.EventToolResult},
	})

	err := restoreSession(w, es, store, "my-session")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := w.Text()
	if !strings.Contains(text, "Restored session 'my-session'") {
		t.Errorf("expected restore message, got: %s", text)
	}
	if !strings.Contains(text, "2 events") {
		t.Errorf("expected event count, got: %s", text)
	}
	if len(es.flashes) != 2 {
		t.Errorf("expected 2 flashes, got %d: %v", len(es.flashes), es.flashes)
	}
}

func TestRestoreSession_LoadError(t *testing.T) {
	w := newWriter()
	es := &fakeEventSink{}
	store := newSessionStore(nil)
	store.err = assertError("corrupt file")

	err := restoreSession(w, es, store, "bad-session")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(w.Text(), "Error loading session") {
		t.Errorf("expected error message, got: %s", w.Text())
	}
}

func TestShowSessionPicker_NoStore(t *testing.T) {
	w := newWriter()
	es := &fakeEventSink{}
	sel := newSelector("", false)

	err := showSessionPicker(w, es, sel, nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(w.Text(), "Session store not available") {
		t.Errorf("expected store-unavailable message, got: %s", w.Text())
	}
}

func TestShowSessionPicker_Empty(t *testing.T) {
	w := newWriter()
	es := &fakeEventSink{}
	sel := newSelector("", false)
	store := newSessionStore(nil)

	err := showSessionPicker(w, es, sel, store, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(w.Text(), "No saved sessions found") {
		t.Errorf("expected empty message, got: %s", w.Text())
	}
}

func TestShowSessionPicker_DeleteCancel(t *testing.T) {
	w := newWriter()
	es := &fakeEventSink{}
	sel := newSelector("", false) // ok=false → cancelled
	store := newSessionStore([]core.SessionInfo{{Name: "my-session", EventCount: 5}})

	err := showSessionPicker(w, es, sel, store, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(es.flashes) != 0 {
		t.Errorf("expected no flashes on cancel, got: %v", es.flashes)
	}
}

func TestBuildSessionItems(t *testing.T) {
	sessions := []core.SessionInfo{
		{Name: "s1", EventCount: 10, TokenTotal: 500},
		{Name: "s2", EventCount: 3, TokenTotal: 0},
		{Name: "s3", EventCount: 5, TokenTotal: 200, FirstMessage: "summarize current project"},
		{Name: "s4", EventCount: 1, TokenTotal: 0, FirstMessage: "Read the first 500 lines of go.mod for me and summarize what you find"},
	}

	items := buildSessionItems(sessions)
	if len(items) != 4 {
		t.Fatalf("expected 4 items, got %d", len(items))
	}
	if items[0].Value != "s1" || items[0].Label != "s1" {
		t.Errorf("unexpected item: %+v", items[0])
	}
	if !strings.Contains(items[0].Description, "500 tokens") {
		t.Errorf("expected token count in desc, got: %s", items[0].Description)
	}
	if strings.Contains(items[1].Description, "tokens") {
		t.Errorf("s2 with 0 tokens should not show token count, got: %s", items[1].Description)
	}
	// s3 has a short first message prepended.
	if !strings.Contains(items[2].Description, "summarize current project") {
		t.Errorf("expected first message in desc, got: %s", items[2].Description)
	}
	if !strings.Contains(items[2].Description, "5 events") {
		t.Errorf("expected event count in desc, got: %s", items[2].Description)
	}
	// s4 has a long first message that should be truncated.
	if strings.Contains(items[3].Description, "what you find") {
		t.Errorf("expected truncated message, got: %s", items[3].Description)
	}
}

func TestTruncateFirstMessage(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"short", "short"},
		{"trailing punctuation!?", "trailing punctuation"},
		{"first line\nsecond line", "first line"},
		{"hello, world!  ", "hello, world"},
		{"", ""},
	}
	for _, tt := range tests {
		got := truncateFirstMessage(tt.input)
		if got != tt.want {
			t.Errorf("truncateFirstMessage(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestTruncateFirstMessage_Long(t *testing.T) {
	long := "Read the first 500 lines of go.mod for me and summarize what you find"
	got := truncateFirstMessage(long)
	if len(got) > 63 {
		t.Errorf("truncated message too long (%d chars): %q", len(got), got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("expected truncation with ..., got: %q", got)
	}
}

func TestImportSessionFromZip_NoStore(t *testing.T) {
	w := newWriter()
	es := &fakeEventSink{}

	err := importSessionFromZip(w, es, nil, []string{"test.zip"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(w.Text(), "Session store not available") {
		t.Errorf("expected store-unavailable message, got: %s", w.Text())
	}
}

func TestImportSessionFromZip_NoArgs(t *testing.T) {
	w := newWriter()
	es := &fakeEventSink{}
	store := newSessionStore(nil)

	err := importSessionFromZip(w, es, store, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(w.Text(), "Usage:") {
		t.Errorf("expected usage message, got: %s", w.Text())
	}
}

func TestImportSessionFromZip_InvalidPath(t *testing.T) {
	w := newWriter()
	es := &fakeEventSink{}
	store := newSessionStore(nil)

	err := importSessionFromZip(w, es, store, []string{"/nonexistent/file.zip"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(w.Text(), "Error opening ZIP") {
		t.Errorf("expected error message, got: %s", w.Text())
	}
}

func TestImportSessionFromZip_StoreError(t *testing.T) {
	w := newWriter()
	es := &fakeEventSink{}
	store := newSessionStore(nil)
	store.err = assertError("import failed")

	err := importSessionFromZip(w, es, store, []string{"/nonexistent/file.zip"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should hit ZIP open error before store error.
	if !strings.Contains(w.Text(), "Error opening ZIP") {
		t.Errorf("expected ZIP error, got: %s", w.Text())
	}
}

// assertError is a simple error for testing error paths.
type assertError string

func (a assertError) Error() string { return string(a) }
