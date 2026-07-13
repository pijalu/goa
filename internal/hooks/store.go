// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package hooks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Entry records a single hook execution.
type Entry struct {
	Event      Event     `json:"event"`
	Command    string    `json:"command"`
	Args       []string  `json:"args,omitempty"`
	Payload    string    `json:"payload"`
	Output     string    `json:"output,omitempty"`
	ExitCode   int       `json:"exit_code"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
}

// Store records hook audit entries to a persistent log.
type Store struct {
	mu     sync.Mutex
	path   string
	memory []Entry
}

// NewStore creates an audit store. If path is empty, entries are kept in memory.
func NewStore(path string) *Store {
	return &Store{path: path}
}

// Record appends an entry to the audit log.
func (s *Store) Record(e Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.memory = append(s.memory, e)
	if s.path == "" {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return fmt.Errorf("hooks store: create directory: %w", err)
	}
	data, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("hooks store: marshal entry: %w", err)
	}
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("hooks store: open file: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("hooks store: write entry: %w", err)
	}
	if _, err := f.WriteString("\n"); err != nil {
		return fmt.Errorf("hooks store: write newline: %w", err)
	}
	return nil
}

// Entries returns a copy of the in-memory entries recorded so far.
func (s *Store) Entries() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Entry, len(s.memory))
	copy(out, s.memory)
	return out
}

// Path returns the configured log path, or empty for in-memory mode.
func (s *Store) Path() string { return s.path }
