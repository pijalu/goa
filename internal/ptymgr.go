// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package internal

import (
	"fmt"
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
	return pm.start(id, exec.Command("sh", "-c", command), command, "", cols, rows)
}

// OpenShell creates a persistent interactive shell session (bash) that
// survives across tool calls. dir is the initial working directory (empty
// inherits the parent's); commands are delivered later via Write/Send.
func (pm *PTYManager) OpenShell(id, dir string, cols, rows int) (*PTYSession, error) {
	return pm.start(id, exec.Command("bash"), "bash", dir, cols, rows)
}

// start is the shared session spawner: it holds the manager lock, rejects
// duplicate IDs, attaches the PTY, and registers the session. display is the
// human-readable command shown in List output.
func (pm *PTYManager) start(id string, cmd *exec.Cmd, display, dir string, cols, rows int) (*PTYSession, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if _, exists := pm.sessions[id]; exists {
		return nil, fmt.Errorf("session %q already exists", id)
	}
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}

	// Set up environment
	cmd.Dir = dir
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
		Command:   display,
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
			s.mu.Lock()
			s.running = false
			s.mu.Unlock()
			return
		}
		if n > 0 {
			s.Buffer.Write(string(buf[:n]))
			// Wake any waiting Send/ReadBlocking callers without blocking.
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

// Send writes text to a session and, when wait > 0, blocks until the shell's
// output goes silent (the command's output plus the new prompt have landed),
// the session exits, or the timeout elapses, returning the new output. With
// wait <= 0 it returns immediately with no output.
func (pm *PTYManager) Send(id, text string, wait time.Duration) (string, error) {
	s, err := pm.getSession(id)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return "", fmt.Errorf("session %q is not running", id)
	}
	if _, err := s.PTY.Write([]byte(text)); err != nil {
		s.mu.Unlock()
		return "", fmt.Errorf("write to session %q: %w", id, err)
	}
	s.mu.Unlock()

	if wait <= 0 {
		return "", nil
	}

	mark := s.Buffer.Len()
	pm.waitForOutputSilence(s, mark, wait)

	newCount := s.Buffer.Len() - mark
	if newCount <= 0 {
		return "", nil
	}
	return concatChunks(s.Buffer.Read(newCount)), nil
}

// waitForOutputSilence blocks until the session's buffer has grown past mark
// and then stayed silent for sendSilence (the command finished producing
// output), the session exits, or the deadline elapses. The tty echoes the
// typed command first, then the command's own output and the next shell
// prompt follow; returning on the first chunk would hand back only the echo.
func (pm *PTYManager) waitForOutputSilence(s *PTYSession, mark int, wait time.Duration) {
	deadline := time.Now().Add(wait)
	const sendSilence = 500 * time.Millisecond
	lastGrow := time.Now()
	lastLen := mark
	for {
		currentLen := s.Buffer.Len()
		if currentLen > lastLen {
			lastLen = currentLen
			lastGrow = time.Now()
		}
		if currentLen > mark && time.Since(lastGrow) >= sendSilence {
			return
		}
		if !s.IsRunning() {
			return
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return
		}
		select {
		case <-s.dataReady:
		case <-time.After(min(sendSilence, remaining)):
		}
	}
}

// Read returns the last N lines from the PTY output buffer.
func (pm *PTYManager) Read(id string, tail int) (string, error) {
	s, err := pm.getSession(id)
	if err != nil {
		return "", err
	}
	return concatChunks(s.Buffer.Read(tail)), nil
}

// ReadRange returns a bounded page of retained output: up to count chunks
// ending offset chunks before the newest (offset 0 = newest). Negative
// offsets are clamped to 0; count <= 0 falls back to 500. An offset at or
// beyond the retained history returns an empty page.
func (pm *PTYManager) ReadRange(id string, offset, count int) (string, error) {
	s, err := pm.getSession(id)
	if err != nil {
		return "", err
	}
	if count <= 0 {
		count = 500
	}
	if offset < 0 {
		offset = 0
	}
	all := s.Buffer.ReadAll()
	total := len(all)
	if offset >= total {
		return "", nil
	}
	end := total - offset
	start := end - count
	if start < 0 {
		start = 0
	}
	return concatChunks(all[start:end]), nil
}

// ReadBlocking waits up to timeout for output to appear and returns all output
// currently in the buffer. If the buffer already contains data when called, it
// returns immediately instead of waiting for more data.
func (pm *PTYManager) ReadBlocking(id string, timeout time.Duration) (string, error) {
	s, err := pm.getSession(id)
	if err != nil {
		return "", err
	}

	// If no data is available yet, wait for the readOutput goroutine to signal
	// that it has written something, or for the timeout to expire.
	if s.Buffer.Len() == 0 {
		select {
		case <-time.After(timeout):
		case <-s.dataReady:
		}
	}

	return concatChunks(s.Buffer.ReadAll()), nil
}

// concatChunks joins ring-buffer chunks back into a single stream. Chunks are
// arbitrary PTY reads, not newline-terminated lines, so they are concatenated
// verbatim (matching the historical Read behavior).
func concatChunks(chunks []string) string {
	var result string
	for _, line := range chunks {
		result += line
	}
	return result
}

// IsRunning returns whether the PTY session's process is still running.
func (s *PTYSession) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
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
	if s.Cmd != nil {
		killSessionTree(s.Cmd)
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
