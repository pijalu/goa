// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package background

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// envPairs converts an env map into KEY=VALUE slices for exec.Cmd.
func envPairs(env map[string]string) []string {
	pairs := make([]string, 0, len(env))
	for k, v := range env {
		pairs = append(pairs, fmt.Sprintf("%s=%s", k, v))
	}
	return pairs
}

// writeFileAtomic writes data via a temp file + rename so a crash mid-write
// cannot corrupt the registry.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	return os.Rename(tmpName, path)
}

// teeWriter appends complete lines to a log file and to an in-memory ring
// buffer so output is both live-readable and durable across restarts.
type teeWriter struct {
	file *os.File
	ring *ringBuffer
	done chan struct{}
	mu   sync.Mutex
}

func newTeeWriter(path string, ring *ringBuffer) *teeWriter {
	tw := &teeWriter{ring: ring, done: make(chan struct{})}
	if path != "" {
		if f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			tw.file = f
		}
	}
	return tw
}

func (tw *teeWriter) line(s string) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if tw.ring != nil {
		tw.ring.Write(s)
	}
	if tw.file != nil {
		_, _ = tw.file.WriteString(s + "\n")
	}
}

// tailLines returns up to the last n lines of path. For large files only the
// trailing chunk is read.
func tailLines(path string, n int) []string {
	if n <= 0 {
		return nil
	}
	buf, ok := readTail(path)
	if !ok {
		return nil
	}
	lines := strings.Split(strings.TrimRight(string(buf), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// readTail returns the whole file, or just its trailing chunk when it is large
// enough that reading it all would be wasteful.
func readTail(path string) ([]byte, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	const chunk = 64 * 1024
	stat, err := f.Stat()
	if err != nil || stat.Size() <= int64(chunk) {
		b, err := io.ReadAll(f)
		if err != nil {
			return nil, false
		}
		return b, true
	}
	if _, err := f.Seek(-int64(chunk), io.SeekEnd); err != nil {
		return nil, false
	}
	b := make([]byte, chunk)
	r, _ := f.Read(b)
	b = b[:r]
	if i := bytes.IndexByte(b, '\n'); i >= 0 {
		return b[i+1:], true
	}
	return b, true
}

// ringBuffer is a circular buffer of text lines.
type ringBuffer struct {
	mu    sync.Mutex
	buf   []string
	size  int
	pos   int
	count int
}

func newRingBuffer(size int) *ringBuffer {
	return &ringBuffer{buf: make([]string, size), size: size}
}

func (rb *ringBuffer) Write(line string) {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	rb.buf[rb.pos] = line
	rb.pos = (rb.pos + 1) % rb.size
	if rb.count < rb.size {
		rb.count++
	}
}

func (rb *ringBuffer) ReadLast(n int) []string {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	if n > rb.count {
		n = rb.count
	}
	if n <= 0 {
		return nil
	}
	result := make([]string, n)
	for i := 0; i < n; i++ {
		idx := (rb.pos - n + i) % rb.size
		if idx < 0 {
			idx += rb.size
		}
		result[i] = rb.buf[idx]
	}
	return result
}
