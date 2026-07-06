// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pijalu/goa/core/goal"
)

func TestGoalQueueStore_CleanupExpired(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.json")
	old := time.Now().AddDate(0, 0, -10)
	file := queuedGoalsFile{
		Version: 1,
		Goals: []goal.UpcomingGoal{
			{ID: "g1", Objective: "old", UpdatedAt: old},
			{ID: "g2", Objective: "fresh", UpdatedAt: time.Now()},
		},
	}
	data, _ := json.MarshalIndent(file, "", "  ")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write queue: %v", err)
	}

	s := NewGoalQueueStore(path)
	removed, err := s.CleanupExpired(7)
	if err != nil {
		t.Fatalf("CleanupExpired: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}
	goals, _ := s.Read()
	if len(goals) != 1 || goals[0].ID != "g2" {
		t.Errorf("remaining goals = %v, want [g2]", goals)
	}
}
