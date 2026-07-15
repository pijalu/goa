// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
)

// SessionInfo holds metadata about a saved session.
type SessionInfo struct {
	Name         string    `json:"name"`
	Date         time.Time `json:"date"`
	EventCount   int       `json:"event_count"`
	TokenTotal   int       `json:"token_total"`
	FirstMessage string    `json:"first_message,omitempty"`
}

// ErrEmptySession is returned by SaveCurrent when there is no conversation to
// persist. Callers can surface a friendly message instead of creating a file.
var ErrEmptySession = errors.New("session is empty")

// SessionStore manages session persistence using JSONL format.
// Sessions are stored in .goa/sessions/<timestamp>_<name>.jsonl.
// Writes are asynchronous: events are queued to a dedicated goroutine that
// batches them to disk so the streaming hot path never blocks on I/O.
type SessionStore struct {
	mu           sync.Mutex
	dir          string
	sessionID    string
	writer       *sessionWriter
	eventCount   int
	tokenTotal   int
	writerBroken bool
	logger       *agentic.Logger
}

// sessionWriter owns a single session file and flushes events asynchronously.
type sessionWriter struct {
	file    *os.File
	ch      chan agentic.OutputEvent
	done    chan struct{}
	wg      sync.WaitGroup
	onError func(error)
}

// newSessionWriter opens path for append and starts the background writer.
func newSessionWriter(path string, onError func(error)) (*sessionWriter, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	sw := &sessionWriter{
		file:    f,
		ch:      make(chan agentic.OutputEvent, 256),
		done:    make(chan struct{}),
		onError: onError,
	}
	sw.wg.Add(1)
	go sw.run()
	return sw, nil
}

// run reads events from the channel and writes them to the session file.
// It flushes on every event so crashes lose at most the in-flight event.
func (sw *sessionWriter) run() {
	defer sw.wg.Done()
	bw := bufio.NewWriter(sw.file)

	for {
		select {
		case ev := <-sw.ch:
			if !sw.writeAndFlush(bw, ev) {
				return
			}
		case <-sw.done:
			sw.drainAndClose(bw)
			return
		}
	}
}

// writeAndFlush serializes one event and flushes it to disk.
// Returns false if an error occurred and the writer should stop.
func (sw *sessionWriter) writeAndFlush(bw *bufio.Writer, ev agentic.OutputEvent) bool {
	data, err := json.Marshal(ev)
	if err != nil {
		sw.onError(fmt.Errorf("marshal session event: %w", err))
		return false
	}
	if _, err := bw.Write(append(data, '\n')); err != nil {
		sw.onError(fmt.Errorf("write session event: %w", err))
		return false
	}
	if err := bw.Flush(); err != nil {
		sw.onError(fmt.Errorf("flush session file: %w", err))
		return false
	}
	return true
}

// drainAndClose flushes any remaining buffered events before shutdown.
func (sw *sessionWriter) drainAndClose(bw *bufio.Writer) {
	for {
		select {
		case ev := <-sw.ch:
			if !sw.writeAndFlush(bw, ev) {
				return
			}
		default:
			_ = bw.Flush()
			return
		}
	}
}

// Write enqueues an event. It blocks only while the buffer is full.
func (sw *sessionWriter) Write(ev agentic.OutputEvent) {
	sw.ch <- ev
}

// Close stops the writer, flushes buffered events, and closes the file.
func (sw *sessionWriter) Close() error {
	close(sw.done)
	sw.wg.Wait()
	return sw.file.Close()
}

// NewSessionStore creates a session store rooted at the given directory.
func NewSessionStore(dir string) *SessionStore {
	return &SessionStore{dir: dir}
}

// SetLogger configures a logger for reporting async write errors.
func (s *SessionStore) SetLogger(logger *agentic.Logger) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logger = logger
}

// SessionID returns the active session ID, or an empty string if no session is
// currently running.
func (s *SessionStore) SessionID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionID
}

