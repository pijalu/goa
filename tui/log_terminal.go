// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// LogTerminal wraps a Terminal and records every byte written to a debug log
// file. It is intended for tracing TUI/compositor output when diagnosing
// rendering bugs such as duplicated lines or scrollback corruption.
// Enable it by setting GOA_DEBUG_TERMINAL to a file path.
type LogTerminal struct {
	Terminal
	mu   sync.Mutex
	file *os.File
}

// NewLogTerminal opens a LogTerminal that forwards all terminal operations to
// term and writes a copy of every Write/WriteString to logPath.
func NewLogTerminal(term Terminal, logPath string) (*LogTerminal, error) {
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	return &LogTerminal{Terminal: term, file: f}, nil
}

// Write writes to the underlying terminal and appends a copy to the log.
func (t *LogTerminal) Write(p []byte) (int, error) {
	t.logWrite(p)
	return t.Terminal.Write(p)
}

// WriteString writes to the underlying terminal and appends a copy to the log.
func (t *LogTerminal) WriteString(s string) {
	t.logWrite([]byte(s))
	t.Terminal.WriteString(s)
}

// Stop closes the log file before stopping the wrapped terminal.
func (t *LogTerminal) Stop() {
	t.mu.Lock()
	if t.file != nil {
		_ = t.file.Close()
		t.file = nil
	}
	t.mu.Unlock()
	t.Terminal.Stop()
}

func (t *LogTerminal) logWrite(p []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.file == nil || len(p) == 0 {
		return
	}
	// Timestamped header followed by the raw bytes (with escapes intact).
	_, _ = fmt.Fprintf(t.file, "\n# %s write %d bytes\n", time.Now().Format(time.RFC3339Nano), len(p))
	_, _ = t.file.Write(p)
	_, _ = t.file.WriteString("\n")
	_ = t.file.Sync()
}

var (
	_ Terminal = (*LogTerminal)(nil)
	_ io.Writer = (*LogTerminal)(nil)
)
