// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/tools"
)

// TestSchedulePreTurnProvider_DeliversDueOnce verifies the app-level delivery
// hook: a due job is rendered as a user-role reminder message exactly once,
// and a future job is not delivered.
func TestSchedulePreTurnProvider_DeliversDueOnce(t *testing.T) {
	store := newScheduleStore("")
	// Dates far from the real wall clock so the provider's time.Now() claim
	// is deterministic: 2000 is long past due, 2099 is long in the future.
	if _, err := store.Create(tools.ScheduleJob{
		Kind:        tools.ScheduleKindAt,
		Prompt:      "ship the release",
		ScheduledAt: time.Date(2000, 1, 2, 3, 4, 5, 0, time.UTC),
	}); err != nil {
		t.Fatalf("create due: %v", err)
	}
	if _, err := store.Create(tools.ScheduleJob{
		Kind:        tools.ScheduleKindAt,
		Prompt:      "future job",
		ScheduledAt: time.Date(2099, 1, 2, 3, 4, 5, 0, time.UTC),
	}); err != nil {
		t.Fatalf("create future: %v", err)
	}

	provider := &schedulePreTurnProvider{store: store}
	msgs := provider.PreTurnMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected exactly 1 pre-turn message, got %d", len(msgs))
	}
	if !strings.Contains(msgs[0], "[SCHEDULE REMINDER]") {
		t.Fatalf("expected reminder framing, got %q", msgs[0])
	}
	if !strings.Contains(msgs[0], "ship the release") {
		t.Fatalf("expected due prompt in message, got %q", msgs[0])
	}
	if strings.Contains(msgs[0], "future job") {
		t.Fatalf("future job must not be delivered, got %q", msgs[0])
	}

	// Second invocation claims nothing: due job injects once.
	if again := provider.PreTurnMessages(); len(again) != 0 {
		t.Fatalf("expected no re-delivery, got %d messages", len(again))
	}
}

// TestWireScheduleDelivery_NilSafe verifies the wiring helper tolerates nil.
func TestWireScheduleDelivery_NilSafe(t *testing.T) {
	wireScheduleDelivery(nil, nil) // must not panic
	am := core.NewAgentManager(nil, nil, nil, nil, nil, "")
	wireScheduleDelivery(am, nil) // nil store → no-op
}
