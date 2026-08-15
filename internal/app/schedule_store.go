// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/pijalu/goa/tools"
)

// scheduleStore persists schedule jobs to a JSON file so created schedules
// survive restarts. It implements tools.ScheduleStore. All methods are safe
// for concurrent use; mutations are written through atomically (temp file +
// rename) so a crash cannot corrupt the store.
type scheduleStore struct {
	mu   sync.Mutex
	path string
	// seen tracks every id ever allocated so deleted ids are never reused
	// (dsh parity: allocateScheduleId scans the full id history).
	seen []string
	jobs []tools.ScheduleJob // active jobs in creation order
}

// scheduleFileLayout is the on-disk JSON shape.
type scheduleFileLayout struct {
	Seen []string            `json:"seen"`
	Jobs []tools.ScheduleJob `json:"jobs"`
}

// newScheduleStore opens (or initializes) the schedule store at path. An empty
// path yields an in-memory store with persistence disabled (used by tests and
// degenerate setups).
func newScheduleStore(path string) *scheduleStore {
	s := &scheduleStore{path: path}
	if path != "" {
		s.load()
	}
	return s
}

// load reads the store from disk. Corrupt files are treated as empty (the
// scheduler must never brick the agent over a broken reminder file).
func (s *scheduleStore) load() {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var layout scheduleFileLayout
	if err := json.Unmarshal(data, &layout); err != nil {
		return
	}
	s.seen = layout.Seen
	s.jobs = layout.Jobs
}

// save writes the store to disk atomically. No-op when persistence is disabled.
func (s *scheduleStore) save() error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}
	layout := scheduleFileLayout{Seen: s.seen, Jobs: s.jobs}
	data, err := json.MarshalIndent(layout, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Create persists a new job, assigning a fresh schedule-N id.
func (s *scheduleStore) Create(job tools.ScheduleJob) (tools.ScheduleJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	job.ID = s.allocateIDLocked()
	job.CreatedAt = time.Now()
	s.seen = append(s.seen, job.ID)
	s.jobs = append(s.jobs, job)
	if err := s.save(); err != nil {
		return tools.ScheduleJob{}, err
	}
	return job, nil
}

// allocateIDLocked returns the smallest schedule-N id not present in seen.
func (s *scheduleStore) allocateIDLocked() string {
	used := make(map[string]bool, len(s.seen))
	for _, id := range s.seen {
		used[id] = true
	}
	for n := 1; ; n++ {
		candidate := fmt.Sprintf("schedule-%d", n)
		if !used[candidate] {
			return candidate
		}
	}
}

// Delete removes the job with the given id. It reports whether the job existed.
func (s *scheduleStore) Delete(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, job := range s.jobs {
		if job.ID == id {
			s.jobs = append(s.jobs[:i], s.jobs[i+1:]...)
			if err := s.save(); err != nil {
				// Restore on failure so the in-memory view stays consistent
				// with disk.
				s.jobs = append(s.jobs[:i], append([]tools.ScheduleJob{job}, s.jobs[i:]...)...)
				return false, err
			}
			return true, nil
		}
	}
	return false, nil
}

// List returns active jobs in creation order.
func (s *scheduleStore) List() ([]tools.ScheduleJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]tools.ScheduleJob, len(s.jobs))
	copy(out, s.jobs)
	return out, nil
}

// TakeDue atomically claims every due job: one-shots are removed, recurring
// jobs advance to their next occurrence (creation-aligned, skipping missed
// occurrences — dsh resolveEveryOccurrence parity). Returned jobs carry the
// delivered occurrence view. A subsequent TakeDue returns nothing for the same
// jobs, which is what makes "due job injects once" hold across restart.
func (s *scheduleStore) TakeDue(now time.Time) ([]tools.ScheduleJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Snapshot for rollback so a failed write cannot lose or reorder jobs.
	snapshot := append([]tools.ScheduleJob(nil), s.jobs...)

	var due []tools.ScheduleJob
	var remaining []tools.ScheduleJob
	for _, job := range s.jobs {
		if job.ScheduledAt.After(now) {
			remaining = append(remaining, job)
			continue
		}
		if job.IsOneShot() {
			due = append(due, job)
			continue
		}
		// Recurring: deliver the latest occurrence at or before now, then
		// advance to the next occurrence (strictly after now). A non-positive
		// interval (corrupt store file) is treated as a one-shot to avoid an
		// infinite loop.
		interval := time.Duration(job.EverySeconds) * time.Second
		if interval <= 0 {
			due = append(due, job)
			continue
		}
		occurrence := job.ScheduledAt
		for !occurrence.Add(interval).After(now) {
			occurrence = occurrence.Add(interval)
		}
		delivered := job
		delivered.ScheduledAt = occurrence
		due = append(due, delivered)
		job.ScheduledAt = occurrence.Add(interval)
		remaining = append(remaining, job)
	}
	s.jobs = remaining
	if len(due) > 0 {
		if err := s.save(); err != nil {
			s.jobs = snapshot
			return nil, err
		}
	}
	return due, nil
}

// scheduleStorePath returns where the schedule jobs file lives for a project.
func scheduleStorePath(projectDir string) string {
	return filepath.Join(projectDir, ".goa", "schedule", "jobs.json")
}

// Compile-time assertion that the store satisfies the tools interface.
var _ tools.ScheduleStore = (*scheduleStore)(nil)
