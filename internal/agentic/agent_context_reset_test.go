// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import "testing"

// TestAgent_EmitContextReset verifies the fresh-context reset signal
// (fresh-context goal start must not count as a cache miss):
// exactly one EventContextReset reaches observers so downstream stats can
// re-arm their per-conversation detector baselines.
func TestAgent_EmitContextReset(t *testing.T) {
	agent := NewAgent(Config{Logger: NewLogger(Error)})
	obs := &mockEventObserver{}
	agent.AddObserver(obs)

	agent.EmitContextReset()

	events := obs.Events()
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 event, got %d: %v", len(events), events)
	}
	if events[0].Type != EventContextReset {
		t.Errorf("event type = %q, want %q", events[0].Type, EventContextReset)
	}
}
