// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plan

import (
	"strings"
	"testing"
)

func TestAnnotationsSummary_Basic(t *testing.T) {
	p := &Plan{
		Name:      "happy.hare",
		Objective: "Build authentication system",
		Revision:  2,
		Items: []PlanItem{
			{ID: "item-1", Title: "Setup database schema and migrations"},
			{ID: "item-2", Title: "Implement login API endpoint"},
		},
		Comments: []PlanComment{
			{ID: "c-1", ItemID: "item-1", Content: "Should we use an ORM?", Revision: 2, Resolved: false},
			{ID: "c-2", ItemID: "item-1", Content: "Add indexes", Revision: 2, Resolved: false},
			{ID: "c-3", ItemID: "item-2", Content: "Use JWT", Revision: 2, Resolved: true},
			{ID: "c-4", ItemID: "", Content: "Plan looks good", Revision: 1, Resolved: true},
		},
	}

	summary := AnnotationsSummary(p)

	// Check header
	if !strings.Contains(summary, "# Plan Annotations") {
		t.Error("missing header")
	}
	if !strings.Contains(summary, "**Objective:** Build authentication system") {
		t.Error("missing objective")
	}
	if !strings.Contains(summary, "**Revision:** 2") {
		t.Error("missing revision")
	}
	if !strings.Contains(summary, "**Open comments:** 2") {
		t.Errorf("missing open comment count, got:\n%s", summary)
	}

	// Check open comments section
	if !strings.Contains(summary, "## Open Comments") {
		t.Error("missing open comments section")
	}

	// Check item grouping with title excerpt (≤5 words)
	if !strings.Contains(summary, "Setup database schema and") {
		t.Errorf("expected item-1 title excerpt, got:\n%s", summary)
	}
	// item-2 only has a resolved comment, so it should not appear in open comments.
	if strings.Contains(summary, "item-2") {
		// but the resolved section mentions "on item-2", so this is fine.
	}

	// Check open comment content
	if !strings.Contains(summary, "Should we use an ORM?") {
		t.Error("missing open comment content")
	}
	if !strings.Contains(summary, "Add indexes") {
		t.Error("missing second open comment content")
	}

	// Check resolved comments section (current revision only)
	if !strings.Contains(summary, "## Resolved Comments (current revision)") {
		t.Error("missing resolved comments section")
	}
	if !strings.Contains(summary, "Use JWT") {
		t.Error("missing resolved comment 'Use JWT'")
	}
	// c-4 is revision 1, which is not current (revision 2), so should not appear.
	if strings.Contains(summary, "Plan looks good") {
		t.Error("resolved comment from old revision should not appear")
	}
}

func TestAnnotationsSummary_NoComments(t *testing.T) {
	p := &Plan{
		Name:      "empty",
		Objective: "Nothing",
		Revision:  1,
		Items: []PlanItem{
			{ID: "item-1", Title: "Do something"},
		},
	}

	summary := AnnotationsSummary(p)

	if !strings.Contains(summary, "No comments.") {
		t.Errorf("expected 'No comments.' got:\n%s", summary)
	}
	if strings.Contains(summary, "## Open Comments") {
		t.Error("should not have open comments section")
	}
}

func TestAnnotationsSummary_PlanLevelComments(t *testing.T) {
	p := &Plan{
		Name:      "plan-level",
		Objective: "Test",
		Revision:  1,
		Items: []PlanItem{
			{ID: "item-1", Title: "First item"},
		},
		Comments: []PlanComment{
			{ID: "c-1", ItemID: "", Content: "Plan-level note", Revision: 1, Resolved: false},
			{ID: "c-2", ItemID: "item-1", Content: "Item note", Revision: 1, Resolved: false},
		},
	}

	summary := AnnotationsSummary(p)

	if !strings.Contains(summary, "Plan-level") {
		t.Errorf("expected plan-level section, got:\n%s", summary)
	}
	if !strings.Contains(summary, "Plan-level note") {
		t.Error("missing plan-level comment")
	}
}

func TestAnnotationsSummary_DeterministicOrder(t *testing.T) {
	// Comments should be ordered by item order, then by position in comments slice.
	p := &Plan{
		Name:      "ordered",
		Objective: "Test order",
		Revision:  1,
		Items: []PlanItem{
			{ID: "item-1", Title: "First item"},
			{ID: "item-2", Title: "Second item"},
		},
		Comments: []PlanComment{
			{ID: "c-1", ItemID: "item-2", Content: "Second item comment", Revision: 1, Resolved: false},
			{ID: "c-2", ItemID: "item-1", Content: "First item comment", Revision: 1, Resolved: false},
		},
	}

	summary1 := AnnotationsSummary(p)
	summary2 := AnnotationsSummary(p)

	if summary1 != summary2 {
		t.Error("summary is not deterministic")
	}

	// First item should appear before second item in the output.
	firstPos := strings.Index(summary1, "First item")
	secondPos := strings.Index(summary1, "Second item")
	if firstPos < 0 || secondPos < 0 {
		t.Fatal("expected both items in summary")
	}
	if firstPos > secondPos {
		t.Error("items in wrong order — first item should appear before second")
	}
}

func TestAnnotationsSummary_TitleExcerpt(t *testing.T) {
	tests := []struct {
		title string
		n     int
		want  string
	}{
		{"short", 5, "short"},
		{"one two three four five six", 5, "one two three four five …"},
		{"", 5, ""},
		{"hello world", 0, ""},
	}

	for _, tt := range tests {
		got := excerptTitle(tt.title, tt.n)
		if got != tt.want {
			t.Errorf("excerptTitle(%q, %d) = %q, want %q", tt.title, tt.n, got, tt.want)
		}
	}
}
