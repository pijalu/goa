// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"testing"

	"github.com/pijalu/goa/core/plan"
)

func TestPlanPager_Navigation(t *testing.T) {
	s, err := plan.Create(t.TempDir(), "test nav")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer s.Close()

	// Add some items so the render has anchors.
	s.AddItem("First item", "Description 1", "", nil, "")
	s.AddItem("Second item", "Description 2", "", nil, "")
	s.AddItem("Third item", "Description 3", "", nil, "")

	p := NewPlanPager(s)
	p.SetViewport(80, 40)
	lines := p.Render(80)
	if len(lines) == 0 {
		t.Fatal("expected rendered lines")
	}

	// Verify initial cursor position.
	if p.cursor != 0 {
		t.Errorf("initial cursor = %d, want 0", p.cursor)
	}

	// Test navigation keys.
	p.HandleInput("down")
	if p.cursor != 1 {
		t.Errorf("after down cursor = %d, want 1", p.cursor)
	}

	p.HandleInput("up")
	if p.cursor != 0 {
		t.Errorf("after up cursor = %d, want 0", p.cursor)
	}
}

func TestPlanPager_ItemJump(t *testing.T) {
	s, err := plan.Create(t.TempDir(), "test jumps")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer s.Close()

	s.AddItem("A", "", "", nil, "")
	s.AddItem("B", "", "", nil, "")
	s.AddItem("C", "", "", nil, "")

	p := NewPlanPager(s)
	p.SetViewport(80, 40)

	// Jump to next item.
	p.HandleInput("n")
	// Cursor should now be on the first item heading.
	if p.cursor <= 0 {
		t.Errorf("after 'n' cursor = %d, expected > 0", p.cursor)
	}
	firstCursor := p.cursor

	// Jump to next again.
	p.HandleInput("n")
	if p.cursor <= firstCursor {
		t.Errorf("after second 'n' cursor should have moved, was %d now %d", firstCursor, p.cursor)
	}

	// Jump to previous.
	p.HandleInput("p")
	if p.cursor != firstCursor {
		t.Errorf("after 'p' cursor = %d, want %d", p.cursor, firstCursor)
	}
}

func TestPlanPager_KeyRouting(t *testing.T) {
	s, err := plan.Create(t.TempDir(), "test keys")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer s.Close()

	p := NewPlanPager(s)

	// Test that all keys are handled without crash.
	keys := []string{"up", "down", "k", "j", "pgup", "pgdn", "n", "p", "c", "e", "d", "s", "a", "q", "esc", "ctrl+c"}
	for _, key := range keys {
		p.HandleInput(key)
	}
}

func TestPlanPager_ApproveWithComments(t *testing.T) {
	s, err := plan.Create(t.TempDir(), "test approve")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer s.Close()

	id, _ := s.AddItem("Task", "", "", nil, "")
	s.AddComment(id, "needs review")

	p := NewPlanPager(s)

	approved := false
	p.OnApprovePlan = func() { approved = true }
	p.OnConfirm = func(q string, onResult func(bool)) { onResult(true) }

	p.HandleInput("a")

	if !approved {
		t.Error("expected approve callback to be called after confirm")
	}
}

func TestPlanPager_CommentRoundTrip(t *testing.T) {
	s, err := plan.Create(t.TempDir(), "test comments")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer s.Close()

	id, _ := s.AddItem("Task", "", "", nil, "")

	p := NewPlanPager(s)

	// Navigate to the item anchor and add a comment.
	p.HandleInput("n") // jump to first item

	var addedText string
	p.OnCommentRequest = func(title, current string, onSubmit func(string)) {
		onSubmit("This needs attention")
	}

	p.HandleInput("c")

	// Verify comment was added to the store.
	snap, _ := s.Snapshot()
	if len(snap.Comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(snap.Comments))
	}
	if snap.Comments[0].Content != "This needs attention" {
		t.Errorf("comment content = %q", snap.Comments[0].Content)
	}
	if snap.Comments[0].ItemID != id {
		t.Errorf("comment item = %q, want %q", snap.Comments[0].ItemID, id)
	}

	_ = addedText
}

func TestPlanPager_SubmitAnnotations(t *testing.T) {
	s, err := plan.Create(t.TempDir(), "test submit")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer s.Close()

	s.AddItem("Task", "", "", nil, "")

	p := NewPlanPager(s)

	var submitted string
	p.OnSubmitAnnotations = func(text string) { submitted = text }
	p.OnConfirm = func(q string, onResult func(bool)) { onResult(true) }

	p.HandleInput("s")

	if submitted == "" {
		t.Error("expected annotations to be submitted")
	}
	if !contains(submitted, "Plan Annotations") {
		t.Errorf("submission should contain 'Plan Annotations', got: %s", submitted)
	}
}

// contains reports whether s contains substr.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && containsStr(s, substr)
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
