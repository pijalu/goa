// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plan

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/pijalu/goa/internal"
)

// PlanItemPatch carries optional field updates for UpdateItem.
// Pointer fields distinguish "set to zero" from "don't change".
type PlanItemPatch struct {
	Title       *string   `json:"title,omitempty"`
	Description *string   `json:"description,omitempty"`
	DependsOn   *[]string `json:"depends_on,omitempty"`
	Role        *string   `json:"role,omitempty"`
}

// nextItemID returns the next free item-N identifier.
func (s *Store) nextItemID() string {
	n := 1
	for _, item := range s.plan.Items {
		var id int
		if _, err := fmt.Sscanf(item.ID, "item-%d", &id); err == nil && id >= n {
			n = id + 1
		}
	}
	return fmt.Sprintf("item-%d", n)
}

// nextCommentID returns the next free comment identifier.
func (s *Store) nextCommentID() string {
	return "c-" + internal.RandomString(8)
}

// AddItem appends or inserts a new plan item. If after is non-empty, the item
// is inserted after the item with that ID. The generated ID is returned.
func (s *Store) AddItem(title, description, after string, dependsOn []string, role string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := s.nextItemID()
	item := PlanItem{
		ID:          id,
		Title:       title,
		Description: description,
		DependsOn:   dependsOn,
		Role:        role,
		Status:      ItemPending,
	}

	if after != "" {
		pos := -1
		for i, it := range s.plan.Items {
			if it.ID == after {
				pos = i
				break
			}
		}
		if pos == -1 {
			return "", fmt.Errorf("item %q not found to insert after", after)
		}
		s.plan.Items = append(s.plan.Items[:pos+1], append([]PlanItem{item}, s.plan.Items[pos+1:]...)...)
	} else {
		s.plan.Items = append(s.plan.Items, item)
	}
	s.plan.UpdatedAt = time.Now().UTC()

	payload, _ := json.Marshal(PayloadItemAdded{Item: item})
	s.append(Event{Type: EventItemAdded, Payload: payload})
	return id, nil
}

// UpdateItem applies a partial update to an existing item.
func (s *Store) UpdateItem(id string, patch PlanItemPatch) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	item := s.plan.Item(id)
	if item == nil {
		return fmt.Errorf("item %q not found", id)
	}

	if patch.Title != nil {
		item.Title = *patch.Title
	}
	if patch.Description != nil {
		item.Description = *patch.Description
	}
	if patch.DependsOn != nil {
		item.DependsOn = *patch.DependsOn
	}
	if patch.Role != nil {
		item.Role = *patch.Role
	}

	s.plan.UpdatedAt = time.Now().UTC()

	// Build fields map for the event payload.
	fields := make(map[string]json.RawMessage)
	if patch.Title != nil {
		fields["title"], _ = json.Marshal(*patch.Title)
	}
	if patch.Description != nil {
		fields["description"], _ = json.Marshal(*patch.Description)
	}
	if patch.DependsOn != nil {
		fields["depends_on"], _ = json.Marshal(*patch.DependsOn)
	}
	if patch.Role != nil {
		fields["role"], _ = json.Marshal(*patch.Role)
	}

	rawFields, _ := json.Marshal(fields)
	payload, _ := json.Marshal(PayloadItemUpdated{ItemID: id, Fields: rawFields})
	s.append(Event{Type: EventItemUpdated, Payload: payload})
	return nil
}

// RemoveItem removes an item. It returns an error if other items depend on it.
func (s *Store) RemoveItem(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.plan.Item(id) == nil {
		return fmt.Errorf("item %q not found", id)
	}

	deps := s.plan.Dependents(id)
	if len(deps) > 0 {
		return fmt.Errorf("cannot remove %q: depended on by %v", id, deps)
	}

	for i, item := range s.plan.Items {
		if item.ID == id {
			s.plan.Items = append(s.plan.Items[:i], s.plan.Items[i+1:]...)
			break
		}
	}
	s.plan.UpdatedAt = time.Now().UTC()

	payload, _ := json.Marshal(PayloadItemRemoved{ItemID: id})
	s.append(Event{Type: EventItemRemoved, Payload: payload})
	return nil
}

