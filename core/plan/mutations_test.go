// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plan

import (
	"testing"
)

// setupPlan creates a fresh store with some initial items for mutation tests.
func setupPlan(t *testing.T, root string) *Store {
	t.Helper()
	s, err := Create(root, "test plan")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return s
}

func TestMutAddItem(t *testing.T) {
	root := t.TempDir()
	s := setupPlan(t, root)
	defer s.Close()

	tests := []struct {
		name        string
		title       string
		description string
		after       string
		dependsOn   []string
		role        string
		wantErr     bool
	}{
		{
			name:        "first_item",
			title:       "Setup DB",
			description: "Create the database schema",
			wantErr:     false,
		},
		{
			name:        "second_item",
			title:       "Write API",
			description: "Implement the REST API",
			dependsOn:   []string{"item-1"},
			role:        "coder",
			wantErr:     false,
		},
		{
			name:        "insert_after",
			title:       "Design API",
			description: "Design the API spec",
			after:       "item-1",
			wantErr:     false,
		},
		{
			name:    "insert_after_nonexistent",
			title:   "Orphan",
			after:   "item-99",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runAddItemCase(t, s, tt.title, tt.description, tt.after, tt.dependsOn, tt.role, tt.wantErr)
		})
	}
}

func runAddItemCase(t *testing.T, s *Store, title, description, after string, dependsOn []string, role string, wantErr bool) {
	t.Helper()
	id, err := s.AddItem(title, description, after, dependsOn, role)
	if wantErr {
		if err == nil {
			t.Error("expected error, got nil")
		}
		return
	}
	if err != nil {
		t.Fatalf("AddItem: %v", err)
	}
	if id == "" {
		t.Error("expected non-empty ID")
	}
	item := s.plan.Item(id)
	if item == nil {
		t.Fatalf("Item %q not found after AddItem", id)
	}
	if item.Title != title {
		t.Errorf("Title = %q, want %q", item.Title, title)
	}
	if item.Status != ItemPending {
		t.Errorf("Status = %q, want pending", item.Status)
	}
}

func TestMutUpdateItem(t *testing.T) {
	root := t.TempDir()
	s := setupPlan(t, root)
	defer s.Close()

	id, err := s.AddItem("Original Title", "Original desc", "", nil, "")
	if err != nil {
		t.Fatalf("AddItem: %v", err)
	}

	title := "Updated Title"
	desc := "Updated desc"

	t.Run("happy_path", func(t *testing.T) {
		err := s.UpdateItem(id, PlanItemPatch{
			Title:       &title,
			Description: &desc,
		})
		if err != nil {
			t.Fatalf("UpdateItem: %v", err)
		}
		item := s.plan.Item(id)
		if item.Title != "Updated Title" {
			t.Errorf("Title = %q", item.Title)
		}
		if item.Description != "Updated desc" {
			t.Errorf("Description = %q", item.Description)
		}
	})

	t.Run("not_found", func(t *testing.T) {
		err := s.UpdateItem("item-99", PlanItemPatch{Title: &title})
		if err == nil {
			t.Error("expected error for unknown item")
		}
	})
}

func TestMutRemoveItem(t *testing.T) {
	root := t.TempDir()
	s := setupPlan(t, root)
	defer s.Close()

	id1, _ := s.AddItem("Item 1", "", "", nil, "")
	id2, _ := s.AddItem("Item 2", "", "", []string{id1}, "")

	t.Run("has_dependents", func(t *testing.T) {
		err := s.RemoveItem(id1)
		if err == nil {
			t.Error("expected error for item with dependents")
		}
	})

	t.Run("success", func(t *testing.T) {
		err := s.RemoveItem(id2)
		if err != nil {
			t.Fatalf("RemoveItem: %v", err)
		}
		if s.plan.Item(id2) != nil {
			t.Error("item should not exist after removal")
		}
	})

	t.Run("not_found", func(t *testing.T) {
		err := s.RemoveItem("item-99")
		if err == nil {
			t.Error("expected error for unknown item")
		}
	})
}

