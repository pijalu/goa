// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package internal

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/creack/pty"
)

// PTYSessionInfo holds metadata about a PTY session.
type PTYSessionInfo struct {
	ID        string    `json:"id"`
	Command   string    `json:"command"`
	CreatedAt time.Time `json:"created_at"`
	Running   bool      `json:"running"`
	PID       int       `json:"pid"`
}

// PTYSession manages a single pseudo-terminal session.
type PTYSession struct {
	ID        string
	Command   string
	PTY       *os.File
	Cmd       *exec.Cmd
	Buffer    *RingBuffer
	CreatedAt time.Time
	done      chan struct{}
	mu        sync.Mutex
	running   bool
	dataReady chan struct{} // signaled (non-blocking) when new data is written to Buffer
}

// PTYManager manages multiple PTY sessions.
type PTYManager struct {
	sessions map[string]*PTYSession
	mu       sync.Mutex
}

// NewPTYManager creates a new PTY manager.
func NewPTYManager() *PTYManager {
	return &PTYManager{
		sessions: make(map[string]*PTYSession),
	}
}

// Start creates a new PTY session and spawns the command.
func (pm *PTYManager) Start(id, command string, cols, rows int) (*PTYSession, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if _, exists := pm.sessions[id]; exists {
		return nil, fmt.Errorf("session %q already exists", id)
	}

	cmd := exec.Command("sh", "-c", command)

	// Set up environment
	cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		fmt.Sprintf("COLUMNS=%d", cols),
		fmt.Sprintf("LINES=%d", rows),
	)

	// Create PTY
	f, err := pty.StartWithSize(cmd, &pty.Winsize{
		Cols: uint16(cols),
		Rows: uint16(rows),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to start PTY: %w", err)
	}

	session := &PTYSession{
		ID:        id,
		Command:   command,
		PTY:       f,
		Cmd:       cmd,
		Buffer:    NewRingBuffer(10000),
		CreatedAt: time.Now(),
		done:      make(chan struct{}),
		running:   true,
		dataReady: make(chan struct{}, 1),
	}

	// Start reading output in background
	go pm.readOutput(session)

	pm.sessions[id] = session
	return session, nil
}

// readOutput reads PTY output and stores it in the ring buffer.
func (pm *PTYManager) readOutput(s *PTYSession) {
	buf := make([]byte, 4096)
	for {
		select {
		case <-s.done:
			return
		default:
		}
		n, err := s.PTY.Read(buf)
		if err != nil {
			if err != io.EOF {
				s.mu.Lock()
				s.running = false
				s.mu.Unlock()
			}
			return
		}
		if n > 0 {
			s.Buffer.Write(string(buf[:n]))
			// Wake any waiting ReadBlocking callers without blocking.
			select {
			case s.dataReady <- struct{}{}:
			default:
			}
		}
	}
}

// Write sends input to the PTY's stdin.
func (pm *PTYManager) Write(id, input string) error {
	s, err := pm.getSession(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return fmt.Errorf("session %q is not running", id)
	}
	if _, err := s.PTY.Write([]byte(input)); err != nil {
		return fmt.Errorf("write to session %q: %w", id, err)
	}
	return nil
}

// Read returns the last N lines from the PTY output buffer.
func (pm *PTYManager) Read(id string, tail int) (string, error) {
	s, err := pm.getSession(id)
	if err != nil {
		return "", err
	}
	lines := s.Buffer.Read(tail)
	if len(lines) == 0 {
		return "", nil
	}
	result := ""
	for _, line := range lines {
		result += line
	}
	return result, nil
}

// ReadBlocking reads output with a timeout — returns when new data arrives or timeout expires.
// Uses a signal channel instead of polling to avoid unnecessary CPU wake-ups.
func (pm *PTYManager) ReadBlocking(id string, timeout time.Duration) (string, error) {
	s, err := pm.getSession(id)
	if err != nil {
		return "", err
	}
	startLen := s.Buffer.Len()
	deadline := time.After(timeout)

	// Pre-check: data might already be available.
	if s.Buffer.Len() > startLen {
		goto collect
	}

	select {
	case <-deadline:
		// Timeout — check if any data arrived anyway.
		if s.Buffer.Len() > startLen {
			goto collect
		}
		return "", nil
	case <-s.dataReady:
		// Data was written. Fall through to collect.
	}

collect:
	lines := s.Buffer.ReadAll()
	if len(lines) > startLen {
		result := ""
		for _, line := range lines[startLen:] {
			result += line
		}
		return result, nil
	}
	return "", nil
}

// Resize changes the PTY dimensions.
func (pm *PTYManager) Resize(id string, cols, rows int) error {
	s, err := pm.getSession(id)
	if err != nil {
		return err
	}
	return pty.Setsize(s.PTY, &pty.Winsize{
		Cols: uint16(cols),
		Rows: uint16(rows),
	})
}

// Stop terminates a PTY session.
func (pm *PTYManager) Stop(id string) error {
	pm.mu.Lock()
	s, ok := pm.sessions[id]
	if !ok {
		pm.mu.Unlock()
		return fmt.Errorf("session %q not found", id)
	}
	delete(pm.sessions, id)
	pm.mu.Unlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		close(s.done)
		s.running = false
	}
	if s.PTY != nil {
		s.PTY.Close()
	}
	if s.Cmd != nil && s.Cmd.Process != nil {
		s.Cmd.Process.Kill()
	}
	return nil
}

// List returns all active session IDs with metadata.
func (pm *PTYManager) List() []PTYSessionInfo {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	var result []PTYSessionInfo
	for id, s := range pm.sessions {
		info := PTYSessionInfo{
			ID:        id,
			Command:   s.Command,
			CreatedAt: s.CreatedAt,
			PID:       0,
		}
		s.mu.Lock()
		info.Running = s.running
		if s.Cmd != nil && s.Cmd.Process != nil {
			info.PID = s.Cmd.Process.Pid
		}
		s.mu.Unlock()
		result = append(result, info)
	}
	return result
}

// Cleanup terminates all sessions (call on Goa exit).
func (pm *PTYManager) Cleanup() {
	pm.mu.Lock()
	ids := make([]string, 0, len(pm.sessions))
	for id := range pm.sessions {
		ids = append(ids, id)
	}
	pm.mu.Unlock()
	for _, id := range ids {
		pm.Stop(id)
	}
}

func (pm *PTYManager) getSession(id string) (*PTYSession, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	s, ok := pm.sessions[id]
	if !ok {
		return nil, fmt.Errorf("session %q not found", id)
	}
	return s, nil
}
