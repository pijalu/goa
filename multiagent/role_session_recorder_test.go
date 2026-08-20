// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package multiagent

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/provider"
)

// readRoleEvents parses a role JSONL file into events.
func readRoleEvents(t *testing.T, path string) []agentic.OutputEvent {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	var events []agentic.OutputEvent
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		var ev agentic.OutputEvent
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			t.Fatalf("unmarshal line: %v", err)
		}
		events = append(events, ev)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return events
}

// TestRoleSessionRecorder_WritesPerRoleFiles verifies the RC-6 logging fix:
// every pool sub-agent's complete exchange lands in a per-role JSONL file
// under the session's agents dir.
func TestRoleSessionRecorder_WritesPerRoleFiles(t *testing.T) {
	dir := t.TempDir()
	rr := NewRoleSessionRecorder(func() string { return dir })
	defer rr.Close()

	rr.Record("planner", agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.User, Text: "design the schema"})
	rr.Record("coder", agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.User, Text: "implement it"})
	rr.Record("planner", agentic.OutputEvent{Type: agentic.EventToolCall, ToolName: "read_file"})
	rr.Record("planner", agentic.OutputEvent{Type: agentic.EventEnd})

	plannerEvents := readRoleEvents(t, filepath.Join(dir, "planner.jsonl"))
	if len(plannerEvents) != 3 {
		t.Fatalf("planner events = %d, want 3", len(plannerEvents))
	}
	if plannerEvents[0].Text != "design the schema" || plannerEvents[0].Role != agentic.User {
		t.Errorf("planner[0] = %+v", plannerEvents[0])
	}
	if plannerEvents[1].Type != agentic.EventToolCall || plannerEvents[1].ToolName != "read_file" {
		t.Errorf("planner[1] = %+v", plannerEvents[1])
	}

	coderEvents := readRoleEvents(t, filepath.Join(dir, "coder.jsonl"))
	if len(coderEvents) != 1 || coderEvents[0].Text != "implement it" {
		t.Errorf("coder events = %+v", coderEvents)
	}
}

// TestRoleSessionRecorder_RotatesOnDirChange verifies a new session directory
// (e.g. /new) starts fresh files instead of appending to the old session's.
func TestRoleSessionRecorder_RotatesOnDirChange(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	current := dir1
	rr := NewRoleSessionRecorder(func() string { return current })
	defer rr.Close()

	rr.Record("companion", agentic.OutputEvent{Type: agentic.EventContent, Text: "session one"})
	current = dir2
	rr.Record("companion", agentic.OutputEvent{Type: agentic.EventContent, Text: "session two"})

	e1 := readRoleEvents(t, filepath.Join(dir1, "companion.jsonl"))
	e2 := readRoleEvents(t, filepath.Join(dir2, "companion.jsonl"))
	if len(e1) != 1 || e1[0].Text != "session one" {
		t.Errorf("dir1 events = %+v", e1)
	}
	if len(e2) != 1 || e2[0].Text != "session two" {
		t.Errorf("dir2 events = %+v", e2)
	}
}

// TestRoleSessionRecorder_NoSession verifies events are dropped silently when
// no session is active (dirFn returns "").
func TestRoleSessionRecorder_NoSession(t *testing.T) {
	dir := t.TempDir()
	active := false
	rr := NewRoleSessionRecorder(func() string {
		if !active {
			return ""
		}
		return dir
	})
	defer rr.Close()

	rr.Record("planner", agentic.OutputEvent{Type: agentic.EventContent, Text: "dropped"})
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Errorf("dir should be empty, entries=%v err=%v", entries, err)
	}

	active = true
	rr.Record("planner", agentic.OutputEvent{Type: agentic.EventContent, Text: "kept"})
	events := readRoleEvents(t, filepath.Join(dir, "planner.jsonl"))
	if len(events) != 1 || events[0].Text != "kept" {
		t.Errorf("events = %+v", events)
	}
}

// TestRoleSessionRecorder_SanitizesRoleNames verifies hostile role names
// cannot escape the agents directory.
func TestRoleSessionRecorder_SanitizesRoleNames(t *testing.T) {
	dir := t.TempDir()
	rr := NewRoleSessionRecorder(func() string { return dir })
	defer rr.Close()

	rr.Record("../../etc/evil role", agentic.OutputEvent{Type: agentic.EventContent, Text: "x"})

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %v, want exactly 1 file inside dir", entries)
	}
	name := entries[0].Name()
	if filepath.Base(name) != name || name == "" || name[0] == '.' {
		t.Errorf("unsafe file name %q", name)
	}
}

// TestRoleSessionRecorder_CloseIdempotent verifies double Close is safe.
func TestRoleSessionRecorder_CloseIdempotent(t *testing.T) {
	dir := t.TempDir()
	rr := NewRoleSessionRecorder(func() string { return dir })
	rr.Record("coder", agentic.OutputEvent{Type: agentic.EventContent, Text: "hi"})
	rr.Close()
	rr.Close()
	// Recording after close is a no-op (no panic, no write).
	rr.Record("coder", agentic.OutputEvent{Type: agentic.EventContent, Text: "after close"})
	events := readRoleEvents(t, filepath.Join(dir, "coder.jsonl"))
	if len(events) != 1 {
		t.Errorf("events after close = %d, want 1", len(events))
	}
}

// TestOrchestrator_RecordsSubAgentExchange verifies the wiring: with a
// RoleSessionRecorder installed on the orchestrator, a pool-created
// sub-agent's turn lands in that role's JSONL file.
func TestOrchestrator_RecordsSubAgentExchange(t *testing.T) {
	dir := t.TempDir()
	pool := NewAgentPool(testModel("default"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)
	rr := NewRoleSessionRecorder(func() string { return dir })
	orch.SetRoleRecorder(rr)
	defer rr.Close()

	agent, err := pool.GetOrCreate("companion")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}

	if err := agent.Run(context.Background(), "review main's patch"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	events := readRoleEvents(t, filepath.Join(dir, "companion.jsonl"))
	if len(events) == 0 {
		t.Fatal("no events recorded for sub-agent turn")
	}
	var hasUser, hasAssistant bool
	for _, ev := range events {
		if ev.Type == agentic.EventContent && ev.Role == agentic.User && ev.Text == "review main's patch" {
			hasUser = true
		}
		if ev.Type == agentic.EventContent && ev.Role == agentic.Assistant {
			hasAssistant = true
		}
	}
	if !hasUser {
		t.Errorf("user message not recorded: %+v", events)
	}
	if !hasAssistant {
		t.Errorf("assistant response not recorded: %+v", events)
	}
}