// Reorder sets the items to the given order, which must be a permutation of all item IDs.
func (s *Store) Reorder(ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(ids) != len(s.plan.Items) {
		return fmt.Errorf("reorder: got %d ids, need %d items", len(ids), len(s.plan.Items))
	}

	// Build a set of valid IDs.
	valid := make(map[string]bool, len(s.plan.Items))
	for _, item := range s.plan.Items {
		valid[item.ID] = true
	}

	// Verify ids is a permutation.
	seen := make(map[string]int)
	for _, id := range ids {
		if !valid[id] {
			return fmt.Errorf("reorder: unknown item %q", id)
		}
		if seen[id] > 0 {
			return fmt.Errorf("reorder: duplicate item %q", id)
		}
		seen[id]++
	}

	itemMap := make(map[string]PlanItem, len(s.plan.Items))
	for _, item := range s.plan.Items {
		itemMap[item.ID] = item
	}

	ordered := make([]PlanItem, 0, len(ids))
	for _, id := range ids {
		ordered = append(ordered, itemMap[id])
	}
	s.plan.Items = ordered
	s.plan.UpdatedAt = time.Now().UTC()

	payload, _ := json.Marshal(PayloadItemsReordered{IDs: ids})
	s.append(Event{Type: EventItemsReordered, Payload: payload})
	return nil
}

// SubmitRevision increments the revision and sets the plan status to in_review.
func (s *Store) SubmitRevision() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.plan.Status != PlanDraft && s.plan.Status != PlanInReview {
		return fmt.Errorf("cannot submit revision in status %q", s.plan.Status)
	}

	s.plan.Revision++
	s.plan.Status = PlanInReview
	s.plan.UpdatedAt = time.Now().UTC()

	payload, _ := json.Marshal(PayloadRevisionSubmitted{Revision: s.plan.Revision})
	s.append(Event{Type: EventRevisionSubmitted, Payload: payload})
	return nil
}

// AddComment adds a comment to a plan or specific item.
func (s *Store) AddComment(itemID, content string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if content == "" {
		return "", fmt.Errorf("comment content must not be empty")
	}

	now := time.Now().UTC()
	comment := PlanComment{
		ID:        s.nextCommentID(),
		ItemID:    itemID,
		Content:   content,
		Revision:  s.plan.Revision,
		Resolved:  false,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.plan.Comments = append(s.plan.Comments, comment)
	s.plan.UpdatedAt = now

	payload, _ := json.Marshal(PayloadCommentAdded{Comment: comment})
	s.append(Event{Type: EventCommentAdded, Payload: payload})
	return comment.ID, nil
}

// UpdateComment updates the content of an existing comment.
func (s *Store) UpdateComment(commentID, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if content == "" {
		return fmt.Errorf("comment content must not be empty")
	}

	for i, c := range s.plan.Comments {
		if c.ID == commentID {
			s.plan.Comments[i].Content = content
			s.plan.Comments[i].UpdatedAt = time.Now().UTC()
			s.plan.UpdatedAt = s.plan.Comments[i].UpdatedAt

			payload, _ := json.Marshal(PayloadCommentUpdated{CommentID: commentID, Content: content})
			s.append(Event{Type: EventCommentUpdated, Payload: payload})
			return nil
		}
	}
	return fmt.Errorf("comment %q not found", commentID)
}

// RemoveComment deletes a comment.
func (s *Store) RemoveComment(commentID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, c := range s.plan.Comments {
		if c.ID == commentID {
			s.plan.Comments = append(s.plan.Comments[:i], s.plan.Comments[i+1:]...)
			s.plan.UpdatedAt = time.Now().UTC()

			payload, _ := json.Marshal(PayloadCommentRemoved{CommentID: commentID})
			s.append(Event{Type: EventCommentRemoved, Payload: payload})
			return nil
		}
	}
	return fmt.Errorf("comment %q not found", commentID)
}

// ResolveComment marks a comment as resolved with an optional note.
func (s *Store) ResolveComment(commentID, note string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, c := range s.plan.Comments {
		if c.ID == commentID {
			s.plan.Comments[i].Resolved = true
			s.plan.Comments[i].UpdatedAt = time.Now().UTC()
			s.plan.UpdatedAt = s.plan.Comments[i].UpdatedAt

			payload, _ := json.Marshal(PayloadCommentResolved{CommentID: commentID, Note: note})
			s.append(Event{Type: EventCommentResolved, Payload: payload})
			return nil
		}
	}
	return fmt.Errorf("comment %q not found", commentID)
}

// Approve approves the plan, changing its status from in_review to approved.
func (s *Store) Approve() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.plan.Status != PlanInReview {
		return fmt.Errorf("cannot approve plan in status %q", s.plan.Status)
	}

	s.plan.Status = PlanApproved
	s.plan.UpdatedAt = time.Now().UTC()

	payload, _ := json.Marshal(PayloadPlanApproved{})
	s.append(Event{Type: EventPlanApproved, Payload: payload})
	return nil
}

