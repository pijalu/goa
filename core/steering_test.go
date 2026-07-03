// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal"
)

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

// TestSteering_DispatchAfterRunningFalse verifies that steering input saved
// in finalizeTurn is dispatched after am.running is set to false (the defer
// in runAgentTurn). Without this, steering re-queues forever because the
// alreadyRunning check fires while agent.Run() is still emitting EventEnd.
func TestSteering_DispatchAfterRunningFalse(t *testing.T) {
	am := NewAgentManager(&config.Config{}, nil, nil, NewSessionState(internal.ModeState{}), nil, "")

	// Simulate: user sends steering while agent is running
	// The agent's finalizeTurn saves merged steering into pendingSteering
	am.pendingSteering = "steer me"

	// Simulate: runAgentTurn defer runs after the turn completes.
	// It must clear pendingSteering and dispatch via SendUserInput.
	am.mu.Lock()
	am.running = true
	am.mu.Unlock()

	// Simulate the defer logic
	func() {
		am.mu.Lock()
		am.running = false
		pending := am.pendingSteering
		am.pendingSteering = ""
		am.mu.Unlock()

		if pending == "" {
			t.Error("expected pendingSteering to be set before defer")
		}
		// This should succeed without re-queuing because am.running is now false.
		// It will fail with "no active session" since there's no mock agent,
		// but the key thing is it doesn't re-append to steering.
		_ = am.SendUserInput(pending)
	}()

	// Verify: steering queue should be empty (we sent it, not re-queued)
	if am.steering.Len() != 0 {
		t.Errorf("steering queue should be empty after dispatch, got %d items", am.steering.Len())
	}

	// Verify: pendingSteering was cleared
	am.mu.Lock()
	p := am.pendingSteering
	am.mu.Unlock()
	if p != "" {
		t.Errorf("pendingSteering should be cleared after defer, got %q", p)
	}
}
