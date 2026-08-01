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
	if recs[0].Type != "content" || recs[0].Text != "Let me " || !recs[0].IsDelta || recs[0].State != "thinking" {
		t.Fatalf("first delta record mangled: %+v", recs[0])
	}
	if recs[3].Type != "tool_start" || recs[3].ToolName != "read" || recs[3].ToolInput == "" {
		t.Fatalf("tool start record mangled: %+v", recs[3])
	}
	if recs[4].ToolResult != "package x" {
		t.Fatalf("tool result record mangled: %+v", recs[4])
	}
	for i, rec := range recs {
		if rec.TS == 0 {
			t.Fatalf("record %d missing timestamp", i)
		}
	}
}

func TestStreamCapture_OpenFailure(t *testing.T) {
	if _, err := newStreamCapture(filepath.Join(t.TempDir(), "missing", "dir", "flow.jsonl")); err == nil {
		t.Fatal("expected error for missing parent directory")
	}
}