// StartExecution transitions the plan to executing state and records the run ID.
func (s *Store) StartExecution(runID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.plan.Status != PlanApproved && s.plan.Status != PlanExecuting {
		return fmt.Errorf("cannot start execution in status %q", s.plan.Status)
	}

	s.plan.Status = PlanExecuting
	s.plan.RunID = runID
	s.plan.UpdatedAt = time.Now().UTC()

	payload, _ := json.Marshal(PayloadExecutionStarted{RunID: runID})
	s.append(Event{Type: EventExecutionStarted, Payload: payload})
	return nil
}

// StartItem marks an item as in_progress after validation.
// Validation rules (spec §5):
//  1. Plan status is executing.
//  2. Item exists and is pending.
//  3. No other item is in_progress (one in flight).
//  4. Every ID in DependsOn is done or skipped.
func (s *Store) StartItem(itemID, role, agentID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Rule 1
	if s.plan.Status != PlanExecuting {
		return fmt.Errorf("cannot start item in status %q", s.plan.Status)
	}

	// Rule 2
	item := s.plan.Item(itemID)
	if item == nil {
		return fmt.Errorf("item %q not found", itemID)
	}
	if item.Status != ItemPending {
		if item.Status == ItemBlocked {
			return fmt.Errorf("item %q is blocked; use skip_item or replan", itemID)
		}
		return fmt.Errorf("item %q is %q, not pending", itemID, item.Status)
	}

	// Rule 3
	for i := range s.plan.Items {
		if s.plan.Items[i].Status == ItemInProgress {
			return fmt.Errorf("item %q is already in_progress", s.plan.Items[i].ID)
		}
	}

	// Rule 4
	for _, dep := range item.DependsOn {
		depItem := s.plan.Item(dep)
		if depItem == nil {
			return fmt.Errorf("dependency %q not found", dep)
		}
		if depItem.Status != ItemDone && depItem.Status != ItemSkipped {
			return fmt.Errorf("dependency %q is %q, not done or skipped", dep, depItem.Status)
		}
	}

	item.Status = ItemInProgress
	s.plan.UpdatedAt = time.Now().UTC()

	payload, _ := json.Marshal(PayloadItemStarted{ItemID: itemID, Role: role, AgentID: agentID})
	s.append(Event{Type: EventItemStarted, Payload: payload})
	return nil
}