func TestMutReorder(t *testing.T) {
	root := t.TempDir()
	s := setupPlan(t, root)
	defer s.Close()

	id1, _ := s.AddItem("A", "", "", nil, "")
	id2, _ := s.AddItem("B", "", "", nil, "")
	id3, _ := s.AddItem("C", "", "", nil, "")

	t.Run("valid_permutation", func(t *testing.T) {
		err := s.Reorder([]string{id3, id1, id2})
		if err != nil {
			t.Fatalf("Reorder: %v", err)
		}
		if s.plan.Items[0].ID != id3 {
			t.Errorf("first item = %q", s.plan.Items[0].ID)
		}
		if s.plan.Items[1].ID != id1 {
			t.Errorf("second item = %q", s.plan.Items[1].ID)
		}
	})

	t.Run("wrong_length", func(t *testing.T) {
		err := s.Reorder([]string{id1, id2})
		if err == nil {
			t.Error("expected error for wrong length")
		}
	})

	t.Run("unknown_id", func(t *testing.T) {
		err := s.Reorder([]string{id1, id2, "item-99"})
		if err == nil {
			t.Error("expected error for unknown ID")
		}
	})

	t.Run("duplicate_id", func(t *testing.T) {
		err := s.Reorder([]string{id1, id2, id2})
		if err == nil {
			t.Error("expected error for duplicate ID")
		}
	})
}

func TestMutComments(t *testing.T) {
	s := setupPlan(t, t.TempDir())
	defer s.Close()
	itemID := mustAddCommentTestItem(t, s)
	commentID := addPlanComment(t, s)
	addItemComment(t, s, itemID)
	assertEmptyCommentRejected(t, s)
	updateComment(t, s, commentID)
	resolveComment(t, s, commentID)
	assertMissingCommentRejected(t, s)
	removeComment(t, s, commentID)
}

func mustAddCommentTestItem(t *testing.T, s *Store) string {
	t.Helper()
	id, err := s.AddItem("Task", "", "", nil, "")
	if err != nil {
		t.Fatalf("AddItem: %v", err)
	}
	return id
}

func addPlanComment(t *testing.T, s *Store) string {
	t.Helper()
	id, err := s.AddComment("", "plan-level comment")
	if err != nil {
		t.Fatalf("AddComment: %v", err)
	}
	return id
}

func addItemComment(t *testing.T, s *Store, itemID string) {
	t.Helper()
	if _, err := s.AddComment(itemID, "item comment"); err != nil {
		t.Fatalf("AddComment: %v", err)
	}
}

func assertEmptyCommentRejected(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.AddComment("", ""); err == nil {
		t.Error("expected error for empty content")
	}
}

func updateComment(t *testing.T, s *Store, id string) {
	t.Helper()
	if err := s.UpdateComment(id, "updated content"); err != nil {
		t.Fatalf("UpdateComment: %v", err)
	}
	assertCommentContent(t, s, id, "updated content")
}

func resolveComment(t *testing.T, s *Store, id string) {
	t.Helper()
	if err := s.ResolveComment(id, "fixed"); err != nil {
		t.Fatalf("ResolveComment: %v", err)
	}
	for _, comment := range s.plan.Comments {
		if comment.ID == id && !comment.Resolved {
			t.Error("comment should be resolved")
		}
	}
}

func assertMissingCommentRejected(t *testing.T, s *Store) {
	t.Helper()
	if err := s.UpdateComment("c-nonexistent", "content"); err == nil {
		t.Error("expected error")
	}
}

func removeComment(t *testing.T, s *Store, id string) {
	t.Helper()
	if err := s.RemoveComment(id); err != nil {
		t.Fatalf("RemoveComment: %v", err)
	}
	for _, comment := range s.plan.Comments {
		if comment.ID == id {
			t.Error("comment should be removed")
		}
	}
}

func assertCommentContent(t *testing.T, s *Store, id, want string) {
	t.Helper()
	for _, comment := range s.plan.Comments {
		if comment.ID == id && comment.Content != want {
			t.Errorf("Content = %q, want %q", comment.Content, want)
		}
	}
}

func TestMutSubmitRevision(t *testing.T) {
	root := t.TempDir()
	s := setupPlan(t, root)
	defer s.Close()

	t.Run("first_submit", func(t *testing.T) {
		err := s.SubmitRevision()
		if err != nil {
			t.Fatalf("SubmitRevision: %v", err)
		}
		if s.plan.Revision != 1 {
			t.Errorf("Revision = %d, want 1", s.plan.Revision)
		}
		if s.plan.Status != PlanInReview {
			t.Errorf("Status = %q, want in_review", s.plan.Status)
		}
	})

	t.Run("second_submit", func(t *testing.T) {
		err := s.SubmitRevision()
		if err != nil {
			t.Fatalf("SubmitRevision: %v", err)
		}
		if s.plan.Revision != 2 {
			t.Errorf("Revision = %d, want 2", s.plan.Revision)
		}
	})
}

