// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
)

func readCaptureRecords(t *testing.T, path string) []streamCaptureRecord {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open capture file: %v", err)
	}
	defer func() { _ = f.Close() }()
	var recs []streamCaptureRecord
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var rec streamCaptureRecord
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			t.Fatalf("invalid JSONL record %q: %v", sc.Text(), err)
		}
		recs = append(recs, rec)
	}
	return recs
}

// assertContentDeltaRecord checks the first thinking delta round-tripped
// intact: type, text, delta flag and reasoning state preserved.
func assertContentDeltaRecord(t *testing.T, rec streamCaptureRecord) {
	t.Helper()
	if rec.Type == "content" && rec.Text == "Let me " && rec.IsDelta && rec.State == "thinking" {
		return
	}
	t.Fatalf("first delta record mangled: %+v", rec)
}

// assertToolStartRecord checks the tool-start record kept its name and input.
func assertToolStartRecord(t *testing.T, rec streamCaptureRecord) {
	t.Helper()
	if rec.Type == "tool_start" && rec.ToolName == "read" && rec.ToolInput != "" {
		return
	}
	t.Fatalf("tool start record mangled: %+v", rec)
}

// assertAllTimestamped requires every record to carry a monotonic timestamp.
func assertAllTimestamped(t *testing.T, recs []streamCaptureRecord) {
	t.Helper()
	for i, rec := range recs {
		if rec.TS == 0 {
			t.Fatalf("record %d missing timestamp", i)
		}
	}
}

func TestStreamCapture_RecordsExactFlow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flow.jsonl")
	c, err := newStreamCapture(path)
	if err != nil {
		t.Fatalf("newStreamCapture: %v", err)
	}
	events := []agentic.OutputEvent{
		{Type: agentic.EventContent, State: agentic.StateThinking, Text: "Let me ", IsDelta: true},
		{Type: agentic.EventContent, State: agentic.StateThinking, Text: "check:", IsDelta: true},
		{Type: agentic.EventContent, State: agentic.StateContent, Text: "Done.", IsDelta: true},
		{Type: agentic.EventToolStart, ToolName: "read", ToolCallID: "call_1", ToolInput: `{"file_path":"x.go"}`},
		{Type: agentic.EventToolResult, ToolName: "read", ToolCallID: "call_1", ToolResult: "package x"},
	}
	for i := range events {
		c.record(&events[i])
	}
	c.close()

	recs := readCaptureRecords(t, path)
	if len(recs) != len(events) {
		t.Fatalf("records = %d, want %d", len(recs), len(events))
	}
	assertContentDeltaRecord(t, recs[0])
	assertToolStartRecord(t, recs[3])
	if recs[4].ToolResult != "package x" {
		t.Fatalf("tool result record mangled: %+v", recs[4])
	}
	assertAllTimestamped(t, recs)
}

func TestStreamCapture_OpenFailure(t *testing.T) {
	if _, err := newStreamCapture(filepath.Join(t.TempDir(), "missing", "dir", "flow.jsonl")); err == nil {
		t.Fatal("expected error for missing parent directory")
	}
}
