// SPDX-License-Identifier: GPL-3.0-or-later

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
	EventRunStarted      EventType = "run_started"
	EventAgentStarted    EventType = "agent_started"
	EventAgentMessage    EventType = "agent_message"
	EventAgentThinking   EventType = "agent_thinking"
	EventAgentToolCall   EventType = "agent_tool_call"
	EventAgentToolResult EventType = "agent_tool_result"
	EventAgentSteered    EventType = "agent_steered"
	EventAgentStats      EventType = "agent_stats"
	EventAgentFinished   EventType = "agent_finished"
	EventRunFinished     EventType = "run_finished"
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
//
// Implementations MAY additionally implement Flush() and Close() (checked via
// type assertion by the runtime's async durable sink) so buffered writes can
// be flushed to disk on a timer and at shutdown without forcing every caller
// to depend on those methods.
type EventStore interface {
	Append(evt Event) error
	Replay() ([]Event, error)
	Path() string
}

// FileEventStore is a file-backed EventStore using newline-delimited JSON
// under .goa/orchestrator/<run-id>/events.jsonl.
//
// The file is opened lazily on the first Append and KEPT OPEN, so Append is a
// single write syscall (no open/close per event). Writes are write-through to
// the OS page cache, so a concurrent reader (e.g. the run snapshot opening the
// same path) sees appended events immediately, matching the previous
// open/write/close semantics at a fraction of the cost.
//
// This matters because Append is invoked (via the async durable sink) for
// every streamed token turned into an event — the previous open/write/close
// per call blocked the agent's stream reader (the "LM Studio freeze"). The
// sink (not the store) is what takes that write off the streaming goroutine.
type FileEventStore struct {
	path string
	dir  string

	mu sync.Mutex
	seq int64
	f   *os.File
}

// NewFileEventStore constructs a store for the given run id rooted at rootDir
// (typically ".goa/orchestrator"). The directory/file is created lazily so
// simply constructing a store never fails on a read-only filesystem.
func NewFileEventStore(rootDir, runID string) *FileEventStore {
	dir := filepath.Join(rootDir, runID)
	return &FileEventStore{
		path: filepath.Join(dir, "events.jsonl"),
		dir:  dir,
	}
}

// Path returns the absolute event-log path.
func (s *FileEventStore) Path() string { return s.path }

// ensureOpenLocked opens the output file for append. It is idempotent;
// caller must hold s.mu.
func (s *FileEventStore) ensureOpenLocked() error {
	if s.f != nil {
		return nil
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("orchestrator: create event dir: %w", err)
	}
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	s.f = f
	return nil
}

// Append writes a single event as a JSON line. Seq and Timestamp are stamped
// if unset; Seq is always assigned monotonically by the store so callers may
// leave it zero. The write is write-through (page cache), so it is visible to
// concurrent readers immediately.
func (s *FileEventStore) Append(evt Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureOpenLocked(); err != nil {
		return err
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
	if _, err := s.f.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

// Flush is a no-op: writes are already write-through to the page cache. It
// exists so the durable sink can treat all stores uniformly via its flusher
// type assertion. True fsync is intentionally not performed, matching the
// durability of the previous open/write/close implementation.
func (s *FileEventStore) Flush() {}

// Close closes the open file. After Close the store can no longer be appended
// to (subsequent Append reopens the file). Safe to call multiple times.
func (s *FileEventStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f == nil {
		return nil
	}
	err := s.f.Close()
	s.f = nil
	return err
}

// Replay reads all events in seq order. A missing file yields nil (fresh run).
// Corrupt lines are skipped rather than failing the whole replay, mirroring
// core/goal/store.go. Any buffered-but-unflushed events are flushed first so
// the snapshot includes in-flight writes.
func (s *FileEventStore) Replay() ([]Event, error) {
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
		events = append(events, evt)
	}
	if err := scanner.Err(); err != nil {
		return events, err
	}

	s.mu.Lock()
	for _, evt := range events {
		if evt.Seq > s.seq {
			s.seq = evt.Seq
		}
	}
	s.mu.Unlock()
	return events, nil
}