// CurrentSessionPath returns the filesystem path to the active session file,
// or an empty string if no session is active.
func (s *SessionStore) CurrentSessionPath() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessionID == "" {
		return ""
	}
	return filepath.Join(s.dir, "sessions", s.sessionID+".jsonl")
}

// StartSession creates a new session file and returns the session ID.
func (s *SessionStore) StartSession() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Close existing writer before creating new one.
	if s.writer != nil {
		if err := s.writer.Close(); err != nil {
			s.logError("close previous session writer", err)
		}
	}
	s.sessionID = fmt.Sprintf("%d_%s", time.Now().Unix(), randomID(8))
	return s.openSessionWriterLocked()
}

// StartSessionWithID starts a session with the given ID, re-opening the
// existing session file for append. The ID must be a valid session name
// (filename without .jsonl). Used by session restore so the restored
// conversation keeps its original identity.
func (s *SessionStore) StartSessionWithID(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.writer != nil {
		if err := s.writer.Close(); err != nil {
			s.logError("close previous session writer", err)
		}
	}
	s.sessionID = id
	return s.openSessionWriterLocked()
}

// openSessionWriterLocked creates the session writer for the current sessionID.
// Caller must hold s.mu.
func (s *SessionStore) openSessionWriterLocked() string {
	sessionDir := filepath.Join(s.dir, "sessions")
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		s.logError("create sessions dir", err)
	}

	path := filepath.Join(sessionDir, s.sessionID+".jsonl")
	w, err := newSessionWriter(path, s.onWriteError)
	if err != nil {
		// Fall back to in-memory only; the next write will report the error.
		s.writer = nil
		s.writerBroken = true
		s.logError("create session file", err)
	} else {
		s.writer = w
		s.writerBroken = false
	}
	s.eventCount = 0
	s.tokenTotal = 0
	return s.sessionID
}

func (s *SessionStore) onWriteError(err error) {
	s.mu.Lock()
	s.writerBroken = true
	logger := s.logger
	s.mu.Unlock()

	if logger != nil {
		logger.Log(agentic.Error, "session write failed: %v", err)
	}
}

func (s *SessionStore) logError(action string, err error) {
	if s.logger != nil {
		s.logger.Log(agentic.Error, "%s: %v", action, err)
	}
}

// WriteEvent enqueues an event for asynchronous persistence.
func (s *SessionStore) WriteEvent(event agentic.OutputEvent) {
	// Streaming tool-call deltas carry the accumulated arguments so far.
	// For a single streamed call (e.g. write of a large file) the provider
	// emits one delta per chunk, each containing the full content written so
	// far. Persisting those deltas in the session file produces quadratic
	// growth (observed: a 6.4GB session file from ~9k events). The final
	// completed (non-delta) tool-call event is sufficient for replay/export.
	if event.Type == agentic.EventToolCall && event.IsDelta {
		return
	}

	s.mu.Lock()
	writer := s.writer
	broken := s.writerBroken
	s.eventCount++
	s.updateTokenTotalsLocked(event)
	s.mu.Unlock()

	if writer == nil || broken {
		return
	}
	writer.Write(event)
}

func (s *SessionStore) updateTokenTotalsLocked(event agentic.OutputEvent) {
	if event.Type == agentic.EventTokenStats && event.Timings != nil {
		s.tokenTotal += event.Timings.PromptN + event.Timings.PredictedN
	}
	if event.Type == agentic.EventContextStats && event.ContextStats != nil {
		s.tokenTotal = event.ContextStats.EstimatedTokens
	}
}

// Close flushes and closes the current session writer.
func (s *SessionStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writer == nil {
		return nil
	}
	err := s.writer.Close()
	s.writer = nil
	return err
}

