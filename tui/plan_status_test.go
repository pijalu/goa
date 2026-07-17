// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/core/plan"
)

func TestPlanStatusOverlay_Render(t *testing.T) {
	s, err := plan.Create(t.TempDir(), "test status")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer s.Close()

	// Add items with various statuses.
	id1, _ := s.AddItem("Task A", "Description A", "", nil, "coder")
	s.AddItem("Task B", "Description B", "", nil, "")
	s.SubmitRevision()
	s.Approve()
	s.StartExecution("run-test")
	s.StartItem(id1, "coder", "agent-1")
	s.CompleteItem(id1, "completed A")

	o := NewPlanStatusOverlay(s)
	o.SetViewport(80, 40)

	lines := o.Render(80)
	if len(lines) == 0 {
		t.Fatal("expected rendered lines")
	}

	rendered := strings.Join(lines, "\n")
	if !strings.Contains(rendered, "Task A") {
		t.Error("expected Task A in output")
	}
	if !strings.Contains(rendered, "Task B") {
		t.Error("expected Task B in output")
	}
	if !strings.Contains(rendered, "☑") {
		t.Error("expected done glyph")
	}
	if !strings.Contains(rendered, "☐") {
		t.Error("expected pending glyph")
	}
}

func TestPlanStatusOverlay_Navigation(t *testing.T) {
	s, err := plan.Create(t.TempDir(), "test nav")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer s.Close()

	s.AddItem("A", "", "", nil, "")
	s.AddItem("B", "", "", nil, "")

	o := NewPlanStatusOverlay(s)
	o.SetViewport(80, 40)

	if o.cursor != 0 {
		t.Errorf("initial cursor = %d, want 0", o.cursor)
	}

	o.HandleInput("down")
	if o.cursor != 1 {
		t.Errorf("after down cursor = %d, want 1", o.cursor)
	}

	o.HandleInput("up")
	if o.cursor != 0 {
		t.Errorf("after up cursor = %d, want 0", o.cursor)
	}
}

func TestPlanStatusOverlay_ToggleDetail(t *testing.T) {
	s, err := plan.Create(t.TempDir(), "test detail")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer s.Close()

	id1, _ := s.AddItem("Task A", "Description A", "", nil, "")

	o := NewPlanStatusOverlay(s)
	o.SetViewport(80, 40)

	// Toggle detail on.
	o.HandleInput("enter")
	if o.detailItem != id1 {
		t.Errorf("detailItem = %q, want %q", o.detailItem, id1)
	}

	// Toggle detail off.
	o.HandleInput("enter")
	if o.detailItem != "" {
		t.Error("detailItem should be empty after toggle off")
	}
}

func TestPlanStatusOverlay_Close(t *testing.T) {
	s, err := plan.Create(t.TempDir(), "test close")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer s.Close()

	o := NewPlanStatusOverlay(s)
	closed := false
	o.OnClose = func() { closed = true }

	o.HandleInput("q")
	if !closed {
		t.Error("expected OnClose to be called")
	}
}

func TestPlanStatusOverlay_AllStatuses(t *testing.T) {
	s, err := plan.Create(t.TempDir(), "test statuses")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer s.Close()

	s.AddItem("Pending", "", "", nil, "")
	id2, _ := s.AddItem("InProgress", "", "", nil, "")
	id3, _ := s.AddItem("Done", "", "", nil, "")
	s.SubmitRevision()
	s.Approve()
	s.StartExecution("run-test")
	s.StartItem(id2, "coder", "agent")
	s.StartItem(id3, "coder", "agent-2")
	s.CompleteItem(id3, "ok")

	// Manually set Blocked and Skipped via the in-memory plan.
	p := s.Plan()
	p.Item(id2).Status = plan.ItemBlocked
	p.Item("item-1").Status = plan.ItemSkipped

	o := NewPlanStatusOverlay(s)
	o.SetViewport(80, 40)

	lines := o.Render(80)
	rendered := strings.Join(lines, "\n")

	glyphChecks := []struct {
		status string
		glyph  string
	}{
		{string(plan.ItemPending), "☐"},
		{string(plan.ItemBlocked), "✖"},
		{string(plan.ItemSkipped), "–"},
	}

	for _, gc := range glyphChecks {
		if !strings.Contains(rendered, gc.glyph) {
			t.Errorf("expected glyph %q for status %s", gc.glyph, gc.status)
		}
	}
}
