// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// keyLogger is an asynchronous, buffered trace logger for raw TUI key events.
// It is off by default; when enabled it opens a single file once and writes
// key events from a dedicated goroutine so that the input hot path never blocks
// on disk I/O.
type keyLogger struct {
	path string
	file *os.File
	ch   chan string
	done chan struct{}
	wg   sync.WaitGroup
}

// newKeyLogger opens path for append and starts the background writer.
// The file is created with 0600 permissions and intermediate directories with 0700.
func newKeyLogger(path string) (*keyLogger, error) {
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, fmt.Errorf("create key-log directory: %w", err)
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, fmt.Errorf("open key-log file: %w", err)
	}
	k := &keyLogger{
		path: path,
		file: f,
		ch:   make(chan string, 1024),
		done: make(chan struct{}),
	}
	k.wg.Add(1)
	go k.run()
	return k, nil
}

// run reads formatted log lines from the channel and writes them to file.
// It drains the channel when done is closed.
func (k *keyLogger) run() {
	defer k.wg.Done()
	for {
		select {
		case s := <-k.ch:
			fmt.Fprint(k.file, s)
		case <-k.done:
			for {
				select {
				case s := <-k.ch:
					fmt.Fprint(k.file, s)
				default:
					return
				}
			}
		}
	}
}

// logf formats and enqueues a log line. If the channel is full the line is
// dropped rather than blocking the input hot path.
func (k *keyLogger) logf(format string, args ...any) {
	if k == nil {
		return
	}
	select {
	case k.ch <- fmt.Sprintf(format, args...):
	default:
	}
}

// close shuts down the logger and flushes any buffered lines.
func (k *keyLogger) close() error {
	if k == nil {
		return nil
	}
	close(k.done)
	k.wg.Wait()
	if k.file != nil {
		return k.file.Close()
	}
	return nil
}
