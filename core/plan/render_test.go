// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plan

import (
	"strings"
	"testing"
)

func TestRender_Basic(t *testing.T) {
	p := &Plan{
		Name:      "happy.hare",
		Objective: "Build authentication system",
		Status:    PlanInReview,
		Revision:  2,
		Items: []PlanItem{
			{
				ID:          "item-1",
				Title:       "Setup database schema",
				Description: "Create the users and sessions tables",
				DependsOn:   nil,
				Role:        "coder",
				Status:      ItemDone,
				Result:      "Tables created",
			},
			{
				ID:          "item-2",
				Title:       "Implement login API",
				Description: "POST /api/login endpoint with JWT",
				DependsOn:   []string{"item-1"},
				Role:        "coder",
				Status:      ItemPending,
			},
			{
				ID:          "item-3",
				Title:       "Add email verification",
				Description: "Send verification email on registration",
				DependsOn:   []string{"item-1"},
				Role:        "",
				Status:      ItemBlocked,
			},
		},
	}

	md, anchors := Render(p)

	// Check header
	if !strings.Contains(md, "# Plan: happy.hare (revision 2)") {
		t.Errorf("header missing, got:\n%s", md)
	}
	if !strings.Contains(md, "**Objective:** Build authentication system") {
		t.Errorf("objective missing")
	}
	if !strings.Contains(md, "**Status:** in_review") {
		t.Errorf("status missing")
	}

	// Check items
	if !strings.Contains(md, "## 1. Setup database schema") {
		t.Errorf("item 1 heading missing")
	}
	if !strings.Contains(md, "## 2. Implement login API") {
		t.Errorf("item 2 heading missing")
	}
	if !strings.Contains(md, "## 3. Add email verification") {
		t.Errorf("item 3 heading missing")
	}

	// Check anchors
	if len(anchors) != 3 {
		t.Fatalf("expected 3 anchors, got %d: %v", len(anchors), anchors)
	}

	anchorCheck := func(line int, id string) {
		found := false
		for _, a := range anchors {
			if a.ItemID == id {
				found = true
				if a.Line <= 0 {
					t.Errorf("anchor %q has line %d", id, a.Line)
				}
				break
			}
		}
		if !found {
			t.Errorf("anchor for %q not found", id)
		}
	}
	anchorCheck(0, "item-1")
	anchorCheck(0, "item-2")
	anchorCheck(0, "item-3")

	// Check anchor comment markers in markdown
	for _, id := range []string{"item-1", "item-2", "item-3"} {
		marker := "<!-- anchor: " + id + " -->"
		if !strings.Contains(md, marker) {
			t.Errorf("anchor marker %q not found in render", marker)
		}
	}

	// Check status lines
	if !strings.Contains(md, "_Status: done_") {
		t.Errorf("item-1 status missing")
	}
	if !strings.Contains(md, "_Status: pending_") {
		t.Errorf("item-2 status missing")
	}
	if !strings.Contains(md, "_Status: blocked_") {
		t.Errorf("item-3 status missing")
	}

	// Check dependency references
	if !strings.Contains(md, "_Depends on: item-1") {
		t.Errorf("dependency reference missing")
	}

	// Check result line
	if !strings.Contains(md, "_Result:") {
		t.Errorf("result line missing for item-1")
	}

	// Check role lines
	if !strings.Contains(md, "_Role: coder_") {
		t.Errorf("role line missing for coder role")
	}
}

func TestRender_EmptyPlan(t *testing.T) {
	p := &Plan{
		Name:      "empty",
		Objective: "nothing",
		Status:    PlanDraft,
		Revision:  0,
	}

	md, anchors := Render(p)

	if !strings.Contains(md, "# Plan: empty (revision 0)") {
		t.Errorf("header missing: %s", md)
	}
	if len(anchors) != 0 {
		t.Errorf("expected no anchors for empty plan, got %d", len(anchors))
	}
}

func TestRender_AnchorStability(t *testing.T) {
	// Render, add comment, render again — anchors for unchanged items must match.
	p := &Plan{
		Name:     "stable",
		Revision: 1,
		Items: []PlanItem{
			{ID: "item-1", Title: "First", Status: ItemPending},
			{ID: "item-2", Title: "Second", Status: ItemPending},
		},
	}

	_, anchors1 := Render(p)

	// Add a comment (this adds lines to the render output).
	p.Comments = append(p.Comments, PlanComment{
		ID:      "c-1",
		ItemID:  "item-1",
		Content: "needs review",
	})

	md2, anchors2 := Render(p)

	// Item anchors should reference the same item IDs.
	if len(anchors1) != len(anchors2) {
		t.Fatalf("anchor count changed: %d vs %d", len(anchors1), len(anchors2))
	}
	for i := range anchors1 {
		if anchors1[i].ItemID != anchors2[i].ItemID {
			t.Errorf("anchor %d item changed: %q vs %q", i, anchors1[i].ItemID, anchors2[i].ItemID)
		}
		// Line numbers may change due to additional lines from comments.
	}

	// Verify comment appears in the output.
	if !strings.Contains(md2, "needs review") {
		t.Errorf("comment content not found in render:\n%s", md2)
	}
	if !strings.Contains(md2, "Comments") {
		t.Errorf("Comments section not found in render:\n%s", md2)
	}
}

func TestRender_Deterministic(t *testing.T) {
	p := &Plan{
		Name:     "deterministic",
		Revision: 1,
		Items: []PlanItem{
			{ID: "item-1", Title: "A", Status: ItemPending},
			{ID: "item-2", Title: "B", Status: ItemDone, Result: "ok"},
			{ID: "item-3", Title: "C", Status: ItemSkipped},
		},
		Comments: []PlanComment{
			{ID: "c-1", ItemID: "item-1", Content: "hello"},
		},
	}

	md1, _ := Render(p)
	md2, _ := Render(p)

	if md1 != md2 {
		t.Error("render is not deterministic")
	}
}

func TestRender_MapFreeIterationProof(t *testing.T) {
	// Render uses only slice iteration, not map iteration.
	// This test verifies item order is preserved.
	p := &Plan{
		Name: "ordered",
		Items: []PlanItem{
			{ID: "z", Title: "Z last", Status: ItemPending},
			{ID: "a", Title: "A first", Status: ItemPending},
		},
	}

	md, _ := Render(p)

	zPos := strings.Index(md, "Z last")
	aPos := strings.Index(md, "A first")

	if zPos > aPos {
		t.Error("items rendered out of order — A appears before Z, but Z is first in the slice")
	}
}
