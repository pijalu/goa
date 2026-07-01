// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tasks

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Store persists tasks.
type Store interface {
	Save(task Task) error
	Load() ([]Task, error)
}

// JSONLStore persists tasks as newline-delimited JSON.
type JSONLStore struct {
	path string
	mu   sync.Mutex
}

// NewJSONLStore creates a JSONL store at the given path.
func NewJSONLStore(path string) *JSONLStore {
	return &JSONLStore{path: path}
}

// Save appends a task record to the JSONL file.
func (s *JSONLStore) Save(task Task) error {
	if s.path == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("tasks store: mkdir: %w", err)
	}
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("tasks store: open: %w", err)
	}
	defer f.Close()
	data, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("tasks store: marshal: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("tasks store: write: %w", err)
	}
	if _, err := f.WriteString("\n"); err != nil {
		return fmt.Errorf("tasks store: newline: %w", err)
	}
	return nil
}

// Load reads all persisted tasks. The on-disk file is an append-only event
// log (one record per state transition), so multiple records may share an ID.
// To present callers a consistent view, records are de-duplicated by ID keeping
// the last-written state for each (last-write-wins). First-seen ordering is
// preserved so the relative ordering of distinct tasks is stable.
func (s *JSONLStore) Load() ([]Task, error) {
	if s.path == "" {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.Open(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("tasks store: open: %w", err)
	}
	defer f.Close()

	order := make([]string, 0)
	byID := make(map[string]Task)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var task Task
		if err := json.Unmarshal(line, &task); err != nil {
			continue
		}
		if _, seen := byID[task.ID]; !seen {
			order = append(order, task.ID)
		}
		byID[task.ID] = task // last write wins
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("tasks store: scan: %w", err)
	}
	tasks := make([]Task, 0, len(order))
	for _, id := range order {
		tasks = append(tasks, byID[id])
	}
	return tasks, nil
}

// NopStore is a store that does nothing.
type NopStore struct{}

// Save implements Store.
func (NopStore) Save(task Task) error { return nil }

// Load implements Store.
func (NopStore) Load() ([]Task, error) { return nil, nil }
