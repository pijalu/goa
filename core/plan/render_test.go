// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plan

import (
	"strings"
	"testing"
)

func assertBasicRenderAnchors(t *testing.T, md string, anchors []LineAnchor) {
	t.Helper()
	if len(anchors) != 3 {
		t.Fatalf("expected 3 anchors, got %d: %v", len(anchors), anchors)
	}
	for _, id := range []string{"item-1", "item-2", "item-3"} {
		found := false
		for _, anchor := range anchors {
			if anchor.ItemID == id {
				found = true
				if anchor.Line <= 0 {
					t.Errorf("anchor %q has line %d", id, anchor.Line)
				}
				break
			}
		}
		if !found {
			t.Errorf("anchor for %q not found", id)
		}
		if !strings.Contains(md, "<!-- anchor: "+id+" -->") {
			t.Errorf("anchor marker for %q not found in render", id)
		}
	}
}

func assertBasicRenderDetails(t *testing.T, md string) {
	t.Helper()
	checks := map[string]string{
		"done status": "_Status: done_", "pending status": "_Status: pending_", "blocked status": "_Status: blocked_",
		"dependency": "_Depends on: item-1", "result": "_Result:", "role": "_Role: coder_",
	}
	for name, text := range checks {
		if !strings.Contains(md, text) {
			t.Errorf("%s missing", name)
		}
	}
}

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

	assertRenderContains(t, md, map[string]string{
		"header": "# Plan: happy.hare (revision 2)", "objective": "**Objective:** Build authentication system", "status": "**Status:** in_review",
		"item 1": "## 1. Setup database schema", "item 2": "## 2. Implement login API", "item 3": "## 3. Add email verification",
	})
	assertBasicRenderAnchors(t, md, anchors)
	assertBasicRenderDetails(t, md)
}

func assertRenderContains(t *testing.T, md string, expected map[string]string) {
	t.Helper()
	for name, text := range expected {
		if !strings.Contains(md, text) {
			t.Errorf("%s missing", name)
		}
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
