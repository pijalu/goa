// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import "testing"

func TestSteeringQueue_AppendFlush(t *testing.T) {
	sq := NewSteeringQueue()
	sq.Append("one")
	sq.Append("two")

	if sq.Len() != 2 {
		t.Errorf("len = %d, want 2", sq.Len())
	}

	pending := sq.Flush()
	if len(pending) != 2 {
		t.Fatalf("expected 2 pending, got %d", len(pending))
	}
	if pending[0] != "one" || pending[1] != "two" {
		t.Errorf("pending = %v", pending)
	}
	if sq.Len() != 0 {
		t.Errorf("queue not empty after flush: %d", sq.Len())
	}
}

func TestSteeringQueue_FlushEmpty(t *testing.T) {
	sq := NewSteeringQueue()
	if got := sq.Flush(); len(got) != 0 {
		t.Errorf("expected empty flush, got %v", got)
	}
}
