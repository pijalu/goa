// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package common

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// readLines returns all non-empty lines of a file.
func readLines(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if line := sc.Text(); line != "" {
			lines = append(lines, line)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan %s: %v", path, err)
	}
	return lines
}

func TestDispatchLog_AppendAndReadBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dispatch.jsonl")
	log, err := NewDispatchLog(path)
	if err != nil {
		t.Fatalf("NewDispatchLog: %v", err)
	}
	defer log.Close()

	start := time.Now()
	entries := []DispatchEntry{
		{RunID: "r1", Seq: 1, CallID: "r1:sub:1", Tool: "read", Arguments: `{"path":"a"}`, StartedAt: start, FinishedAt: start.Add(10 * time.Millisecond), DurationMS: 10, OK: true, Result: "content"},
		{RunID: "r1", Seq: 2, CallID: "r1:sub:2", Tool: "bash", Arguments: `{"command":"ls"}`, StartedAt: start, FinishedAt: start.Add(5 * time.Millisecond), DurationMS: 5, OK: false, Error: "denied"},
	}
	for _, e := range entries {
		if err := log.Append(e); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	lines := readLines(t, path)
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	for i, line := range lines {
		var got DispatchEntry
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			t.Fatalf("line %d is not valid JSON: %v", i, err)
		}
		if got.Tool != entries[i].Tool || got.Seq != entries[i].Seq {
			t.Errorf("line %d = %+v, want %+v", i, got, entries[i])
		}
	}
}

func TestDispatchLog_AppendAfterCloseFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dispatch.jsonl")
	log, err := NewDispatchLog(path)
	if err != nil {
		t.Fatalf("NewDispatchLog: %v", err)
	}
	if err := log.Append(DispatchEntry{RunID: "r", Seq: 1, Tool: "read"}); err != nil {
		t.Fatalf("Append before close: %v", err)
	}
	if err := log.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := log.Close(); err != nil {
		t.Fatalf("Close idempotent: %v", err)
	}
	if err := log.Append(DispatchEntry{RunID: "r", Seq: 2, Tool: "read"}); err == nil {
		t.Fatal("Append after Close should fail")
	}
}

func TestDispatchLog_ConcurrentAppendsStayValidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dispatch.jsonl")
	log, err := NewDispatchLog(path)
	if err != nil {
		t.Fatalf("NewDispatchLog: %v", err)
	}
	defer log.Close()

	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = log.Append(DispatchEntry{RunID: "r", Seq: i, Tool: "read", Result: "x"})
		}(i)
	}
	wg.Wait()

	lines := readLines(t, path)
	if len(lines) != n {
		t.Fatalf("got %d lines, want %d", len(lines), n)
	}
	seen := make(map[int]bool)
	for _, line := range lines {
		var e DispatchEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("concurrent append corrupted a line: %v", err)
		}
		seen[e.Seq] = true
	}
	if len(seen) != n {
		t.Fatalf("got %d distinct seqs, want %d", len(seen), n)
	}
}

func TestDispatchLog_FilePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dispatch.jsonl")
	log, err := NewDispatchLog(path)
	if err != nil {
		t.Fatalf("NewDispatchLog: %v", err)
	}
	defer log.Close()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("dispatch log permissions %o, want owner-only (0600)", perm)
	}
	if log.Path() != path {
		t.Errorf("Path() = %q, want %q", log.Path(), path)
	}
}
