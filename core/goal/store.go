// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"bufio"
	"encoding/json"
	"os"
	"sync"
)

// EventStore persists and replays goal event records.
type EventStore interface {
	Append(record GoalEventRecord) error
	Replay() ([]GoalEventRecord, error)
}

// Telemetry records goal lifecycle events.
type Telemetry interface {
	Track(event string, props map[string]any)
}

// ReminderFunc injects system reminder text into the conversation.
type ReminderFunc func(string)

// FileEventStore is a file-backed EventStore using newline-delimited JSON.
type FileEventStore struct {
	path string
	mu   sync.Mutex
}

// NewFileEventStore creates a new file-backed event store.
func NewFileEventStore(path string) *FileEventStore {
	return &FileEventStore{path: path}
}

// Append writes a single record as a JSON line.
func (s *FileEventStore) Append(record GoalEventRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.Marshal(record)
	if err != nil {
		return err
	}

	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.Write(append(data, '\n'))
	return err
}

// Replay reads all records from the store.
func (s *FileEventStore) Replay() ([]GoalEventRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := os.Open(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var records []GoalEventRecord
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var record GoalEventRecord
		if err := json.Unmarshal(line, &record); err != nil {
			// Skip corrupt lines rather than failing replay.
			continue
		}
		records = append(records, record)
	}
	return records, scanner.Err()
}
