// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/internal/background"
	bgpanel "github.com/pijalu/goa/tui/background"
)

func TestTaskSnapshotsFromManager(t *testing.T) {
	mgr, err := background.NewManager("")
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	task, err := mgr.Start("true", "", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForTaskExit(t, mgr, task.ID, 2*time.Second)

	snapshots := taskSnapshotsFromManager(mgr)
	if len(snapshots) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(snapshots))
	}
	if snapshots[0].ID != task.ID {
		t.Errorf("ID mismatch: got %q, want %q", snapshots[0].ID, task.ID)
	}
	if snapshots[0].Command != "true" {
		t.Errorf("Command mismatch: got %q, want %q", snapshots[0].Command, "true")
	}
}

func TestBackgroundPanel_SnapshotFromManager(t *testing.T) {
	mgr, err := background.NewManager("")
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	task, err := mgr.Start("true", "", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForTaskExit(t, mgr, task.ID, 2*time.Second)

	panel := bgpanel.NewPanel(func() []bgpanel.Task {
		return taskSnapshotsFromManager(mgr)
	})
	lines := panel.Render(80)
	if len(lines) == 0 {
		t.Fatal("expected panel to render when tasks exist")
	}
	visible := ansi.Strip(strings.Join(lines, "\n"))
	if !strings.Contains(visible, "Background") {
		t.Errorf("expected panel title, got:\n%s", visible)
	}
	if !strings.Contains(visible, task.ID) {
		t.Errorf("expected task ID %q, got:\n%s", task.ID, visible)
	}
}

func waitForTaskExit(t *testing.T, mgr *background.Manager, id string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		task := mgr.Get(id)
		if task != nil && task.Status != background.StatusRunning {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("task %q did not exit within %v", id, timeout)
}
