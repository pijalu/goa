// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/core/goal"
)

// TestGoalQueueStore_NotifiesEveryMutation: every mutator must fire the change
// hook exactly once per successful call. The hook is the ONLY notification
// path for queue changes, so a mutator that skips it freezes the footer's ◈
// goal count at its last value (the reported bug: the count stayed at 1 while
// goals:list showed a much deeper queue).
func TestGoalQueueStore_NotifiesEveryMutation(t *testing.T) {
	store := NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))
	calls := 0
	store.SetOnChange(func() { calls++ })

	expect := func(name string, want int, fn func() error) {
		t.Helper()
		calls = 0
		if err := fn(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if calls != want {
			t.Errorf("%s notified %d times, want %d", name, calls, want)
		}
	}

	var firstID string
	expect("AppendGoal", 1, func() error {
		goals, err := store.AppendGoal(goal.UpcomingGoalInput{Objective: "first"})
		if err == nil {
			firstID = goals[0].ID
		}
		return err
	})
	expect("PrependGoal", 1, func() error {
		_, err := store.PrependGoal(goal.UpcomingGoalInput{Objective: "front"})
		return err
	})
	expect("Update", 1, func() error {
		_, err := store.Update(firstID, "renamed")
		return err
	})
	expect("Move", 1, func() error {
		_, err := store.Move(firstID, "up")
		return err
	})
	expect("ReorderByMapping", 1, func() error {
		_, err := store.ReorderByMapping("1B,2A")
		return err
	})
	expect("Remove", 1, func() error {
		_, _, err := store.Remove(firstID)
		return err
	})
	expect("Restore", 1, func() error {
		_, err := store.Restore(goal.UpcomingGoal{ID: "restored", Objective: "restored"})
		return err
	})
	expect("Clear", 1, func() error {
		_, err := store.Clear()
		return err
	})
}

// TestGoalQueueStore_NoNotificationOnFailure: a rejected mutation changed
// nothing on disk, so it must not wake the UI.
func TestGoalQueueStore_NoNotificationOnFailure(t *testing.T) {
	store := NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))
	calls := 0
	store.SetOnChange(func() { calls++ })

	if _, err := store.AppendGoal(goal.UpcomingGoalInput{Objective: "keep"}); err != nil {
		t.Fatal(err)
	}
	calls = 0

	if _, err := store.Update("missing-id", "x"); err == nil {
		t.Fatal("expected Update on a missing id to fail")
	}
	if _, _, err := store.Remove("missing-id"); err == nil {
		t.Fatal("expected Remove on a missing id to fail")
	}
	if _, err := store.Move("missing-id", "up"); err == nil {
		t.Fatal("expected Move on a missing id to fail")
	}
	tooBig := strings.Repeat("x", goal.MaxObjectiveLength+1)
	if _, err := store.AppendGoal(goal.UpcomingGoalInput{Objective: tooBig}); err == nil {
		t.Fatal("expected an oversized objective to be rejected")
	}
	if calls != 0 {
		t.Errorf("failed mutations notified %d times, want 0", calls)
	}
}

// TestGoalQueueStore_HookRunsAfterUnlock: the hook fires with the store lock
// RELEASED, so a listener may read the queue back. Notifying under the lock
// would invert the lock order against GoalMode.CreateGoal (mode lock held →
// queue read for friendly-name uniqueness) and deadlock the app.
func TestGoalQueueStore_HookRunsAfterUnlock(t *testing.T) {
	store := NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))
	var readErr error
	store.SetOnChange(func() {
		done := make(chan error, 1)
		go func() {
			_, err := store.Read()
			done <- err
		}()
		select {
		case readErr = <-done:
		case <-time.After(5 * time.Second):
			t.Error("queue read blocked inside the change hook: the hook runs with the store lock held")
		}
	})

	if _, err := store.Append("first"); err != nil {
		t.Fatal(err)
	}
	if readErr != nil {
		t.Fatalf("reading the queue inside the change hook: %v", readErr)
	}
}
