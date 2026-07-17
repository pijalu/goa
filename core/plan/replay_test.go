// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestReplay_AllEventTypes(t *testing.T) {
	// This test verifies that every event type can be replayed by:
	// 1. Creating a plan
	// 2. Appending events via the public mutation API
	// 3. Reopening and verifying state
	root := t.TempDir()

	s, err := Create(root, "replay all events")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Add items and perform mutations.
	id1, err := s.AddItem("Item 1", "Description 1", "", nil, "coder")
	if err != nil {
		t.Fatalf("AddItem: %v", err)
	}
	id2, err := s.AddItem("Item 2", "Description 2", "", nil, "")
	if err != nil {
		t.Fatalf("AddItem: %v", err)
	}

	// Update item.
	title := "Updated Item 1"
	desc := "Updated description"
	err = s.UpdateItem(id1, PlanItemPatch{Title: &title, Description: &desc})
	if err != nil {
		t.Fatalf("UpdateItem: %v", err)
	}

	// Reorder.
	err = s.Reorder([]string{id2, id1})
	if err != nil {
		t.Fatalf("Reorder: %v", err)
	}

	// Add comments.
	cid1, err := s.AddComment("", "Plan-level observation")
	if err != nil {
		t.Fatalf("AddComment plan: %v", err)
	}
	cid2, err := s.AddComment(id1, "Item 1 needs work")
	if err != nil {
		t.Fatalf("AddComment item: %v", err)
	}

	// Update comment.
	err = s.UpdateComment(cid2, "Item 1 needs more work")
	if err != nil {
		t.Fatalf("UpdateComment: %v", err)
	}

	// Submit revision.
	err = s.SubmitRevision()
	if err != nil {
		t.Fatalf("SubmitRevision: %v", err)
	}

	// Resolve one comment.
	err = s.ResolveComment(cid2, "addressed")
	if err != nil {
		t.Fatalf("ResolveComment: %v", err)
	}

	// Approve.
	err = s.Approve()
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}

	// Start execution.
	err = s.StartExecution("run-abc123")
	if err != nil {
		t.Fatalf("StartExecution: %v", err)
	}

	// Execute items.
	err = s.StartItem(id2, "coder", "agent-1")
	if err != nil {
		t.Fatalf("StartItem: %v", err)
	}
	err = s.CompleteItem(id2, "All done")
	if err != nil {
		t.Fatalf("CompleteItem: %v", err)
	}

	err = s.StartItem(id1, "coder", "agent-2")
	if err != nil {
		t.Fatalf("StartItem: %v", err)
	}

	// Record clarification.
	err = s.RecordClarification(id1, "Which approach?", "REST")
	if err != nil {
		t.Fatalf("RecordClarification: %v", err)
	}

	// Block item.
	err = s.BlockItem(id1, "Can't proceed, missing dependency")
	if err != nil {
		t.Fatalf("BlockItem: %v", err)
	}

	// Skip blocked item.
	err = s.SkipItem(id1, "Will do manually")
	if err != nil {
		t.Fatalf("SkipItem: %v", err)
	}

	// Remove a comment.
	err = s.RemoveComment(cid1)
	if err != nil {
		t.Fatalf("RemoveComment: %v", err)
	}

	s.Close()

	// Now reopen and verify everything replayed correctly.
	s2, err := Open(root, s.id)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s2.Close()

	// Verify plan metadata.
	if s2.plan.Name == "" {
		t.Error("plan name should be set after replay")
	}
	if s2.plan.Objective != "replay all events" {
		t.Errorf("objective = %q", s2.plan.Objective)
	}
	if s2.plan.Status != PlanExecuting {
		t.Errorf("status = %q, want executing", s2.plan.Status)
	}
	if s2.plan.Revision != 1 {
		t.Errorf("revision = %d, want 1", s2.plan.Revision)
	}
	if s2.plan.RunID != "run-abc123" {
		t.Errorf("RunID = %q", s2.plan.RunID)
	}

	// Verify items.
	if len(s2.plan.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(s2.plan.Items))
	}

	// Items were reordered to [id2, id1].
	if s2.plan.Items[0].ID != id2 {
		t.Errorf("first item should be %q, got %q", id2, s2.plan.Items[0].ID)
	}
	if s2.plan.Items[1].ID != id1 {
		t.Errorf("second item should be %q, got %q", id1, s2.plan.Items[1].ID)
	}

	// Item id1 was updated.
	it1 := s2.plan.Item(id1)
	if it1 == nil {
		t.Fatal("item-1 not found after replay")
	}
	if it1.Title != "Updated Item 1" {
		t.Errorf("title = %q", it1.Title)
	}
	if it1.Description != "Updated description" {
		t.Errorf("description = %q", it1.Description)
	}
	if it1.Status != ItemSkipped {
		t.Errorf("status = %q, want skipped", it1.Status)
	}

	// Item id2 was completed.
	it2 := s2.plan.Item(id2)
	if it2 == nil {
		t.Fatal("item-2 not found after replay")
	}
	if it2.Status != ItemDone {
		t.Errorf("status = %q, want done", it2.Status)
	}
	if it2.Result != "All done" {
		t.Errorf("result = %q", it2.Result)
	}

	// Verify comments.
	// cid1 was removed, cid2 remains resolved.
	if len(s2.plan.Comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(s2.plan.Comments))
	}
	if s2.plan.Comments[0].ID != cid2 {
		t.Errorf("remaining comment should be %q, got %q", cid2, s2.plan.Comments[0].ID)
	}
	if !s2.plan.Comments[0].Resolved {
		t.Error("comment should be resolved")
	}
	if s2.plan.Comments[0].Content != "Item 1 needs more work" {
		t.Errorf("content = %q", s2.plan.Comments[0].Content)
	}
}

