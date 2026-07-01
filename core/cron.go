// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package core provides the foundational types for Goa — agent management,
// session tracking, loop detection, and the scheduled task (cron) system
// for running agent tasks at specified intervals.
package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// CronJob represents a single scheduled task.
type CronJob struct {
	ID        string    `json:"id"`
	Schedule  string    `json:"schedule"` // cron expression: "0 9 * * 1" or "@daily"
	Task      string    `json:"task"`     // prompt text to execute
	Enabled   bool      `json:"enabled"`
	LastRun   time.Time `json:"last_run,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// CronManager manages scheduled agent tasks.
// Jobs are persisted as JSON files in .goa/cron/.
type CronManager struct {
	mu     sync.Mutex
	jobs   map[string]*CronJob
	dir    string
	ticker *time.Ticker
	stopCh chan struct{}
	onFire func(task string) // injected callback to execute a task

	// nextRun computes the next time a schedule should fire.
	// Default: nextCronTime. Override for testing.
	nextRun func(schedule string, since time.Time) (time.Time, error)
}

// NewCronManager creates a cron manager that persists jobs to dir/cron/.
// The onFire callback is called when a job's schedule triggers.
func NewCronManager(dir string, onFire func(task string)) *CronManager {
	return &CronManager{
		jobs:   make(map[string]*CronJob),
		dir:    filepath.Join(dir, "cron"),
		stopCh: make(chan struct{}),
		onFire: onFire,
		nextRun: func(schedule string, since time.Time) (time.Time, error) {
			return nextCronTime(schedule, since)
		},
	}
}

// AddJob adds a job. Returns error if ID already exists.
func (m *CronManager) AddJob(job CronJob) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.jobs[job.ID]; exists {
		return fmt.Errorf("cron job %q already exists", job.ID)
	}
	if job.Task == "" {
		return fmt.Errorf("cron job %q has empty task", job.ID)
	}
	// Validate schedule by testing parsing.
	if _, err := m.nextRun(job.Schedule, time.Now()); err != nil {
		return fmt.Errorf("cron job %q invalid schedule %q: %w", job.ID, job.Schedule, err)
	}

	job.CreatedAt = time.Now()
	job.Enabled = true
	m.jobs[job.ID] = &job
	return nil
}

// RemoveJob removes a job by ID. No-op if not found.
func (m *CronManager) RemoveJob(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.jobs, id)
}

// GetJob returns a job by ID, or nil.
func (m *CronManager) GetJob(id string) *CronJob {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.jobs[id]
}

// ListJobs returns all jobs sorted by creation time.
func (m *CronManager) ListJobs() []CronJob {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := make([]CronJob, 0, len(m.jobs))
	for _, j := range m.jobs {
		result = append(result, *j)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})
	return result
}

// Start begins the ticker loop that checks for due jobs every 30s.
func (m *CronManager) Start() {
	m.mu.Lock()
	if m.ticker != nil {
		m.mu.Unlock()
		return
	}
	m.ticker = time.NewTicker(30 * time.Second)
	m.mu.Unlock()

	go func() {
		for {
			select {
			case <-m.ticker.C:
				m.checkAndFire()
			case <-m.stopCh:
				return
			}
		}
	}()
}

// Stop stops the ticker loop.
func (m *CronManager) Stop() {
	m.mu.Lock()
	if m.ticker != nil {
		m.ticker.Stop()
		m.ticker = nil
	}
	m.mu.Unlock()
}

// checkAndFire checks all enabled jobs and fires any that are due.
func (m *CronManager) checkAndFire() {
	m.mu.Lock()
	// Snapshot by value to avoid data races
	type jobEntry struct {
		id  string
		job CronJob
	}
	entries := make([]jobEntry, 0, len(m.jobs))
	for id, j := range m.jobs {
		entries = append(entries, jobEntry{id: id, job: *j})
	}
	now := time.Now()
	m.mu.Unlock()

	for _, entry := range entries {
		job := entry.job
		if !job.Enabled {
			continue
		}
		next, err := m.nextRun(job.Schedule, job.LastRun)
		if err != nil {
			continue
		}
		if !now.After(next) && !now.Equal(next) {
			continue
		}

		// Fire the job.
		if m.onFire != nil {
			m.onFire(job.Task)
		}

		// Update last run time under lock.
		m.mu.Lock()
		if live := m.jobs[entry.id]; live != nil {
			live.LastRun = now
		}
		m.mu.Unlock()
	}
}

// Save persists all jobs to the cron directory.
func (m *CronManager) Save() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := os.MkdirAll(m.dir, 0755); err != nil {
		return err
	}

	for id, job := range m.jobs {
		data, err := json.MarshalIndent(job, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal job %q: %w", id, err)
		}
		path := filepath.Join(m.dir, id+".json")
		if err := os.WriteFile(path, data, 0644); err != nil {
			return fmt.Errorf("write job %q: %w", id, err)
		}
	}
	return nil
}

// Load reads all persisted jobs from the cron directory.
func (m *CronManager) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	entries, err := os.ReadDir(m.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(m.dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		var job CronJob
		if err := json.Unmarshal(data, &job); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		m.jobs[job.ID] = &job
	}
	return nil
}

// ── cron expression parsing ──

// cronField holds one field of a 5-field cron expression.
type cronField struct {
	values []int // expanded values (e.g., 0-59 for minute)
}

// parseCronField expands a single cron field into a set of valid values.
func parseCronField(field string, min, max int) (cronField, error) {
	// Handle special characters: comma, step, range, wildcard
	parts := strings.Split(field, ",")
	seen := make(map[int]bool)
	for _, part := range parts {
		if err := parseCronPart(part, min, max, seen); err != nil {
			return cronField{}, err
		}
	}
	vals := make([]int, 0, len(seen))
	for v := range seen {
		vals = append(vals, v)
	}
	sort.Ints(vals)
	return cronField{values: vals}, nil
}

func parseCronPart(part string, min, max int, seen map[int]bool) error {
	switch {
	case strings.Contains(part, "/"):
		return parseCronStepPart(part, min, max, seen)
	case strings.Contains(part, "-"):
		return parseCronRangePart(part, min, max, seen)
	case part == "*":
		addRange(min, max, 1, min, max, seen)
		return nil
	default:
		return parseCronSinglePart(part, min, max, seen)
	}
}

func parseCronStepPart(part string, min, max int, seen map[int]bool) error {
	sub := strings.SplitN(part, "/", 2)
	step := 1
	if _, err := fmt.Sscanf(sub[1], "%d", &step); err != nil || step < 1 {
		return fmt.Errorf("invalid step: %q", sub[1])
	}
	lo, hi := min, max
	base := sub[0]
	if base != "*" {
		if strings.Contains(base, "-") {
			fmt.Sscanf(base, "%d-%d", &lo, &hi)
		} else {
			fmt.Sscanf(base, "%d", &lo)
			hi = max
		}
	}
	addRange(lo, hi, step, min, max, seen)
	return nil
}

func parseCronRangePart(part string, min, max int, seen map[int]bool) error {
	var lo, hi int
	if _, err := fmt.Sscanf(part, "%d-%d", &lo, &hi); err != nil {
		return fmt.Errorf("invalid range: %q", part)
	}
	addRange(lo, hi, 1, min, max, seen)
	return nil
}

func parseCronSinglePart(part string, min, max int, seen map[int]bool) error {
	var v int
	if _, err := fmt.Sscanf(part, "%d", &v); err != nil {
		return fmt.Errorf("invalid value: %q", part)
	}
	if v >= min && v <= max {
		seen[v] = true
	}
	return nil
}

func addRange(lo, hi, step, min, max int, seen map[int]bool) {
	for v := lo; v <= hi; v += step {
		if v >= min && v <= max {
			seen[v] = true
		}
	}
}

// nextCronTime computes the next time after `since` that the cron expression matches.
func nextCronTime(expr string, since time.Time) (time.Time, error) {
	if t, ok := tryShortcut(expr, since); ok {
		return t, nil
	}

	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return time.Time{}, fmt.Errorf("cron expression must have 5 fields, got %d: %q", len(fields), expr)
	}

	parsed, err := parseFiveFieldCron(fields)
	if err != nil {
		return time.Time{}, err
	}

	return searchNextMatch(since, parsed, expr)
}

func tryShortcut(expr string, since time.Time) (time.Time, bool) {
	if strings.HasPrefix(expr, "@every ") {
		dur, err := time.ParseDuration(strings.TrimPrefix(expr, "@every "))
		if err != nil {
			return time.Time{}, false
		}
		return since.Add(dur), true
	}
	if expr == "@daily" {
		next := time.Date(since.Year(), since.Month(), since.Day(), 0, 0, 0, 0, since.Location())
		if !since.Before(next) {
			next = next.Add(24 * time.Hour)
		}
		return next, true
	}
	if expr == "@weekly" {
		daysUntilMonday := (8 - int(since.Weekday())) % 7
		if daysUntilMonday == 0 {
			daysUntilMonday = 7
		}
		next := time.Date(since.Year(), since.Month(), since.Day()+daysUntilMonday, 0, 0, 0, 0, since.Location())
		return next, true
	}
	return time.Time{}, false
}

func parseFiveFieldCron(fields []string) ([]cronField, error) {
	parsed := make([]cronField, 5)
	ranges := [][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 6}}
	for i, f := range fields {
		var err error
		parsed[i], err = parseCronField(f, ranges[i][0], ranges[i][1])
		if err != nil {
			return nil, fmt.Errorf("field %d: %w", i+1, err)
		}
	}
	return parsed, nil
}

func searchNextMatch(since time.Time, parsed []cronField, expr string) (time.Time, error) {
	t := since.Truncate(time.Minute).Add(time.Minute)
	for i := 0; i < 525600; i++ {
		if matchCron(t, parsed) {
			return t, nil
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}, fmt.Errorf("no matching time found within 1 year for %q", expr)
}

func matchCron(t time.Time, fields []cronField) bool {
	if !intInSlice(t.Minute(), fields[0].values) {
		return false
	}
	if !intInSlice(t.Hour(), fields[1].values) {
		return false
	}
	if !intInSlice(t.Day(), fields[2].values) {
		return false
	}
	if !intInSlice(int(t.Month()), fields[3].values) {
		return false
	}
	if !intInSlice(int(t.Weekday()), fields[4].values) {
		return false
	}
	return true
}

func intInSlice(v int, slice []int) bool {
	for _, s := range slice {
		if s == v {
			return true
		}
	}
	return false
}
