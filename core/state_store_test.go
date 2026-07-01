// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pijalu/goa/internal"
)

func TestStateStore_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	ss := NewStateStore(dir)

	snap := SessionStateSnapshot{
		ModeState: internal.ModeState{
			Major:    internal.MajorPlanner,
			Skills:   []string{"test-gen"},
			Autonomy: internal.AutonomyReview,
		},
		MinorMode: "review",
	}

	if err := ss.Save(snap); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := ss.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if loaded.ModeState.Major != snap.ModeState.Major {
		t.Errorf("Major = %q, want %q", loaded.ModeState.Major, snap.ModeState.Major)
	}
	if len(loaded.ModeState.Skills) != 1 || loaded.ModeState.Skills[0] != "test-gen" {
		t.Errorf("Skills = %v, want [test-gen]", loaded.ModeState.Skills)
	}
	if loaded.ModeState.Autonomy != snap.ModeState.Autonomy {
		t.Errorf("Autonomy = %q, want %q", loaded.ModeState.Autonomy, snap.ModeState.Autonomy)
	}
	if loaded.MinorMode != snap.MinorMode {
		t.Errorf("MinorMode = %q, want %q", loaded.MinorMode, snap.MinorMode)
	}
}

func TestStateStore_LoadMissing(t *testing.T) {
	dir := t.TempDir()
	ss := NewStateStore(dir)

	snap, err := ss.Load()
	if err != nil {
		t.Fatalf("Load on missing file should not error: %v", err)
	}
	if snap.ModeState.Major != "" {
		t.Errorf("expected zero ModeState, got %+v", snap.ModeState)
	}
	if snap.MinorMode != "" {
		t.Errorf("expected empty MinorMode, got %q", snap.MinorMode)
	}
}

func TestStateStore_Overwrite(t *testing.T) {
	dir := t.TempDir()
	ss := NewStateStore(dir)

	_ = ss.Save(SessionStateSnapshot{
		ModeState: internal.ModeState{Major: internal.MajorCoder},
		MinorMode: "",
	})
	_ = ss.Save(SessionStateSnapshot{
		ModeState: internal.ModeState{Major: internal.MajorReviewer},
		MinorMode: "review",
	})

	loaded, _ := ss.Load()
	if loaded.ModeState.Major != internal.MajorReviewer {
		t.Errorf("Major = %q, want reviewer", loaded.ModeState.Major)
	}
	if loaded.MinorMode != "review" {
		t.Errorf("MinorMode = %q, want review", loaded.MinorMode)
	}
}

func TestStateStore_FileLocation(t *testing.T) {
	dir := t.TempDir()
	ss := NewStateStore(dir)
	_ = ss.Save(SessionStateSnapshot{})

	expected := filepath.Join(dir, ".goa", "state.json")
	if _, err := os.Stat(expected); os.IsNotExist(err) {
		t.Errorf("expected state file at %s", expected)
	}
}

func TestStateStore_ThinkingLevelRoundTrip(t *testing.T) {
	dir := t.TempDir()
	ss := NewStateStore(dir)

	snap := SessionStateSnapshot{
		ModeState:     internal.ModeState{Major: internal.MajorCoder, Autonomy: internal.AutonomyYolo},
		MinorMode:     "companion",
		ThinkingLevel: "high",
	}

	if err := ss.Save(snap); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := ss.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if loaded.ThinkingLevel != "high" {
		t.Errorf("ThinkingLevel = %q, want %q", loaded.ThinkingLevel, "high")
	}
	if loaded.MinorMode != "companion" {
		t.Errorf("MinorMode = %q, want %q", loaded.MinorMode, "companion")
	}
}
