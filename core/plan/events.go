// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plan

import (
	"encoding/json"
	"time"
)

// EventType categorizes a plan store event.
type EventType string

const (
	EventPlanCreated       EventType = "plan_created"
	EventItemAdded         EventType = "item_added"
	EventItemUpdated       EventType = "item_updated"
	EventItemRemoved       EventType = "item_removed"
	EventItemsReordered    EventType = "items_reordered"
	EventRevisionSubmitted EventType = "revision_submitted"
	EventCommentAdded      EventType = "comment_added"
	EventCommentUpdated    EventType = "comment_updated"
	EventCommentRemoved    EventType = "comment_removed"
	EventCommentResolved   EventType = "comment_resolved"
	EventPlanApproved      EventType = "plan_approved"
	EventExecutionStarted  EventType = "execution_started"
	EventItemStarted       EventType = "item_started"
	EventItemCompleted     EventType = "item_completed"
	EventItemBlocked       EventType = "item_blocked"
	EventItemSkipped       EventType = "item_skipped"
	EventClarification     EventType = "clarification"
	EventPlanCompleted     EventType = "plan_completed"
	EventPlanFailed        EventType = "plan_failed"
)

// Event is a single entry in the plan event log (events.jsonl).
type Event struct {
	Seq       int             `json:"seq"`
	Type      EventType       `json:"type"`
	PlanID    string          `json:"plan_id"`
	Timestamp time.Time       `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}

// --- Payload types ---

// PayloadPlanCreated is the payload for EventPlanCreated.
type PayloadPlanCreated struct {
	Objective string `json:"objective"`
	Name      string `json:"name"`
}

// PayloadItemAdded is the payload for EventItemAdded.
type PayloadItemAdded struct {
	Item PlanItem `json:"item"`
}

// PayloadItemUpdated is the payload for EventItemUpdated.
// Fields is a JSON object containing only the changed fields.
type PayloadItemUpdated struct {
	ItemID string          `json:"item_id"`
	Fields json.RawMessage `json:"fields"`
}

// PayloadItemRemoved is the payload for EventItemRemoved.
type PayloadItemRemoved struct {
	ItemID string `json:"item_id"`
}

// PayloadItemsReordered is the payload for EventItemsReordered.
type PayloadItemsReordered struct {
	IDs []string `json:"ids"`
}

// PayloadRevisionSubmitted is the payload for EventRevisionSubmitted.
type PayloadRevisionSubmitted struct {
	Revision int `json:"revision"`
}

// PayloadCommentAdded is the payload for EventCommentAdded.
type PayloadCommentAdded struct {
	Comment PlanComment `json:"comment"`
}

// PayloadCommentUpdated is the payload for EventCommentUpdated.
type PayloadCommentUpdated struct {
	CommentID string `json:"comment_id"`
	Content   string `json:"content"`
}

// PayloadCommentRemoved is the payload for EventCommentRemoved.
type PayloadCommentRemoved struct {
	CommentID string `json:"comment_id"`
}

// PayloadCommentResolved is the payload for EventCommentResolved.
type PayloadCommentResolved struct {
	CommentID string `json:"comment_id"`
	Note      string `json:"note,omitempty"`
}

// PayloadPlanApproved is the payload for EventPlanApproved.
type PayloadPlanApproved struct{}

// PayloadExecutionStarted is the payload for EventExecutionStarted.
type PayloadExecutionStarted struct {
	RunID string `json:"run_id"`
}

// PayloadItemStarted is the payload for EventItemStarted.
type PayloadItemStarted struct {
	ItemID  string `json:"item_id"`
	Role    string `json:"role"`
	AgentID string `json:"agent_id"`
}

// PayloadItemCompleted is the payload for EventItemCompleted.
type PayloadItemCompleted struct {
	ItemID string `json:"item_id"`
	Result string `json:"result"`
}

// PayloadItemBlocked is the payload for EventItemBlocked.
type PayloadItemBlocked struct {
	ItemID string `json:"item_id"`
	Reason string `json:"reason"`
}

// PayloadItemSkipped is the payload for EventItemSkipped.
type PayloadItemSkipped struct {
	ItemID string `json:"item_id"`
	Reason string `json:"reason,omitempty"`
}

// PayloadClarification is the payload for EventClarification.
type PayloadClarification struct {
	ItemID   string `json:"item_id"`
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

// PayloadPlanCompleted is the payload for EventPlanCompleted.
type PayloadPlanCompleted struct{}

// PayloadPlanFailed is the payload for EventPlanFailed.
type PayloadPlanFailed struct {
	Reason string `json:"reason"`
}