// SaveCurrent creates a named save point for the current session.
func (s *SessionStore) SaveCurrent(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.writer == nil {
		return fmt.Errorf("no active session")
	}

	if s.eventCount == 0 && s.tokenTotal == 0 {
		// Empty session — close and discard the file without creating a save.
		filePath := s.writer.file.Name()
		if err := s.writer.Close(); err != nil {
			s.logError("close empty session writer", err)
		}
		os.Remove(filePath)
		s.writer = nil
		s.sessionID = ""
		return ErrEmptySession
	}

	if err := s.writer.Close(); err != nil {
		return err
	}
	s.writer = nil
	s.sessionID = ""
	return nil
}

// ListSessions returns metadata about all saved sessions.
func (s *SessionStore) ListSessions() ([]SessionInfo, error) {
	sessionDir := filepath.Join(s.dir, "sessions")
	entries, err := os.ReadDir(sessionDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var sessions []SessionInfo
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		name := strings.TrimSuffix(entry.Name(), ".jsonl")

		// Count events by reading the file
		count, tokens, firstMsg := s.scanSessionFile(filepath.Join(sessionDir, entry.Name()))

		sessions = append(sessions, SessionInfo{
			Name:         name,
			Date:         info.ModTime(),
			EventCount:   count,
			TokenTotal:   tokens,
			FirstMessage: firstMsg,
		})
	}

	// Sort by date, newest first
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].Date.After(sessions[j].Date)
	})

	return sessions, nil
}

// LoadSession reads all events from a session file and returns them.
// DeleteSession removes a saved session file.
func (s *SessionStore) DeleteSession(name string) error {
	sessionDir := filepath.Join(s.dir, "sessions")
	path := filepath.Join(sessionDir, name+".jsonl")
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("delete session %q: %w", name, err)
	}
	return nil
}

func (s *SessionStore) LoadSession(name string) ([]agentic.OutputEvent, error) {
	sessionDir := filepath.Join(s.dir, "sessions")
	path := filepath.Join(sessionDir, name+".jsonl")

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var events []agentic.OutputEvent
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var event agentic.OutputEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		events = append(events, event)
	}

	return events, nil
}

// scanSessionFile reads a JSONL file and counts events/tokens and extracts
// the first user message text for display in session listings.
// ImportSession copies a JSONL file from sourcePath into the sessions
// directory under the given name. Returns an error if a session with
// that name already exists.
func (s *SessionStore) ImportSession(name, sourcePath string) error {
	sessionDir := filepath.Join(s.dir, "sessions")
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		return fmt.Errorf("create sessions dir: %w", err)
	}

	targetPath := filepath.Join(sessionDir, name+".jsonl")
	if _, err := os.Stat(targetPath); err == nil {
		return fmt.Errorf("session %q already exists", name)
	}

	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return fmt.Errorf("read source: %w", err)
	}

	// Validate that it looks like valid JSONL (at least one parseable event).
	lines := strings.Split(string(data), "\n")
	found := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var event agentic.OutputEvent
		if err := json.Unmarshal([]byte(line), &event); err == nil {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("no valid session events in %q", sourcePath)
	}

	if err := os.WriteFile(targetPath, data, 0644); err != nil {
		return fmt.Errorf("write session: %w", err)
	}
	return nil
}

func (s *SessionStore) scanSessionFile(path string) (count, tokens int, firstMsg string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, ""
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		count++

		var event agentic.OutputEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		if event.Timings != nil {
			tokens += event.Timings.PromptN + event.Timings.PredictedN
		}
		if event.ContextStats != nil {
			tokens = event.ContextStats.EstimatedTokens
		}
		// Capture the first user message text.
		if firstMsg == "" && event.Type == agentic.EventContent && event.Role == "user" && event.Text != "" {
			firstMsg = event.Text
		}
	}
	return
}

// randomID generates a short cryptographically random string for session IDs.
// Uses crypto/rand (via the shared internal.RandomString helper) rather than a
// time-seeded LCG, which collided/predicted IDs when two sessions started in
// the same nanosecond. See CORE-BUG-6.
func randomID(length int) string {
	return internal.RandomString(length)
}
