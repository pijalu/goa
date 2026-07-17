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
			name:        "insert_after_nonexistent",
			title:       "Orphan",
			after:       "item-99",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := s.AddItem(tt.title, tt.description, tt.after, tt.dependsOn, tt.role)
			if tt.wantErr {
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
			if item.Title != tt.title {
				t.Errorf("Title = %q, want %q", item.Title, tt.title)
			}
			if item.Status != ItemPending {
				t.Errorf("Status = %q, want pending", item.Status)
			}
		})
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
	root := t.TempDir()
	s := setupPlan(t, root)
	defer s.Close()
	id, _ := s.AddItem("Task", "", "", nil, "")

	var commentID string

	t.Run("add_plan_comment", func(t *testing.T) {
		cid, err := s.AddComment("", "plan-level comment")
		if err != nil {
			t.Fatalf("AddComment: %v", err)
		}
		commentID = cid
	})

	t.Run("add_item_comment", func(t *testing.T) {
		_, err := s.AddComment(id, "item comment")
		if err != nil {
			t.Fatalf("AddComment: %v", err)
		}
	})

	t.Run("add_empty_comment", func(t *testing.T) {
		_, err := s.AddComment("", "")
		if err == nil {
			t.Error("expected error for empty content")
		}
	})

	t.Run("update_comment", func(t *testing.T) {
		err := s.UpdateComment(commentID, "updated content")
		if err != nil {
			t.Fatalf("UpdateComment: %v", err)
		}
		for _, c := range s.plan.Comments {
			if c.ID == commentID && c.Content != "updated content" {
				t.Errorf("Content = %q", c.Content)
			}
		}
	})

	t.Run("resolve_comment", func(t *testing.T) {
		err := s.ResolveComment(commentID, "fixed")
		if err != nil {
			t.Fatalf("ResolveComment: %v", err)
		}
		for _, c := range s.plan.Comments {
			if c.ID == commentID && !c.Resolved {
				t.Error("comment should be resolved")
			}
		}
	})

	t.Run("update_nonexistent", func(t *testing.T) {
		err := s.UpdateComment("c-nonexistent", "content")
		if err == nil {
			t.Error("expected error")
		}
	})

	t.Run("remove_comment", func(t *testing.T) {
		err := s.RemoveComment(commentID)
		if err != nil {
			t.Fatalf("RemoveComment: %v", err)
		}
		for _, c := range s.plan.Comments {
			if c.ID == commentID {
				t.Error("comment should be removed")
			}
		}
	})
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
	root := t.TempDir()
	s := setupPlan(t, root)
	defer s.Close()

	s.SubmitRevision()
	s.Approve()
	s.StartExecution("run-test")

	id1, _ := s.AddItem("Item 1", "", "", nil, "")
	id2, _ := s.AddItem("Item 2", "", "", []string{id1}, "")
	id3, _ := s.AddItem("Item 3", "", "", []string{id2}, "")

	t.Run("block_blocked_by_dep", func(t *testing.T) {
		// Can't start id2 because id1 is pending.
		err := s.StartItem(id2, "coder", "agent")
		if err == nil {
			t.Error("expected error for unsatisfied dep")
		}
	})

	// Start item 1, complete it.
	s.StartItem(id1, "coder", "agent-1")
	s.CompleteItem(id1, "done")

	// Start item 2, block it.
	s.StartItem(id2, "coder", "agent-2")
	t.Run("block_item", func(t *testing.T) {
		err := s.BlockItem(id2, "missing credentials")
		if err != nil {
			t.Fatalf("BlockItem: %v", err)
		}
		if s.plan.Item(id2).Status != ItemBlocked {
			t.Errorf("Status = %q, want blocked", s.plan.Item(id2).Status)
		}
	})

	t.Run("skip_blocked_item", func(t *testing.T) {
		err := s.SkipItem(id2, "not needed")
		if err != nil {
			t.Fatalf("SkipItem: %v", err)
		}
		if s.plan.Item(id2).Status != ItemSkipped {
			t.Errorf("Status = %q, want skipped", s.plan.Item(id2).Status)
		}
	})

	t.Run("skip_unblocks_dependent", func(t *testing.T) {
		// id3 depends on id2 which is now skipped → dependency satisfied.
		err := s.StartItem(id3, "coder", "agent-3")
		if err != nil {
			t.Fatalf("StartItem after skip: %v", err)
		}
	})

	t.Run("cannot_skip_in_progress", func(t *testing.T) {
		// id3 is in_progress, can't skip it.
		err := s.SkipItem(id3, "why not")
		if err == nil {
			t.Error("expected error for skipping in_progress item")
		}
	})

	t.Run("cannot_skip_done", func(t *testing.T) {
		s.CompleteItem(id3, "done")
		err := s.SkipItem(id3, "already done")
		if err == nil {
			t.Error("expected error for skipping done item")
		}
	})
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
