// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"errors"
	"io"
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

// TestParseSSEM idStreamError verifies that I/O errors mid-stream are surfaced.
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