// CompleteItem marks an item as done with the given result summary.
func (s *Store) CompleteItem(itemID, result string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	item := s.plan.Item(itemID)
	if item == nil {
		return fmt.Errorf("item %q not found", itemID)
	}
	if item.Status != ItemInProgress {
		return fmt.Errorf("item %q is %q, not in_progress", itemID, item.Status)
	}
	if result == "" {
		return fmt.Errorf("result must not be empty")
	}

	item.Status = ItemDone
	item.Result = result
	s.plan.UpdatedAt = time.Now().UTC()

	payload, _ := json.Marshal(PayloadItemCompleted{ItemID: itemID, Result: result})
	s.append(Event{Type: EventItemCompleted, Payload: payload})
	return nil
}

// BlockItem marks an item as blocked with a reason.
func (s *Store) BlockItem(itemID, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	item := s.plan.Item(itemID)
	if item == nil {
		return fmt.Errorf("item %q not found", itemID)
	}
	if item.Status != ItemInProgress && item.Status != ItemPending {
		return fmt.Errorf("item %q is %q, cannot block", itemID, item.Status)
	}

	item.Status = ItemBlocked
	s.plan.UpdatedAt = time.Now().UTC()

	payload, _ := json.Marshal(PayloadItemBlocked{ItemID: itemID, Reason: reason})
	s.append(Event{Type: EventItemBlocked, Payload: payload})
	return nil
}

// SkipItem marks an item as skipped. The item must be pending or blocked.
func (s *Store) SkipItem(itemID, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	item := s.plan.Item(itemID)
	if item == nil {
		return fmt.Errorf("item %q not found", itemID)
	}
	if item.Status != ItemPending && item.Status != ItemBlocked {
		return fmt.Errorf("item %q is %q, cannot skip (must be pending or blocked)", itemID, item.Status)
	}

	item.Status = ItemSkipped
	s.plan.UpdatedAt = time.Now().UTC()

	payload, _ := json.Marshal(PayloadItemSkipped{ItemID: itemID, Reason: reason})
	s.append(Event{Type: EventItemSkipped, Payload: payload})
	return nil
}

// RecordClarification records a clarification exchange for an item.
func (s *Store) RecordClarification(itemID, question, answer string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	item := s.plan.Item(itemID)
	if item == nil {
		return fmt.Errorf("item %q not found", itemID)
	}
	if question == "" && answer == "" {
		return fmt.Errorf("question and answer must not both be empty")
	}

	s.plan.UpdatedAt = time.Now().UTC()

	payload, _ := json.Marshal(PayloadClarification{ItemID: itemID, Question: question, Answer: answer})
	s.append(Event{Type: EventClarification, Payload: payload})
	return nil
}

// Finish marks the plan as done once all items are terminal.
func (s *Store) Finish() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.plan.AllTerminal() {
		return fmt.Errorf("cannot finish plan: not all items are done or skipped")
	}
	if s.plan.Status != PlanExecuting {
		return fmt.Errorf("cannot finish plan in status %q", s.plan.Status)
	}

	s.plan.Status = PlanDone
	s.plan.UpdatedAt = time.Now().UTC()

	payload, _ := json.Marshal(PayloadPlanCompleted{})
	s.append(Event{Type: EventPlanCompleted, Payload: payload})
	return nil
}

// Fail marks the plan as failed with a reason.
func (s *Store) Fail(reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.plan.Status = PlanFailed
	s.plan.UpdatedAt = time.Now().UTC()

	payload, _ := json.Marshal(PayloadPlanFailed{Reason: reason})
	s.append(Event{Type: EventPlanFailed, Payload: payload})
	return nil
}

// BlockPlan marks the plan as blocked. This is a terminal status set when an
// item cannot be unblocked and the plan cannot proceed without user intervention.
// Unlike Fail (runtime error), Blocked means the work itself is stuck.
func (s *Store) BlockPlan(reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.plan.Status = PlanBlocked
	s.plan.UpdatedAt = time.Now().UTC()

	// Use plan_failed event with a descriptive reason for now;
	// a dedicated plan_blocked event can be added when needed.
	payload, _ := json.Marshal(PayloadPlanFailed{Reason: "blocked: " + reason})
	s.append(Event{Type: EventPlanFailed, Payload: payload})
	return nil
}
