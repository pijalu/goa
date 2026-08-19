// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plan

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestEventJSONRoundTrip(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	for _, tt := range eventRoundTripCases() {
		t.Run(tt.name, func(t *testing.T) { validateEventRoundTrip(t, now, tt.etype, tt.pl) })
	}
}

type eventRoundTripCase struct {
	name  string
	etype EventType
	pl    any
}

func eventRoundTripCases() []eventRoundTripCase {
	return []eventRoundTripCase{
		{"plan_created", EventPlanCreated, PayloadPlanCreated{Objective: "build auth", Name: "happy.hare"}},
		{"item_added", EventItemAdded, PayloadItemAdded{Item: PlanItem{ID: "item-1", Title: "Setup DB", Status: ItemPending}}},
		{"item_updated", EventItemUpdated, PayloadItemUpdated{ItemID: "item-1", Fields: json.RawMessage(`{"title":"Setup Database"}`)}},
		{"item_removed", EventItemRemoved, PayloadItemRemoved{ItemID: "item-1"}},
		{"items_reordered", EventItemsReordered, PayloadItemsReordered{IDs: []string{"item-2", "item-1"}}},
		{"revision_submitted", EventRevisionSubmitted, PayloadRevisionSubmitted{Revision: 1}},
		{"comment_added", EventCommentAdded, PayloadCommentAdded{Comment: PlanComment{ID: "c-1", ItemID: "item-1", Content: "needs tests"}}},
		{"comment_updated", EventCommentUpdated, PayloadCommentUpdated{CommentID: "c-1", Content: "needs unit tests"}},
		{"comment_removed", EventCommentRemoved, PayloadCommentRemoved{CommentID: "c-1"}},
		{"comment_resolved", EventCommentResolved, PayloadCommentResolved{CommentID: "c-1", Note: "done"}},
		{"plan_approved", EventPlanApproved, PayloadPlanApproved{}}, {"execution_started", EventExecutionStarted, PayloadExecutionStarted{RunID: "run-abc123"}},
		{"item_started", EventItemStarted, PayloadItemStarted{ItemID: "item-1", Role: "coder", AgentID: "agent-xyz"}}, {"item_completed", EventItemCompleted, PayloadItemCompleted{ItemID: "item-1", Result: "all tests pass"}},
		{"item_blocked", EventItemBlocked, PayloadItemBlocked{ItemID: "item-1", Reason: "missing credentials"}}, {"item_skipped", EventItemSkipped, PayloadItemSkipped{ItemID: "item-1", Reason: "not needed"}},
		{"clarification", EventClarification, PayloadClarification{ItemID: "item-2", Question: "what port?", Answer: "8080"}}, {"plan_completed", EventPlanCompleted, PayloadPlanCompleted{}}, {"plan_failed", EventPlanFailed, PayloadPlanFailed{Reason: "timeout"}},
	}
}

func validateEventRoundTrip(t *testing.T, now time.Time, typ EventType, payload any) {
	t.Helper()
	payloadData, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	orig := Event{Seq: 1, Type: typ, PlanID: "plan-test-123", Timestamp: now, Payload: payloadData}
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	var got Event
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	assertEventMetadata(t, orig, got)
	assertPayloadType(t, got.Payload, payload)
}

func assertEventMetadata(t *testing.T, want, got Event) {
	t.Helper()
	if got.Seq != want.Seq {
		t.Errorf("Seq = %d, want %d", got.Seq, want.Seq)
	}
	if got.Type != want.Type {
		t.Errorf("Type = %q, want %q", got.Type, want.Type)
	}
	if got.PlanID != want.PlanID {
		t.Errorf("PlanID = %q, want %q", got.PlanID, want.PlanID)
	}
	if !got.Timestamp.Equal(want.Timestamp) {
		t.Errorf("Timestamp = %v, want %v", got.Timestamp, want.Timestamp)
	}
}

func assertPayloadType(t *testing.T, data json.RawMessage, payload any) {
	t.Helper()
	target := reflect.New(reflect.TypeOf(payload))
	if err := json.Unmarshal(data, target.Interface()); err != nil {
		t.Errorf("unmarshal payload type %T: %v", payload, err)
	}
}

// TestEventRoundTripAllTypes runs all events through JSON marshal/unmarshal.
func TestEventTypeConstants(t *testing.T) {
	types := []EventType{
		EventPlanCreated,
		EventItemAdded,
		EventItemUpdated,
		EventItemRemoved,
		EventItemsReordered,
		EventRevisionSubmitted,
		EventCommentAdded,
		EventCommentUpdated,
		EventCommentRemoved,
		EventCommentResolved,
		EventPlanApproved,
		EventExecutionStarted,
		EventItemStarted,
		EventItemCompleted,
		EventItemBlocked,
		EventItemSkipped,
		EventClarification,
		EventPlanCompleted,
		EventPlanFailed,
	}

	if len(types) != 19 {
		t.Errorf("expected 19 event types, got %d", len(types))
	}

	seen := make(map[EventType]bool)
	for _, et := range types {
		if seen[et] {
			t.Errorf("duplicate event type: %q", et)
		}
		seen[et] = true
		// Verify marshaling works
		data, err := json.Marshal(et)
		if err != nil {
			t.Errorf("marshal %q: %v", et, err)
		}
		var got EventType
		if err := json.Unmarshal(data, &got); err != nil {
			t.Errorf("unmarshal %q: %v", et, err)
		}
		if got != et {
			t.Errorf("round-trip: got %q, want %q", got, et)
		}
	}
}
