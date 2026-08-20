// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pijalu/goa/tools"
)

// sampleJob builds a one-shot after job for store tests.
func sampleJob(prompt string, after time.Duration) tools.ScheduleJob {
	return tools.ScheduleJob{
		Kind:         tools.ScheduleKindAfter,
		Prompt:       prompt,
		AfterSeconds: int(after.Seconds()),
	}
}

func TestScheduleStore_CreatedScheduleSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.json")

	store := newScheduleStore(path)
	created, err := store.Create(sampleJob("survive me", 3600*time.Second))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID == "" {
		t.Fatal("expected assigned id")
	}

	// Simulate restart: a brand-new store over the same path must see the job.
	reopened := newScheduleStore(path)
	jobs, err := reopened.List()
	if err != nil {
		t.Fatalf("list after restart: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job after restart, got %d", len(jobs))
	}
	if jobs[0].ID != created.ID || jobs[0].Prompt != "survive me" {
		t.Fatalf("job did not survive restart: %+v", jobs[0])
	}
}

func TestScheduleStore_DeleteAndIDReuse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.json")

	store := newScheduleStore(path)
	first, err := store.Create(sampleJob("first", time.Hour))
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	if first.ID != "schedule-1" {
		t.Fatalf("expected schedule-1, got %s", first.ID)
	}
	deleted, err := store.Delete(first.ID)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !deleted {
		t.Fatal("expected deleted true")
	}
	// Unknown id → false.
	deleted, err = store.Delete(first.ID)
	if err != nil {
		t.Fatalf("delete unknown: %v", err)
	}
	if deleted {
		t.Fatal("expected deleted false for unknown id")
	}
	// Deleted ids are never reused (dsh parity): the next create gets
	// schedule-2, not schedule-1.
	second, err := store.Create(sampleJob("second", time.Hour))
	if err != nil {
		t.Fatalf("create second: %v", err)
	}
	if second.ID != "schedule-2" {
		t.Fatalf("expected schedule-2 (no id reuse), got %s", second.ID)
	}
}

func TestScheduleStore_DeleteListRoundTrip(t *testing.T) {
	store := newScheduleStore("") // in-memory
	for i := 0; i < 3; i++ {
		if _, err := store.Create(sampleJob("job", time.Hour)); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	jobs, _ := store.List()
	if len(jobs) != 3 {
		t.Fatalf("expected 3 jobs, got %d", len(jobs))
	}
	if _, err := store.Delete("schedule-2"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	jobs, _ = store.List()
	if len(jobs) != 2 {
		t.Fatalf("expected 2 jobs after delete, got %d", len(jobs))
	}
	// Creation order preserved.
	if jobs[0].ID != "schedule-1" || jobs[1].ID != "schedule-3" {
		t.Fatalf("unexpected order: %v, %v", jobs[0].ID, jobs[1].ID)
	}
}

func TestScheduleStore_TakeDue_InjectsOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.json")

	store := newScheduleStore(path)
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	// A one-shot already past its target and one still in the future.
	past, err := store.Create(tools.ScheduleJob{
		Kind:        tools.ScheduleKindAt,
		Prompt:      "past reminder",
		ScheduledAt: now.Add(-time.Hour),
	})
	if err != nil {
		t.Fatalf("create past: %v", err)
	}
	if _, err := store.Create(tools.ScheduleJob{
		Kind:        tools.ScheduleKindAt,
		Prompt:      "future reminder",
		ScheduledAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("create future: %v", err)
	}

	due, err := store.TakeDue(now)
	if err != nil {
		t.Fatalf("take due: %v", err)
	}
	if len(due) != 1 {
		t.Fatalf("expected exactly 1 due job, got %d", len(due))
	}
	if due[0].ID != past.ID || due[0].Prompt != "past reminder" {
		t.Fatalf("unexpected due job: %+v", due[0])
	}

	// Second claim returns nothing: due job injects once.
	again, err := store.TakeDue(now)
	if err != nil {
		t.Fatalf("second take due: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("expected no re-delivery, got %d", len(again))
	}

	// The one-shot is gone; the future job remains.
	jobs, _ := store.List()
	if len(jobs) != 1 || jobs[0].Prompt != "future reminder" {
		t.Fatalf("unexpected remaining jobs: %+v", jobs)
	}
}

func TestScheduleStore_TakeDue_SurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.json")

	store := newScheduleStore(path)
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if _, err := store.Create(tools.ScheduleJob{
		Kind:        tools.ScheduleKindAt,
		Prompt:      "due while down",
		ScheduledAt: now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Restart before any delivery: the due job is still there.
	reopened := newScheduleStore(path)
	due, err := reopened.TakeDue(now)
	if err != nil {
		t.Fatalf("take due after restart: %v", err)
	}
	if len(due) != 1 || due[0].Prompt != "due while down" {
		t.Fatalf("expected the due job after restart, got %+v", due)
	}

	// And after claiming, a second restart sees it gone.
	reopened2 := newScheduleStore(path)
	jobs, _ := reopened2.List()
	if len(jobs) != 0 {
		t.Fatalf("expected no jobs after claim+restart, got %+v", jobs)
	}
}

func TestScheduleStore_TakeDue_RecurringAdvances(t *testing.T) {
	store := newScheduleStore("")
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	every := 600 * time.Second

	if _, err := store.Create(tools.ScheduleJob{
		Kind:         tools.ScheduleKindEvery,
		Prompt:       "recurring",
		EverySeconds: 600,
		ScheduledAt:  now.Add(-65 * time.Minute), // 6.5 intervals ago
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	due, err := store.TakeDue(now)
	if err != nil {
		t.Fatalf("take due: %v", err)
	}
	if len(due) != 1 {
		t.Fatalf("expected 1 due recurring, got %d", len(due))
	}
	// The delivered occurrence is the LATEST one at/before now — not the
	// original target: missed occurrences are skipped, one latest per overdue
	// rule (dsh resolveEveryOccurrence parity).
	wantOccurrence := now.Add(-5 * time.Minute)
	if !due[0].ScheduledAt.Equal(wantOccurrence) {
		t.Fatalf("expected occurrence %v, got %v", wantOccurrence, due[0].ScheduledAt)
	}

	// The stored job advanced to the next occurrence (strictly after now).
	jobs, _ := store.List()
	if len(jobs) != 1 {
		t.Fatalf("expected recurring job to remain, got %d", len(jobs))
	}
	wantNext := wantOccurrence.Add(every)
	if !jobs[0].ScheduledAt.Equal(wantNext) {
		t.Fatalf("expected next occurrence %v, got %v", wantNext, jobs[0].ScheduledAt)
	}

	// Nothing due again until the next occurrence passes.
	again, _ := store.TakeDue(now)
	if len(again) != 0 {
		t.Fatalf("expected no re-delivery before next occurrence, got %d", len(again))
	}
	due, _ = store.TakeDue(wantNext)
	if len(due) != 1 {
		t.Fatalf("expected the next occurrence due, got %d", len(due))
	}
}

func TestScheduleStore_CorruptFileStartsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.json")
	if err := os.WriteFile(path, []byte("{ not json"), 0644); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}
	store := newScheduleStore(path)
	jobs, err := store.List()
	if err != nil {
		t.Fatalf("list on corrupt store: %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("expected empty store on corrupt file, got %d", len(jobs))
	}
}
