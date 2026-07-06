// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/core/orchestrator"
	"github.com/pijalu/goa/tui"
)

func TestBrowser_RenderEmpty(t *testing.T) {
	b := NewBrowser(t.TempDir(), nil)
	lines := b.Render(60)
	if len(lines) == 0 {
		t.Fatal("expected rendered lines")
	}
	have := strings.Join(lines, "\n")
	if !strings.Contains(have, "no runs") {
		t.Errorf("expected 'no runs' in render, got:\n%s", have)
	}
}

func TestBrowser_RenderWithRuns(t *testing.T) {
	rootDir := t.TempDir()
	store := orchestrator.NewFileEventStore(rootDir, "run-1")
	_ = store.Append(orchestrator.Event{Type: orchestrator.EventRunStarted, Payload: map[string]any{"name": "happy.hare", "objective": "refactor auth", "topology": "fanout"}})

	closed := false
	b := NewBrowser(rootDir, func() { closed = true })
	lines := b.Render(80)
	have := strings.Join(lines, "\n")
	if !strings.Contains(have, "happy.hare") {
		t.Errorf("expected run name in render, got:\n%s", have)
	}
	if !strings.Contains(have, "fanout") {
		t.Errorf("expected topology in render, got:\n%s", have)
	}
	if !strings.Contains(have, "refactor auth") {
		t.Errorf("expected objective in render, got:\n%s", have)
	}

	b.HandleInput("q")
	if !closed {
		t.Error("expected close callback to be invoked on q")
	}
}

func TestBrowser_Navigation(t *testing.T) {
	rootDir := t.TempDir()
	for _, id := range []string{"run-1", "run-2"} {
		store := orchestrator.NewFileEventStore(rootDir, id)
		_ = store.Append(orchestrator.Event{Type: orchestrator.EventRunStarted, Payload: map[string]any{"name": id, "objective": "obj"}})
	}

	b := NewBrowser(rootDir, nil)
	lines := b.Render(80)
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 lines, got %d", len(lines))
	}
	b.HandleInput(tui.KeyDown)
	b.HandleInput(tui.KeyUp)
}
