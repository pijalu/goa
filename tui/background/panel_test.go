// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package background

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

func TestPanel_Render_NoTasks_ReturnsNil(t *testing.T) {
	p := NewPanel(func() []Task { return nil })
	lines := p.Render(80)
	if lines != nil {
		t.Fatalf("expected nil, got %d lines", len(lines))
	}
}

func TestPanel_Render_SingleTask(t *testing.T) {
	p := NewPanel(func() []Task {
		return []Task{{ID: "p1", Command: "npm run dev", Status: "running", PID: 1234}}
	})
	lines := p.Render(80)
	if len(lines) == 0 {
		t.Fatal("expected lines")
	}
	visible := ansi.Strip(strings.Join(lines, "\n"))
	if !strings.Contains(visible, "Background (1/1)") {
		t.Errorf("expected title, got:\n%s", visible)
	}
	if !strings.Contains(visible, "p1") || !strings.Contains(visible, "npm run dev") {
		t.Errorf("expected task info, got:\n%s", visible)
	}
}

func TestPanel_Render_MultipleTasks_CollapsesOverflow(t *testing.T) {
	p := NewPanel(func() []Task {
		return []Task{
			{ID: "p1", Command: "a", Status: "running"},
			{ID: "p2", Command: "b", Status: "running"},
			{ID: "p3", Command: "c", Status: "running"},
			{ID: "p4", Command: "d", Status: "running"},
		}
	})
	lines := p.Render(80)
	visible := ansi.Strip(strings.Join(lines, "\n"))
	if !strings.Contains(visible, "and 1 more") {
		t.Errorf("expected overflow message, got:\n%s", visible)
	}
}

func TestPanel_Render_RespectsWidth(t *testing.T) {
	p := NewPanel(func() []Task {
		return []Task{{ID: "p1", Command: strings.Repeat("x", 100), Status: "running", PID: 1}}
	})
	lines := p.Render(30)
	for i, line := range lines {
		if ansi.Width(line) != 30 {
			t.Errorf("line %d width = %d, want 30: %q", i, ansi.Width(line), line)
		}
	}
}

func TestPanel_SetSnapshot(t *testing.T) {
	p := NewPanel(func() []Task { return nil })
	p.SetSnapshot(func() []Task {
		return []Task{{ID: "p1", Command: "true", Status: "completed"}}
	})
	lines := p.Render(80)
	if len(lines) == 0 {
		t.Fatal("expected lines after snapshot update")
	}
}
