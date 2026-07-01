// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package helper

import (
	"database/sql"
	"encoding/json"
	"os"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/pijalu/goa/internal/agentic"
)

// bufferedMessage holds a message with its session ID for later flushing.
type bufferedMessage struct {
	msg       StructuredMessage
	sessionID string
}

// MessageStorageObserver persists conversation history to SQLite.
type MessageStorageObserver struct {
	db            *sql.DB
	path          string
	mu            sync.Mutex
	buffer        []bufferedMessage
	batchSize     int
	flushInterval time.Duration
	sessionID     string
	stopFlush     chan struct{}

	// Reuse MessageLogObserver logic for building StructuredMessage
	logObserver *MessageLogObserver
}

// StorageOption configures a MessageStorageObserver.
type StorageOption func(*MessageStorageObserver)

// WithBatchSize sets the batch size for flushing messages.
func WithBatchSize(size int) StorageOption {
	return func(o *MessageStorageObserver) {
		o.batchSize = size
	}
}

// WithFlushInterval sets the interval for automatic flushing.
func WithFlushInterval(interval time.Duration) StorageOption {
	return func(o *MessageStorageObserver) {
		o.flushInterval = interval
	}
}

// NewMessageStorageObserver creates a new MessageStorageObserver.
func NewMessageStorageObserver(dbPath string, opts ...StorageOption) (*MessageStorageObserver, error) {
	// Ensure directory exists
	if dir := dbPath[:lastIndex(dbPath, "/")]; dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, err
		}
	}

	db, err := sql.Open("sqlite3", dbPath+"?_foreign_keys=on")
	if err != nil {
		return nil, err
	}

	if err := db.Ping(); err != nil {
		return nil, err
	}

	obs := &MessageStorageObserver{
		db:            db,
		path:          dbPath,
		batchSize:     10,
		flushInterval: 5 * time.Second,
		sessionID:     "default",
		stopFlush:     make(chan struct{}),
		logObserver:   NewMessageLogObserver(),
	}

	for _, opt := range opts {
		opt(obs)
	}

	// Initialize schema
	if err := obs.initSchema(); err != nil {
		return nil, err
	}

	// Start background flush goroutine
	go obs.flushLoop()

	return obs, nil
}

