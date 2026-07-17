// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plan

import (
	"encoding/json"
	"testing"
	"time"
)

func TestEventJSONRoundTrip(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name  string
		etype EventType
		pl    any
	}{
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
		{"plan_approved", EventPlanApproved, PayloadPlanApproved{}},
		{"execution_started", EventExecutionStarted, PayloadExecutionStarted{RunID: "run-abc123"}},
		{"item_started", EventItemStarted, PayloadItemStarted{ItemID: "item-1", Role: "coder", AgentID: "agent-xyz"}},
		{"item_completed", EventItemCompleted, PayloadItemCompleted{ItemID: "item-1", Result: "all tests pass"}},
		{"item_blocked", EventItemBlocked, PayloadItemBlocked{ItemID: "item-1", Reason: "missing credentials"}},
		{"item_skipped", EventItemSkipped, PayloadItemSkipped{ItemID: "item-1", Reason: "not needed"}},
		{"clarification", EventClarification, PayloadClarification{ItemID: "item-2", Question: "what port?", Answer: "8080"}},
		{"plan_completed", EventPlanCompleted, PayloadPlanCompleted{}},
		{"plan_failed", EventPlanFailed, PayloadPlanFailed{Reason: "timeout"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload, err := json.Marshal(tt.pl)
			if err != nil {
				t.Fatalf("marshal payload: %v", err)
			}

			orig := Event{
				Seq:       1,
				Type:      tt.etype,
				PlanID:    "plan-test-123",
				Timestamp: now,
				Payload:   payload,
			}

			data, err := json.Marshal(orig)
			if err != nil {
				t.Fatalf("marshal event: %v", err)
			}

			var got Event
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("unmarshal event: %v", err)
			}

			if got.Seq != orig.Seq {
				t.Errorf("Seq = %d, want %d", got.Seq, orig.Seq)
			}
			if got.Type != orig.Type {
				t.Errorf("Type = %q, want %q", got.Type, orig.Type)
			}
			if got.PlanID != orig.PlanID {
				t.Errorf("PlanID = %q, want %q", got.PlanID, orig.PlanID)
			}
			if !got.Timestamp.Equal(orig.Timestamp) {
				t.Errorf("Timestamp = %v, want %v", got.Timestamp, orig.Timestamp)
			}

			// Verify payload round-trips to the correct type
			switch orig.Type {
			case EventPlanCreated:
				var p PayloadPlanCreated
				if err := json.Unmarshal(got.Payload, &p); err != nil {
					t.Errorf("unmarshal PayloadPlanCreated: %v", err)
				}
			case EventItemAdded:
				var p PayloadItemAdded
				if err := json.Unmarshal(got.Payload, &p); err != nil {
					t.Errorf("unmarshal PayloadItemAdded: %v", err)
				}
			case EventItemUpdated:
				var p PayloadItemUpdated
				if err := json.Unmarshal(got.Payload, &p); err != nil {
					t.Errorf("unmarshal PayloadItemUpdated: %v", err)
				}
			case EventItemRemoved:
				var p PayloadItemRemoved
				if err := json.Unmarshal(got.Payload, &p); err != nil {
					t.Errorf("unmarshal PayloadItemRemoved: %v", err)
				}
			case EventItemsReordered:
				var p PayloadItemsReordered
				if err := json.Unmarshal(got.Payload, &p); err != nil {
					t.Errorf("unmarshal PayloadItemsReordered: %v", err)
				}
			case EventRevisionSubmitted:
				var p PayloadRevisionSubmitted
				if err := json.Unmarshal(got.Payload, &p); err != nil {
					t.Errorf("unmarshal PayloadRevisionSubmitted: %v", err)
				}
			case EventCommentAdded:
				var p PayloadCommentAdded
				if err := json.Unmarshal(got.Payload, &p); err != nil {
					t.Errorf("unmarshal PayloadCommentAdded: %v", err)
				}
			case EventCommentUpdated:
				var p PayloadCommentUpdated
				if err := json.Unmarshal(got.Payload, &p); err != nil {
					t.Errorf("unmarshal PayloadCommentUpdated: %v", err)
				}
			case EventCommentRemoved:
				var p PayloadCommentRemoved
				if err := json.Unmarshal(got.Payload, &p); err != nil {
					t.Errorf("unmarshal PayloadCommentRemoved: %v", err)
				}
			case EventCommentResolved:
				var p PayloadCommentResolved
				if err := json.Unmarshal(got.Payload, &p); err != nil {
					t.Errorf("unmarshal PayloadCommentResolved: %v", err)
				}
			case EventPlanApproved:
				var p PayloadPlanApproved
				if err := json.Unmarshal(got.Payload, &p); err != nil {
					t.Errorf("unmarshal PayloadPlanApproved: %v", err)
				}
			case EventExecutionStarted:
				var p PayloadExecutionStarted
				if err := json.Unmarshal(got.Payload, &p); err != nil {
					t.Errorf("unmarshal PayloadExecutionStarted: %v", err)
				}
			case EventItemStarted:
				var p PayloadItemStarted
				if err := json.Unmarshal(got.Payload, &p); err != nil {
					t.Errorf("unmarshal PayloadItemStarted: %v", err)
				}
			case EventItemCompleted:
				var p PayloadItemCompleted
				if err := json.Unmarshal(got.Payload, &p); err != nil {
					t.Errorf("unmarshal PayloadItemCompleted: %v", err)
				}
			case EventItemBlocked:
				var p PayloadItemBlocked
				if err := json.Unmarshal(got.Payload, &p); err != nil {
					t.Errorf("unmarshal PayloadItemBlocked: %v", err)
				}
			case EventItemSkipped:
				var p PayloadItemSkipped
				if err := json.Unmarshal(got.Payload, &p); err != nil {
					t.Errorf("unmarshal PayloadItemSkipped: %v", err)
				}
			case EventClarification:
				var p PayloadClarification
				if err := json.Unmarshal(got.Payload, &p); err != nil {
					t.Errorf("unmarshal PayloadClarification: %v", err)
				}
			case EventPlanCompleted:
				var p PayloadPlanCompleted
				if err := json.Unmarshal(got.Payload, &p); err != nil {
					t.Errorf("unmarshal PayloadPlanCompleted: %v", err)
				}
			case EventPlanFailed:
				var p PayloadPlanFailed
				if err := json.Unmarshal(got.Payload, &p); err != nil {
					t.Errorf("unmarshal PayloadPlanFailed: %v", err)
				}
			}
		})
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
