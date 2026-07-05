// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

import (
	"testing"
	"time"
)

func TestAgentStats_AddUsageAndStatus(t *testing.T) {
	s := NewAgentStats()
	if got := s.Snapshot().Status; got != AgentPending {
		t.Errorf("initial status = %q, want pending", got)
	}

	s.AddUsage(100, 50, 10, 5)
	s.AddUsage(-1, 0, 0, 0) // negative must be ignored
	s.IncToolCall()
	s.IncToolCall()
	s.IncTurn()
	s.SetStatus(AgentRunning)

	snap := s.Snapshot()
	if snap.TokensIn != 100 || snap.TokensOut != 50 || snap.CacheRead != 10 || snap.CacheCreation != 5 {
		t.Errorf("usage mismatch: %+v", snap)
	}
	if snap.ToolCalls != 2 {
		t.Errorf("tool calls = %d, want 2", snap.ToolCalls)
	}
	if snap.Turns != 1 {
		t.Errorf("turns = %d, want 1", snap.Turns)
	}
	if snap.Status != AgentRunning {
		t.Errorf("status = %q, want running", snap.Status)
	}
	if !snap.UpdatedAt.After(snap.StartedAt) && !snap.UpdatedAt.Equal(snap.StartedAt) {
		t.Errorf("updated before started")
	}
	_ = time.Now()
}

func TestAgentHandle_Steering(t *testing.T) {
	h := NewAgentHandle("coder-1", "coder", "m1")
	if got := h.DrainSteering(); len(got) != 0 {
		t.Errorf("expected empty steering, got %v", got)
	}
	h.Steer("hello")
	h.Steer("world")
	pending := h.DrainSteering()
	if len(pending) != 2 || pending[0] != "hello" || pending[1] != "world" {
		t.Errorf("drain = %v, want [hello world]", pending)
	}
	if got := h.DrainSteering(); len(got) != 0 {
		t.Errorf("drain after flush = %v, want empty", got)
	}
}

func TestAgentHandle_DoneReleasesOnce(t *testing.T) {
	h := NewAgentHandle("a-1", "a", "m1")
	select {
	case <-h.Done():
		t.Fatalf("Done closed before release")
	default:
	}
	h.markReleased()
	h.markReleased() // idempotent — must not panic on double close
	select {
	case <-h.Done():
	default:
		t.Fatalf("Done not closed after release")
	}
}