func TestMutApproveAndExecute(t *testing.T) {
	root := t.TempDir()
	s := setupPlan(t, root)
	defer s.Close()

	// Must be in_review first.
	s.SubmitRevision()

	t.Run("approve", func(t *testing.T) {
		err := s.Approve()
		if err != nil {
			t.Fatalf("Approve: %v", err)
		}
		if s.plan.Status != PlanApproved {
			t.Errorf("Status = %q, want approved", s.plan.Status)
		}
	})

	t.Run("approve_not_in_review", func(t *testing.T) {
		s2 := setupPlan(t, t.TempDir())
		defer s2.Close()
		err := s2.Approve()
		if err == nil {
			t.Error("expected error for draft plan")
		}
	})

	t.Run("start_execution", func(t *testing.T) {
		err := s.StartExecution("run-abc123")
		if err != nil {
			t.Fatalf("StartExecution: %v", err)
		}
		if s.plan.Status != PlanExecuting {
			t.Errorf("Status = %q, want executing", s.plan.Status)
		}
		if s.plan.RunID != "run-abc123" {
			t.Errorf("RunID = %q", s.plan.RunID)
		}
	})

	t.Run("execute_not_approved", func(t *testing.T) {
		err := s.StartExecution("run-xyz")
		if err != nil {
			t.Fatalf("StartExecution on executing: %v", err)
		}
	})
}

func TestMutStartItem_Sequential(t *testing.T) {
	root := t.TempDir()
	s := setupPlan(t, root)
	defer s.Close()

	// Setup: approve and execute.
	s.SubmitRevision()
	s.Approve()
	s.StartExecution("run-test")

	// Add items with dependency chain.
	id1, _ := s.AddItem("Item 1", "desc1", "", nil, "")
	id2, _ := s.AddItem("Item 2", "desc2", "", []string{id1}, "")
	id3, _ := s.AddItem("Item 3", "desc3", "", []string{id2}, "")

	t.Run("start_first_item", func(t *testing.T) {
		err := s.StartItem(id1, "coder", "agent-1")
		if err != nil {
			t.Fatalf("StartItem: %v", err)
		}
		if s.plan.Item(id1).Status != ItemInProgress {
			t.Errorf("Status = %q, want in_progress", s.plan.Item(id1).Status)
		}
	})

	t.Run("second_in_flight_rejected", func(t *testing.T) {
		err := s.StartItem(id2, "coder", "agent-2")
		if err == nil {
			t.Error("expected error for second in-flight item")
		}
	})

	t.Run("complete_and_start_next", func(t *testing.T) {
		if err := s.CompleteItem(id1, "done"); err != nil {
			t.Fatalf("CompleteItem: %v", err)
		}
		if err := s.StartItem(id2, "coder", "agent-2"); err != nil {
			t.Fatalf("StartItem id2: %v", err)
		}
		if s.plan.Item(id2).Status != ItemInProgress {
			t.Errorf("Status = %q, want in_progress", s.plan.Item(id2).Status)
		}
	})

	t.Run("start_with_unsatisfied_dep", func(t *testing.T) {
		// id3 depends on id2 which is in_progress.
		err := s.StartItem(id3, "coder", "agent-3")
		if err == nil {
			t.Error("expected error for unsatisfied dependency")
		}
	})
}

func TestMutBlockAndSkip(t *testing.T) {
	s := setupExecutingPlan(t)
	defer s.Close()
	id1, _ := s.AddItem("Item 1", "", "", nil, "")
	id2, _ := s.AddItem("Item 2", "", "", []string{id1}, "")
	id3, _ := s.AddItem("Item 3", "", "", []string{id2}, "")
	assertBlockedByDependency(t, s, id2)
	completeItem(t, s, id1, "agent-1")
	blockItem(t, s, id2)
	skipItem(t, s, id2)
	startItem(t, s, id3, "agent-3")
	assertCannotSkipInProgress(t, s, id3)
	s.CompleteItem(id3, "done")
	assertCannotSkipDone(t, s, id3)
}

