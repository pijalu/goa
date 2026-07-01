// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCronManager_AddJob(t *testing.T) {
	cm := NewCronManager(t.TempDir(), nil)

	err := cm.AddJob(CronJob{ID: "test", Schedule: "0 9 * * 1", Task: "Review code"})
	if err != nil {
		t.Fatal(err)
	}

	job := cm.GetJob("test")
	if job == nil {
		t.Fatal("job not found")
	}
	if job.Task != "Review code" {
		t.Errorf("Task = %q, want %q", job.Task, "Review code")
	}
	if !job.Enabled {
		t.Error("new job should be enabled")
	}
}

func TestCronManager_DuplicateID_Error(t *testing.T) {
	cm := NewCronManager(t.TempDir(), nil)
	cm.AddJob(CronJob{ID: "test", Schedule: "0 9 * * 1", Task: "task"})

	err := cm.AddJob(CronJob{ID: "test", Schedule: "0 9 * * 1", Task: "other"})
	if err == nil {
		t.Error("expected error for duplicate ID")
	}
}

func TestCronManager_EmptyTask_Error(t *testing.T) {
	cm := NewCronManager(t.TempDir(), nil)
	err := cm.AddJob(CronJob{ID: "test", Schedule: "0 9 * * 1"})
	if err == nil {
		t.Error("expected error for empty task")
	}
}

func TestCronManager_InvalidSchedule_Error(t *testing.T) {
	cm := NewCronManager(t.TempDir(), nil)
	err := cm.AddJob(CronJob{ID: "test", Schedule: "not-a-cron", Task: "test"})
	if err == nil {
		t.Error("expected error for invalid schedule")
	}
}

func TestCronManager_RemoveJob(t *testing.T) {
	cm := NewCronManager(t.TempDir(), nil)
	cm.AddJob(CronJob{ID: "a", Schedule: "@daily", Task: "task"})
	cm.RemoveJob("a")

	if cm.GetJob("a") != nil {
		t.Error("job should be removed")
	}
}

func TestCronManager_ListJobs_SortedByCreation(t *testing.T) {
	cm := NewCronManager(t.TempDir(), nil)
	cm.AddJob(CronJob{ID: "b", Schedule: "@daily", Task: "second"})
	cm.AddJob(CronJob{ID: "a", Schedule: "@daily", Task: "first"})

	jobs := cm.ListJobs()
	if len(jobs) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(jobs))
	}
	if jobs[0].ID != "b" {
		t.Errorf("jobs[0].ID = %q, want %q (by creation time)", jobs[0].ID, "b")
	}
}

func TestCronManager_Persistence(t *testing.T) {
	dir := t.TempDir()

	cm1 := NewCronManager(dir, nil)
	cm1.AddJob(CronJob{ID: "review", Schedule: "0 9 * * 1", Task: "Review code"})
	if err := cm1.Save(); err != nil {
		t.Fatal(err)
	}

	cm2 := NewCronManager(dir, nil)
	if err := cm2.Load(); err != nil {
		t.Fatal(err)
	}

	job := cm2.GetJob("review")
	if job == nil {
		t.Fatal("job should survive restart")
	}
	if job.Task != "Review code" {
		t.Errorf("Task = %q, want %q", job.Task, "Review code")
	}
}

func TestCronManager_JobFires(t *testing.T) {
	fired := make(chan string, 1)
	cm := NewCronManager(t.TempDir(), func(task string) {
		fired <- task
	})

	// Override nextRun to fire immediately.
	cm.nextRun = func(_ string, since time.Time) (time.Time, error) {
		return since.Add(-time.Second), nil // already past
	}

	cm.AddJob(CronJob{ID: "immediate", Schedule: "@every 1s", Task: "do something"})
	cm.checkAndFire()

	select {
	case task := <-fired:
		if task != "do something" {
			t.Errorf("fired task = %q, want %q", task, "do something")
		}
	default:
		t.Error("job did not fire")
	}
}

func TestCronManager_DisabledJob_DoesNotFire(t *testing.T) {
	fired := make(chan string, 1)
	cm := NewCronManager(t.TempDir(), func(task string) {
		fired <- task
	})
	cm.nextRun = func(_ string, since time.Time) (time.Time, error) {
		return since.Add(-time.Second), nil
	}

	cm.AddJob(CronJob{ID: "disabled", Schedule: "@daily", Task: "should not fire"})
	job := cm.GetJob("disabled")
	job.Enabled = false

	cm.checkAndFire()

	select {
	case <-fired:
		t.Error("disabled job fired")
	default:
		// Expected.
	}
}

func TestNextCronTime_Daily(t *testing.T) {
	now := time.Date(2026, 6, 22, 9, 0, 0, 0, time.UTC)
	next, err := nextCronTime("@daily", now)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 6, 23, 0, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("next = %v, want %v", next, want)
	}
}

func TestNextCronTime_Weekly(t *testing.T) {
	// Tuesday
	now := time.Date(2026, 6, 23, 10, 0, 0, 0, time.UTC)
	next, err := nextCronTime("@weekly", now)
	if err != nil {
		t.Fatal(err)
	}
	// Next Monday
	want := time.Date(2026, 6, 29, 0, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("next = %v, want %v", next, want)
	}
}

func TestNextCronTime_EveryDuration(t *testing.T) {
	now := time.Date(2026, 6, 22, 9, 0, 0, 0, time.UTC)
	next, err := nextCronTime("@every 30m", now)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 6, 22, 9, 30, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("next = %v, want %v", next, want)
	}
}

func TestNextCronTime_StandardFiveField(t *testing.T) {
	// "0 9 * * 1" = 9:00 AM every Monday
	now := time.Date(2026, 6, 22, 9, 0, 0, 0, time.UTC) // Monday 9AM
	next, err := nextCronTime("0 9 * * 1", now)
	if err != nil {
		t.Fatal(err)
	}
	// Next Monday (since it's exactly 9AM Monday, next is next week)
	want := time.Date(2026, 6, 29, 9, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("next = %v, want %v", next, want)
	}
}

func TestNextCronTime_TuesdayAtTen(t *testing.T) {
	// Monday 9AM, schedule is Tuesday 10AM
	now := time.Date(2026, 6, 22, 9, 0, 0, 0, time.UTC)
	next, err := nextCronTime("0 10 * * 2", now)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 6, 23, 10, 0, 0, 0, time.UTC) // next day
	if !next.Equal(want) {
		t.Errorf("next = %v, want %v", next, want)
	}
}

func TestNextCronTime_InvalidExpression(t *testing.T) {
	_, err := nextCronTime("invalid", time.Now())
	if err == nil {
		t.Error("expected error for invalid expression")
	}
}

func TestCronManager_Load_NonExistentDir(t *testing.T) {
	cm := NewCronManager("/nonexistent/path", nil)
	err := cm.Load()
	if err != nil {
		t.Fatal(err) // should not error on missing dir
	}
}

func TestCronManager_RemoveNonExistent(t *testing.T) {
	cm := NewCronManager(t.TempDir(), nil)
	cm.RemoveJob("nonexistent") // should not panic
}

func TestCronManager_Save_CreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "deep", "nested")
	cm := NewCronManager(dir, nil)
	cm.AddJob(CronJob{ID: "test", Schedule: "@daily", Task: "task"})
	if err := cm.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "cron", "test.json")); os.IsNotExist(err) {
		t.Fatal("job file not created")
	}
}
