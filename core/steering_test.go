// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"strings"
	"testing"
)

func TestSteeringQueue_AppendAndFlushOrdering(t *testing.T) {
	sq := NewSteeringQueue()
	sq.Append("first")
	sq.Append("second")
	sq.Append("third")

	if got := sq.Len(); got != 3 {
		t.Errorf("Len() = %d, want 3", got)
	}

	pending := sq.Flush()
	want := []string{"first", "second", "third"}
	if len(pending) != len(want) {
		t.Fatalf("Flush() = %v, want %v", pending, want)
	}
	for i := range want {
		if pending[i] != want[i] {
			t.Errorf("pending[%d] = %q, want %q", i, pending[i], want[i])
		}
	}

	if got := sq.Len(); got != 0 {
		t.Errorf("Len() after Flush = %d, want 0", got)
	}
}

func TestSteeringQueue_Merge(t *testing.T) {
	sq := NewSteeringQueue()
	sq.Append("a")
	sq.Append("b")
	merged := strings.Join(sq.Flush(), "\n\n")
	want := "a\n\nb"
	if merged != want {
		t.Errorf("merged = %q, want %q", merged, want)
	}
}

func TestSteeringQueue_EmptyFlush(t *testing.T) {
	sq := NewSteeringQueue()
	if got := sq.Flush(); got != nil {
		t.Errorf("Flush() on empty = %v, want nil", got)
	}
}