func setupExecutingPlan(t *testing.T) *Store {
	t.Helper()
	s := setupPlan(t, t.TempDir())
	if err := s.SubmitRevision(); err != nil {
		t.Fatal(err)
	}
	if err := s.Approve(); err != nil {
		t.Fatal(err)
	}
	if err := s.StartExecution("run-test"); err != nil {
		t.Fatal(err)
	}
	return s
}

func assertBlockedByDependency(t *testing.T, s *Store, id string) {
	t.Helper()
	if err := s.StartItem(id, "coder", "agent"); err == nil {
		t.Error("expected error for unsatisfied dep")
	}
}

func completeItem(t *testing.T, s *Store, id, agent string) {
	t.Helper()
	if err := s.StartItem(id, "coder", agent); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteItem(id, "done"); err != nil {
		t.Fatal(err)
	}
}

func blockItem(t *testing.T, s *Store, id string) {
	t.Helper()
	if err := s.StartItem(id, "coder", "agent-2"); err != nil {
		t.Fatal(err)
	}
	if err := s.BlockItem(id, "missing credentials"); err != nil {
		t.Fatalf("BlockItem: %v", err)
	}
	if s.plan.Item(id).Status != ItemBlocked {
		t.Errorf("Status = %q, want blocked", s.plan.Item(id).Status)
	}
}

func skipItem(t *testing.T, s *Store, id string) {
	t.Helper()
	if err := s.SkipItem(id, "not needed"); err != nil {
		t.Fatalf("SkipItem: %v", err)
	}
	if s.plan.Item(id).Status != ItemSkipped {
		t.Errorf("Status = %q, want skipped", s.plan.Item(id).Status)
	}
}

func startItem(t *testing.T, s *Store, id, agent string) {
	t.Helper()
	if err := s.StartItem(id, "coder", agent); err != nil {
		t.Fatalf("StartItem after skip: %v", err)
	}
}

func assertCannotSkipInProgress(t *testing.T, s *Store, id string) {
	t.Helper()
	if err := s.SkipItem(id, "why not"); err == nil {
		t.Error("expected error for skipping in_progress item")
	}
}

func assertCannotSkipDone(t *testing.T, s *Store, id string) {
	t.Helper()
	if err := s.SkipItem(id, "already done"); err == nil {
		t.Error("expected error for skipping done item")
	}
}

func TestMutFinish(t *testing.T) {
	root := t.TempDir()
	s := setupPlan(t, root)
	defer s.Close()

	s.SubmitRevision()
	s.Approve()
	s.StartExecution("run-test")

	id1, _ := s.AddItem("Item 1", "", "", nil, "")
	s.StartItem(id1, "coder", "agent-1")
	s.CompleteItem(id1, "done")

	t.Run("finish", func(t *testing.T) {
		err := s.Finish()
		if err != nil {
			t.Fatalf("Finish: %v", err)
		}
		if s.plan.Status != PlanDone {
			t.Errorf("Status = %q, want done", s.plan.Status)
		}
	})
}

func TestMutFail(t *testing.T) {
	root := t.TempDir()
	s := setupPlan(t, root)
	defer s.Close()

	t.Run("fail", func(t *testing.T) {
		err := s.Fail("run timeout")
		if err != nil {
			t.Fatalf("Fail: %v", err)
		}
		if s.plan.Status != PlanFailed {
			t.Errorf("Status = %q, want failed", s.plan.Status)
		}
	})
}

func TestMutClarification(t *testing.T) {
	root := t.TempDir()
	s := setupPlan(t, root)
	defer s.Close()

	id, _ := s.AddItem("Item 1", "", "", nil, "")

	t.Run("record_clarification", func(t *testing.T) {
		err := s.RecordClarification(id, "What port?", "8080")
		if err != nil {
			t.Fatalf("RecordClarification: %v", err)
		}
	})

	t.Run("empty_question_and_answer", func(t *testing.T) {
		err := s.RecordClarification(id, "", "")
		if err == nil {
			t.Error("expected error for empty question and answer")
		}
	})

	t.Run("nonexistent_item", func(t *testing.T) {
		err := s.RecordClarification("item-99", "question", "answer")
		if err == nil {
			t.Error("expected error for nonexistent item")
		}
	})
}
