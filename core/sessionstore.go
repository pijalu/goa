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
	// HasModelTurn reports whether the session holds at least one assistant
	// text reply. The /session picker hides sessions without a model turn;
	// export/dream flows still see them via ListSessions.
	HasModelTurn bool `json:"has_model_turn"`
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
		count, tokens, firstMsg, hasConversation, hasModelTurn := s.scanSessionFile(filepath.Join(sessionDir, entry.Name()))
		if !hasConversation {
			// Empty session: no user/assistant conversation content (e.g.
			// only system or stats events, or a session abandoned before the
			// first reply). Restoring it would show a blank transcript, so it
			// is hidden from pickers and listings. The file is kept on disk.
			continue
		}

		sessions = append(sessions, SessionInfo{
			Name:         name,
			Date:         info.ModTime(),
			EventCount:   count,
			TokenTotal:   tokens,
			FirstMessage: firstMsg,
			HasModelTurn: hasModelTurn,
		})
	}

	// Sort newest first; break ModTime ties by name (session names embed a
	// timestamp, so the tiebreak stays chronological) for a stable order
	// when several sessions share a timestamp.
	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].Date.Equal(sessions[j].Date) {
			return sessions[i].Name > sessions[j].Name
		}
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

// sessionScan accumulates listing metadata while scanning a session file.
type sessionScan struct {
	count          int
	tokens         int
	firstMsg       string
	hasConversation bool
	hasModelTurn   bool
}

// absorb folds one JSONL line into the scan: event count, token totals, the
// first user message, and the conversation/model-turn markers.
func (sc *sessionScan) absorb(line string) {
	var event agentic.OutputEvent
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return
	}
	if event.Timings != nil {
		sc.tokens += event.Timings.PromptN + event.Timings.PredictedN
	}
	if event.ContextStats != nil {
		sc.tokens = event.ContextStats.EstimatedTokens
	}
	// Conversation content: any real user or assistant text. Sessions
	// holding only system/stats/progress events are "empty" for listing
	// purposes — restoring them would show a blank transcript.
	isContent := event.Type == agentic.EventContent && event.Text != ""
	if isContent && (event.Role == agentic.User || event.Role == agentic.Assistant) {
		sc.hasConversation = true
	}
	// Model-turn marker: at least one assistant text reply. The session
	// picker hides sessions without one (bugs.md "must not list sessions
	// without an actual model turn") while the store still lists them for
	// export/dream flows.
	if isContent && event.Role == agentic.Assistant {
		sc.hasModelTurn = true
	}
	// Capture the first user message text.
	if sc.firstMsg == "" && event.Type == agentic.EventContent && event.Role == agentic.User && event.Text != "" {
		sc.firstMsg = event.Text
	}
}

func (s *SessionStore) scanSessionFile(path string) (count, tokens int, firstMsg string, hasConversation bool, hasModelTurn bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, "", false, false
	}

	var sc sessionScan
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		sc.count++
		sc.absorb(line)
	}
	return sc.count, sc.tokens, sc.firstMsg, sc.hasConversation, sc.hasModelTurn
}

// randomID generates a short cryptographically random string for session IDs.
// Uses crypto/rand (via the shared internal.RandomString helper) rather than a
// time-seeded LCG, which collided/predicted IDs when two sessions started in
// the same nanosecond. See CORE-BUG-6.
func randomID(length int) string {
	return internal.RandomString(length)
}

// ── Input History ──

// InputEntry records a single user input in a session's input history file.
type InputEntry struct {
	Text      string `json:"t"`
	Timestamp int64  `json:"ts"` // unix nano
}

// inputHistoryDir returns the directory for per-session input history files.
func (s *SessionStore) inputHistoryDir() string {
	return filepath.Join(s.dir, "sessions", "inputs")
}

// inputHistoryPath returns the path to a session's input history file.
func (s *SessionStore) inputHistoryPath(sessionID string) string {
	return filepath.Join(s.inputHistoryDir(), sessionID+".jsonl")
}

// RecordInput appends a user input to the current session's input history file.
// Creates the file if it doesn't exist. Safe for concurrent append by multiple
// processes because each process writes to a different session file.
func (s *SessionStore) RecordInput(sessionID, text string) error {
	if sessionID == "" || text == "" {
		return nil
	}
	dir := s.inputHistoryDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create input history dir: %w", err)
	}
	entry := InputEntry{Text: text, Timestamp: time.Now().UnixNano()}
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal input entry: %w", err)
	}
	path := s.inputHistoryPath(sessionID)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open input history file: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write input history: %w", err)
	}
	return nil
}

// LoadAllInputHistory scans all session input history files and returns
// deduplicated input texts, sorted by recency (oldest-first for the editor),
// capped at max entries. If excludeSession is non-empty, entries from that
// session are omitted. If max <= 0, returns nil (disabled).
func (s *SessionStore) LoadAllInputHistory(max int, excludeSession string) []string {
	if max <= 0 {
		return nil
	}
	entries := s.readAllInputEntries()
	return s.buildHistory(entries, max, excludeSession)
}

// LoadSessionInputHistory loads input entries for a specific session.
func (s *SessionStore) LoadSessionInputHistory(sessionName string) []InputEntry {
	path := s.inputHistoryPath(sessionName)
	return s.readInputFile(path)
}

// readAllInputEntries reads InputEntry from all files in the input history directory.
func (s *SessionStore) readAllInputEntries() []InputEntry {
	dir := s.inputHistoryDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return nil
	}
	var all []InputEntry
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		all = append(all, s.readInputFile(path)...)
	}
	return all
}

// readInputFile reads all InputEntry from a single JSONL file.
func (s *SessionStore) readInputFile(path string) []InputEntry {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var entries []InputEntry
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry InputEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.Text != "" {
			entries = append(entries, entry)
		}
	}
	return entries
}

// buildHistory deduplicates InputEntry by text (keep most recent), sorts by
// timestamp descending, caps at max, then reverses to oldest-first for the
// editor. If excludeSession is non-empty, entries whose source file matches
// that session name are excluded.
func (s *SessionStore) buildHistory(all []InputEntry, max int, excludeSession string) []string {
	// Dedup by text, keep most recent timestamp
	seen := make(map[string]int64) // text → latest timestamp
	for _, entry := range all {
		if ts, ok := seen[entry.Text]; ok {
			if entry.Timestamp > ts {
				seen[entry.Text] = entry.Timestamp
			}
		} else {
			seen[entry.Text] = entry.Timestamp
		}
	}

	// Build slice and sort by timestamp descending (most recent first)
	deduped := make([]InputEntry, 0, len(seen))
	for text, ts := range seen {
		deduped = append(deduped, InputEntry{Text: text, Timestamp: ts})
	}
	sort.Slice(deduped, func(i, j int) bool {
		return deduped[i].Timestamp > deduped[j].Timestamp
	})

	// Cap at max
	if len(deduped) > max {
		deduped = deduped[:max]
	}

	// Reverse to oldest-first for editor
	result := make([]string, len(deduped))
	for i, entry := range deduped {
		result[len(result)-1-i] = entry.Text
	}
	return result
}