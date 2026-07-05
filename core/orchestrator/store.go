// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// EventType enumerates the state transitions appended to a run's event log.
type EventType string

const (
	EventRunStarted     EventType = "run_started"
	EventAgentStarted   EventType = "agent_started"
	EventAgentMessage   EventType = "agent_message"
	EventAgentSteered   EventType = "agent_steered"
	EventAgentStats     EventType = "agent_stats"
	EventAgentFinished  EventType = "agent_finished"
	EventRunFinished    EventType = "run_finished"
)

// Event is a single append-only record in a run's event log. The redundant
// Seq field preserves ordering even if a replaying reader sees partial lines.
type Event struct {
	Seq       int64          `json:"seq"`
	Type      EventType      `json:"type"`
	RunID     string         `json:"run_id"`
	Timestamp time.Time      `json:"ts"`
	AgentID   string         `json:"agent_id,omitempty"`
	Role      string         `json:"role,omitempty"`
	Model     string         `json:"model,omitempty"`
	Payload   map[string]any `json:"payload,omitempty"`
}

// EventStore persists and replays orchestrator events. The interface is
// modeled on core/goal/store.go so the same NDJSON-on-disk strategy backs
// both subsystems.
type EventStore interface {
	Append(evt Event) error
	Replay() ([]Event, error)
	Path() string
}

// FileEventStore is a file-backed EventStore using newline-delimited JSON
// under .goa/orchestrator/<run-id>/events.jsonl. The directory is created on
// first Append.
type FileEventStore struct {
	path string
	dir  string

	mu  sync.Mutex
	seq int64
}

// NewFileEventStore constructs a store for the given run id rooted at rootDir
// (typically ".goa/orchestrator"). The directory is created lazily so simply
// constructing a store never fails on a read-only filesystem.
func NewFileEventStore(rootDir, runID string) *FileEventStore {
	dir := filepath.Join(rootDir, runID)
	return &FileEventStore{
		path: filepath.Join(dir, "events.jsonl"),
		dir:  dir,
	}
}

// Path returns the absolute event-log path.
func (s *FileEventStore) Path() string { return s.path }

// ensureDirLocked creates the store directory; caller must hold s.mu.
func (s *FileEventStore) ensureDirLocked() error {
	return os.MkdirAll(s.dir, 0o755)
}

// Append writes a single event as a JSON line. Seq and Timestamp are stamped
// if unset; Seq is always assigned monotonically by the store so callers may
// leave it zero.
func (s *FileEventStore) Append(evt Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureDirLocked(); err != nil {
		return fmt.Errorf("orchestrator: create event dir: %w", err)
	}
	s.seq++
	evt.Seq = s.seq
	if evt.Timestamp.IsZero() {
		evt.Timestamp = time.Now()
	}

	data, err := json.Marshal(evt)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

// Replay reads all events in seq order. A missing file yields nil (fresh run).
// Corrupt lines are skipped rather than failing the whole replay, mirroring
// core/goal/store.go.
func (s *FileEventStore) Replay() ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := os.Open(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var events []Event
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var evt Event
		if err := json.Unmarshal(line, &evt); err != nil {
			continue
		}
		if evt.Seq > s.seq {
			s.seq = evt.Seq
		}
		events = append(events, evt)
	}
	return events, scanner.Err()
}
