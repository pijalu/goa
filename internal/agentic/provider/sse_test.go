// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestParseSSE(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   []string
		wantOK bool
	}{
		{
			name:   "single data line",
			input:  "data: hello\n\n",
			want:   []string{"hello"},
			wantOK: true,
		},
		{
			name:   "multiple data lines",
			input:  "data: a\n\ndata: b\n\n",
			want:   []string{"a", "b"},
			wantOK: true,
		},
		{
			name:   "done terminator",
			input:  "data: x\n\ndata: [DONE]\n",
			want:   []string{"x"},
			wantOK: true,
		},
		{
			name:   "ignores comments and non-data lines",
			input:  ": ping\n\nevent: foo\ndata: payload\n\n",
			want:   []string{"payload"},
			wantOK: true,
		},
		{
			name:   "clean EOF terminates with nil error",
			input:  "data: tail-with-no-newline",
			want:   []string{"tail-with-no-newline"},
			wantOK: true,
		},
		{
			name:   "empty stream",
			input:  "",
			want:   nil,
			wantOK: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			err := ParseSSE(strings.NewReader(tt.input), func(payload string) {
				got = append(got, payload)
			})
			if tt.wantOK && err != nil {
				t.Fatalf("ParseSSE returned unexpected error: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("payloads: got %d (%v), want %d (%v)", len(got), got, len(tt.want), tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("payload[%d]: got %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestParseSSELargeLine verifies the 1MB buffer handles large payloads
// (e.g., long tool-call arguments) without truncation.
func TestParseSSELargeLine(t *testing.T) {
	big := strings.Repeat("x", 500*1024) // 500KB, exceeds default 64KB scanner buffer
	input := "data: " + big + "\n\n"

	var got string
	if err := ParseSSE(strings.NewReader(input), func(payload string) {
		got = payload
	}); err != nil {
		t.Fatalf("ParseSSE error on large line: %v", err)
	}
	if len(got) != len(big) {
		t.Fatalf("large line truncated: got %d bytes, want %d", len(got), len(big))
	}
}

// errReader returns an error after the first Read call to simulate a
// mid-stream connection drop.
type errReader struct {
	count int
}

func (e *errReader) Read(p []byte) (int, error) {
	e.count++
	if e.count == 1 {
		copy(p, []byte("data: partial\n\n"))
		return len("data: partial\n\n"), nil
	}
	return 0, errors.New("connection reset by peer")
}

// TestParseSSEMidStreamError verifies that I/O errors mid-stream are surfaced.
func TestParseSSEMidStreamError(t *testing.T) {
	var got []string
	err := ParseSSE(&errReader{}, func(payload string) {
		got = append(got, payload)
	})
	if err == nil {
		t.Fatal("expected error from mid-stream I/O failure, got nil")
	}
	if !errors.Is(err, io.ErrUnexpectedEOF) && err.Error() == "" {
		t.Fatalf("expected descriptive error, got %v", err)
	}
	if len(got) != 1 || got[0] != "partial" {
		t.Fatalf("expected partial payload before error, got %v", got)
	}
}

// TestParseSSEConsecutiveDataLinesJoin verifies the WHATWG joining rule:
// consecutive "data:" lines of one event are joined with '\n' and emitted as
// a single payload (regression for payloads silently merging without '\n').
func TestParseSSEConsecutiveDataLinesJoin(t *testing.T) {
	input := "data: {\"delta\":\ndata: \"more text\"}\n\n"
	var got []string
	if err := ParseSSE(strings.NewReader(input), func(p string) { got = append(got, p) }); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"{\"delta\":\n\"more text\"}"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("joined payload: got %q, want %q", got, want)
	}
}

// TestParseSSESplitJSONIsValidJSON ensures a JSON object split across
// multiple data lines reaches callers as one decodable payload.
func TestParseSSESplitJSONIsValidJSON(t *testing.T) {
	input := "data: {\"id\":\"chatcmpl-1\",\ndata: \"choices\":[],\ndata: \"object\":\"chat.completion.chunk\"}\n\n"
	var got []string
	if err := ParseSSE(strings.NewReader(input), func(p string) { got = append(got, p) }); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected single joined payload, got %d: %q", len(got), got)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(got[0]), &decoded); err != nil {
		t.Fatalf("joined payload is not valid JSON: %v (payload %q)", err, got[0])
	}
}

// TestParseSSEBlankLineStillDispatches guards dispatch semantics: events
// separated by blank lines stay separate payloads even after the buffering
// change.
func TestParseSSEBlankLineStillDispatches(t *testing.T) {
	input := "data: one\n\ndata: two\n\ndata: three\ndata: joined\n\ndata: four"
	var got []string
	if err := ParseSSE(strings.NewReader(input), func(p string) { got = append(got, p) }); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"one", "two", "three\njoined", "four"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("payloads: got %q, want %q", got, want)
	}
}

// TestParseSSELenientNoBlankLines verifies providers that omit blank-line
// separators between JSON-per-line events keep working: each non-consecutive
// data line flushes as its own payload because it is followed by another
// field or EOF, and only truly consecutive data lines merge.
func TestParseSSELenientNoBlankLines(t *testing.T) {
	input := "data: {\"chunk\":1}\ndata: {\"chunk\":2}\nevent: ping\ndata: {\"chunk\":3}"
	var got []string
	if err := ParseSSE(strings.NewReader(input), func(p string) { got = append(got, p) }); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// chunk1/chunk2 are consecutive data lines -> joined per spec; chunk3 is
	// flushed by the following non-data line and again at EOF.
	want := []string{"{\"chunk\":1}\n{\"chunk\":2}", "{\"chunk\":3}"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("payloads: got %q, want %q", got, want)
	}
}

// TestParseSSEDoneShortCircuitWithCallbacks verifies [DONE] still stops the
// scan immediately, fires every done callback exactly once, and delivers any
// buffered payload before terminating.
func TestParseSSEDoneShortCircuitWithCallbacks(t *testing.T) {
	input := "data: {\"a\":1}\ndata: {\"b\":2}\n\ndata: [DONE]\ndata: {\"after\":true}\n\n"
	var got []string
	doneCalls := 0
	err := ParseSSE(strings.NewReader(input), func(p string) { got = append(got, p) },
		func() { doneCalls++ }, func() { doneCalls++ })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The joined pair flushes on the blank line; nothing after [DONE] is read.
	want := []string{"{\"a\":1}\n{\"b\":2}"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("payloads: got %q, want %q", got, want)
	}
	if doneCalls != 2 {
		t.Fatalf("done callbacks: got %d calls, want 2", doneCalls)
	}
}

// TestParseSSEDoneFlushesBufferedPayload covers [DONE] arriving directly
// after an unterminated (no blank line) data block: the buffered join is
// delivered, then done callbacks fire.
func TestParseSSEDoneFlushesBufferedPayload(t *testing.T) {
	input := "data: {\"partial\":\ndata: true}\ndata: [DONE]\n"
	var got []string
	doneCalls := 0
	if err := ParseSSE(strings.NewReader(input), func(p string) { got = append(got, p) },
		func() { doneCalls++ }); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"{\"partial\":\ntrue}"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("payloads: got %q, want %q", got, want)
	}
	if doneCalls != 1 {
		t.Fatalf("done callback: got %d calls, want 1", doneCalls)
	}
}
