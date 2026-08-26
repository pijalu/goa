// SPDX-License-Identifier: GPL-3.0-or-later
package goal

import (
	"path/filepath"
	"testing"
)

// TestFriendlyNamesFromEventLog pins the ID→alias distillation: create
// records carry both fields, later writes win, noise (updates/clears,
// missing name or ID) is ignored, and an empty log yields an empty map.
func TestFriendlyNamesFromEventLog(t *testing.T) {
	id := func(s string) *string { return &s }
	records := []GoalEventRecord{
		{Type: GoalEventCreate, GoalID: id("goal-aaa"), Name: id("calm.otter")},
		{Type: GoalEventUpdate, GoalID: id("goal-aaa"), Status: id("completed")},
		{Type: GoalEventCreate, GoalID: id("goal-bbb"), Name: id("cheery.swan")},
		{Type: GoalEventClear},
		{Type: GoalEventCreate, GoalID: id("goal-ccc")}, // unnamed: no entry
		{Type: GoalEventUpdate, GoalID: id("goal-bbb"), Name: id("renamed.alias")},
	}
	got := FriendlyNamesFromEventLog(records)
	if len(got) != 2 {
		t.Fatalf("names = %v, want 2 entries", got)
	}
	if got["goal-aaa"] != "calm.otter" || got["goal-bbb"] != "renamed.alias" {
		t.Errorf("mapping wrong: %v", got)
	}
	if empty := FriendlyNamesFromEventLog(nil); len(empty) != 0 {
		t.Errorf("empty log must yield empty map, got %v", empty)
	}
}

// TestGoalMode_GoalFriendlyNames drives the method through a real event
// store; a mode without a store yields an empty mapping without panicking.
func TestGoalMode_GoalFriendlyNames(t *testing.T) {
	store := NewFileEventStore(filepath.Join(t.TempDir(), "goal-events.jsonl"))
	name := "tidy.falcon"
	obj := "objective"
	id := "goal-feedface"
	if err := store.Append(GoalEventRecord{
		Type:      GoalEventCreate,
		GoalID:    &id,
		Name:      &name,
		Objective: &obj,
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	mode := NewGoalMode(store, nil, nil, nil)
	names := mode.GoalFriendlyNames()
	if len(names) != 1 || names[id] != name {
		t.Errorf("names = %v, want {%q: %q}", names, id, name)
	}

	// No store wired — display surfaces must degrade to opaque IDs silently.
	bare := NewGoalMode(nil, nil, nil, nil)
	if got := bare.GoalFriendlyNames(); len(got) != 0 {
		t.Errorf("storeless names = %v, want empty", got)
	}
}