func TestReplay_ItemUpdatedFields(t *testing.T) {
	// Specifically test applyItemFields through EventItemUpdated.
	root := t.TempDir()

	s, err := Create(root, "test item_updated")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	id, err := s.AddItem("Original", "Original desc", "", nil, "coder")
	if err != nil {
		t.Fatalf("AddItem: %v", err)
	}

	// Update all fields.
	title := "Updated"
	desc := "Updated desc"
	deps := []string{"item-0"}
	role := "reviewer"
	err = s.UpdateItem(id, PlanItemPatch{
		Title:       &title,
		Description: &desc,
		DependsOn:   &deps,
		Role:        &role,
	})
	if err != nil {
		t.Fatalf("UpdateItem: %v", err)
	}

	s.Close()

	s2, err := Open(root, s.id)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s2.Close()

	item := s2.plan.Item(id)
	if item == nil {
		t.Fatal("item not found after replay")
	}
	if item.Title != "Updated" {
		t.Errorf("title = %q", item.Title)
	}
	if item.Description != "Updated desc" {
		t.Errorf("description = %q", item.Description)
	}
	if len(item.DependsOn) != 1 || item.DependsOn[0] != "item-0" {
		t.Errorf("depends_on = %v", item.DependsOn)
	}
	if item.Role != "reviewer" {
		t.Errorf("role = %q", item.Role)
	}
}