func (m *MessageStorageObserver) initSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS sessions (
		id TEXT PRIMARY KEY,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		title TEXT,
		metadata TEXT
	);

	CREATE TABLE IF NOT EXISTS messages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id TEXT NOT NULL,
		role TEXT NOT NULL,
		type TEXT NOT NULL,
		content TEXT,
		tool_name TEXT,
		tool_input TEXT,
		tool_result TEXT,
		tool_call_id TEXT,
		timings TEXT,
		created_at INTEGER NOT NULL,
		FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id);
	CREATE INDEX IF NOT EXISTS idx_messages_role ON messages(role);
	`

	_, err := m.db.Exec(schema)
	return err
}

func (m *MessageStorageObserver) flushLoop() {
	ticker := time.NewTicker(m.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.Flush()
		case <-m.stopFlush:
			return
		}
	}
}

// OnEvent implements agentic.OutputObserver.
func (m *MessageStorageObserver) OnEvent(event agentic.OutputEvent) {
	// Also pass to log observer to build StructuredMessage
	m.logObserver.OnEvent(event)

	// Check if message is complete (EventEnd)
	if event.Type == agentic.EventEnd {
		history := m.logObserver.History()
		if len(history) > 0 {
			// Get the last message (the one that just ended)
			lastMsg := history[len(history)-1]
			// Capture current session ID at message creation time
			m.mu.Lock()
			m.buffer = append(m.buffer, bufferedMessage{
				msg:       lastMsg,
				sessionID: m.sessionID,
			})
			m.mu.Unlock()

			// Check if we should flush
			m.mu.Lock()
			shouldFlush := len(m.buffer) >= m.batchSize
			m.mu.Unlock()

			if shouldFlush {
				m.Flush()
			}
		}
	}
}

// Flush writes buffered messages to the database.
func (m *MessageStorageObserver) Flush() {
	m.mu.Lock()
	buffer := m.buffer
	m.buffer = nil
	m.mu.Unlock()

	if len(buffer) == 0 {
		return
	}

	// Collect unique session IDs and ensure they all exist
	sessionIDs := make(map[string]bool)
	for _, bm := range buffer {
		sessionIDs[bm.sessionID] = true
	}
	for sessionID := range sessionIDs {
		m.ensureSession(sessionID)
	}

	tx, err := m.db.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO messages (session_id, role, type, content, tool_name, tool_input, tool_result, tool_call_id, timings, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return
	}
	defer stmt.Close()

	now := time.Now().Unix()
	for _, bm := range buffer {
		if err := m.insertMessage(stmt, bm, now); err != nil {
			return
		}
	}

	tx.Commit()
}

func (m *MessageStorageObserver) insertMessage(stmt *sql.Stmt, bm bufferedMessage, now int64) error {
	msg := bm.msg
	content, _ := json.Marshal(msg.Elements)
	timings := marshalTimings(msg.Timings)
	toolName, toolInput, toolResult, toolCallID := extractToolInfo(msg.Elements)
	_, err := stmt.Exec(bm.sessionID, msg.Role, "message", string(content), toolName, toolInput, toolResult, toolCallID, string(timings), now)
	return err
}

func marshalTimings(t *agentic.TokenTimings) []byte {
	if t == nil {
		return nil
	}
	b, _ := json.Marshal(t)
	return b
}

func extractToolInfo(elements []MessageElement) (toolName, toolInput, toolResult, toolCallID string) {
	for _, elem := range elements {
		if elem.Type == "tool_call" {
			toolName = elem.ToolName
			toolInput = elem.ToolInput
			toolCallID = elem.ToolCallID
		}
		if elem.Type == "tool_result" {
			toolResult = elem.ToolResult
		}
	}
	return
}

func (m *MessageStorageObserver) ensureSession(sessionID string) {
	now := time.Now().Unix()
	m.db.Exec(`
		INSERT OR IGNORE INTO sessions (id, created_at, updated_at)
		VALUES (?, ?, ?)
	`, sessionID, now, now)
}

// GetMessagesBySession retrieves all messages for a session.
func (m *MessageStorageObserver) GetMessagesBySession(sessionID string) ([]StructuredMessage, error) {
	rows, err := m.db.Query(`
		SELECT role, content, tool_name, tool_input, tool_result, tool_call_id, timings
		FROM messages WHERE session_id = ? ORDER BY id
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []StructuredMessage
	for rows.Next() {
		var msg StructuredMessage
		var content, toolName, toolInput, toolResult, toolCallID, timings []byte

		if err := rows.Scan(&msg.Role, &content, &toolName, &toolInput, &toolResult, &toolCallID, &timings); err != nil {
			continue
		}

		// Reconstruct elements
		var elements []MessageElement
		json.Unmarshal(content, &elements)
		msg.Elements = elements

		if len(timings) > 0 {
			var t agentic.TokenTimings
			json.Unmarshal(timings, &t)
			msg.Timings = &t
		}

		messages = append(messages, msg)
	}

	return messages, nil
}

// SetSessionID sets the current session ID.
func (m *MessageStorageObserver) SetSessionID(sessionID string) {
	m.sessionID = sessionID
}

// Close closes the database connection and stops the flush goroutine.
func (m *MessageStorageObserver) Close() error {
	close(m.stopFlush)
	m.Flush()
	return m.db.Close()
}

// Helper for string last index
func lastIndex(s, sep string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if i >= len(sep)-1 && s[i-len(sep)+1:i+1] == sep {
			return i - len(sep) + 1
		}
	}
	return -1
}