func TestReplay_CorruptLine(t *testing.T) {
	root := t.TempDir()

	s, err := Create(root, "corrupt test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	s.Close()

	// Append a corrupt line.
	eventsPath := filepath.Join(root, s.id, "events.jsonl")
	f, err := os.OpenFile(eventsPath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("open events: %v", err)
	}
	_, err = f.WriteString("{invalid json\n")
	if err != nil {
		t.Fatalf("write corrupt line: %v", err)
	}
	f.Close()

	_, err = Open(root, s.id)
	if err == nil {
		t.Fatal("expected error for corrupt event log")
	}
}

func TestReplay_Idempotent(t *testing.T) {
	root := t.TempDir()

	s, err := Create(root, "idempotent test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Add a few mutations.
	id, _ := s.AddItem("X", "", "", nil, "")
	s.SubmitRevision()
	s.Approve()
	s.StartExecution("run-test")
	s.StartItem(id, "coder", "agent")
	s.CompleteItem(id, "success")
	s.Finish()

	s.Close()

	// Open twice and verify they produce the same state.
	s2, err := Open(root, s.id)
	if err != nil {
		t.Fatalf("First Open: %v", err)
	}
	s2.Close()

	s3, err := Open(root, s.id)
	if err != nil {
		t.Fatalf("Second Open: %v", err)
	}
	defer s3.Close()

	if s3.plan.Status != PlanDone {
		t.Errorf("status = %q, want done", s3.plan.Status)
	}
	if len(s3.plan.Items) != 1 {
		t.Errorf("items = %d", len(s3.plan.Items))
	}
	if s3.plan.Revision != 1 {
		t.Errorf("revision = %d", s3.plan.Revision)
	}
}

func TestReplay_FinishWithAllTerminal(t *testing.T) {
	root := t.TempDir()

	s, err := Create(root, "finish test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	id1, _ := s.AddItem("A", "", "", nil, "")
	id2, _ := s.AddItem("B", "", "", nil, "")

	s.SubmitRevision()
	s.Approve()
	s.StartExecution("run-test")

	s.StartItem(id1, "coder", "agent")
	s.CompleteItem(id1, "done")

	// Skip the second item.
	s.BlockItem(id1, "can't")
	s.StartItem(id2, "coder", "agent-2")
	s.BlockItem(id2, "also can't")
	s.SkipItem(id2, "skip it")

	// Finish.
	err = s.Finish()
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}

	s.Close()

	// Reopen and verify.
	s2, err := Open(root, s.id)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s2.Close()

	if s2.plan.Status != PlanDone {
		t.Errorf("status = %q, want done", s2.plan.Status)
	}
	if !s2.plan.AllTerminal() {
		t.Error("AllTerminal should be true")
	}
}

func TestReplay_Fail(t *testing.T) {
	root := t.TempDir()

	s, err := Create(root, "fail test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	err = s.Fail("something went wrong")
	if err != nil {
		t.Fatalf("Fail: %v", err)
	}

	s.Close()

	s2, err := Open(root, s.id)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s2.Close()

	if s2.plan.Status != PlanFailed {
		t.Errorf("status = %q, want failed", s2.plan.Status)
	}
}

func TestReplay_BlockPlan(t *testing.T) {
	root := t.TempDir()

	s, err := Create(root, "block test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	err = s.BlockPlan("can't proceed")
	if err != nil {
		t.Fatalf("BlockPlan: %v", err)
	}

	s.Close()

	s2, err := Open(root, s.id)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s2.Close()

	// BlockPlan appends a plan_failed event with "blocked:" prefix.
	if s2.plan.Status != PlanFailed {
		t.Errorf("status = %q, want failed (blocked uses plan_failed event)", s2.plan.Status)
	}
}

func TestStore_PlanGetters(t *testing.T) {
	root := t.TempDir()

	s, err := Create(root, "getters test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer s.Close()

	// ID getter
	if s.ID() != s.id {
		t.Errorf("ID() = %q, want %q", s.ID(), s.id)
	}

	// Plan getter returns the in-memory plan.
	p := s.Plan()
	if p.Objective != "getters test" {
		t.Errorf("Plan().Objective = %q", p.Objective)
	}
}

func TestReplay_ItemBlocked(t *testing.T) {
	root := t.TempDir()

	s, err := Create(root, "blocked item test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	id, _ := s.AddItem("Do thing", "", "", nil, "")
	s.SubmitRevision()
	s.Approve()
	s.StartExecution("run-test")
	s.StartItem(id, "coder", "agent-1")
	s.BlockItem(id, "stuck")

	s.Close()

	s2, err := Open(root, s.id)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s2.Close()

	item := s2.plan.Item(id)
	if item == nil {
		t.Fatal("item not found")
	}
	if item.Status != ItemBlocked {
		t.Errorf("status = %q, want blocked", item.Status)
	}
}

func TestReplay_ItemStarted(t *testing.T) {
	root := t.TempDir()

	s, err := Create(root, "started item test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	id, _ := s.AddItem("Do thing", "", "", nil, "")
	s.SubmitRevision()
	s.Approve()
	s.StartExecution("run-test")
	s.StartItem(id, "coder", "agent-1")

	s.Close()

	s2, err := Open(root, s.id)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s2.Close()

	item := s2.plan.Item(id)
	if item == nil {
		t.Fatal("item not found")
	}
	if item.Status != ItemInProgress {
		t.Errorf("status = %q, want in_progress", item.Status)
	}
}

func TestReplay_SkipWithoutReason(t *testing.T) {
	root := t.TempDir()

	s, err := Create(root, "skip no reason")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	id, _ := s.AddItem("Skip me", "", "", nil, "")
	s.SubmitRevision()
	s.Approve()
	s.StartExecution("run-test")

	// Manually create a skip event without reason to test the payload.
	s.mu.Lock()
	payload, _ := json.Marshal(PayloadItemSkipped{ItemID: id}) // no reason
	s.append(Event{Type: EventItemSkipped, Payload: payload})
	s.mu.Unlock()

	s.Close()

	s2, err := Open(root, s.id)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s2.Close()

	item := s2.plan.Item(id)
	if item == nil {
		t.Fatal("item not found")
	}
	if item.Status != ItemSkipped {
		t.Errorf("status = %q, want skipped", item.Status)
	}
}

func TestReplay_ReorderAfterRemove(t *testing.T) {
	root := t.TempDir()

	s, err := Create(root, "reorder after remove")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	id1, _ := s.AddItem("A", "", "", nil, "")
	id2, _ := s.AddItem("B", "", "", nil, "")
	id3, _ := s.AddItem("C", "", "", nil, "")

	// Remove an item, then reorder.
	s.RemoveItem(id2)
	s.Reorder([]string{id3, id1})

	s.Close()

	s2, err := Open(root, s.id)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s2.Close()

	if len(s2.plan.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(s2.plan.Items))
	}
	if s2.plan.Items[0].ID != id3 {
		t.Errorf("first item = %q, want %q", s2.plan.Items[0].ID, id3)
	}
	if s2.plan.Items[1].ID != id1 {
		t.Errorf("second item = %q, want %q", s2.plan.Items[1].ID, id1)
	}
}
