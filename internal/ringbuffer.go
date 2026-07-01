// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package internal

import (
	"sync"
)

// RingBuffer is a thread-safe ring buffer for storing lines of output.
// Used by PTY sessions and bg_exec for managing process output.
type RingBuffer struct {
	data    []string
	maxSize int
	pos     int
	count   int
	mu      sync.RWMutex
}

// NewRingBuffer creates a ring buffer with the given capacity.
func NewRingBuffer(maxSize int) *RingBuffer {
	if maxSize < 1 {
		maxSize = 100
	}
	return &RingBuffer{
		data:    make([]string, maxSize),
		maxSize: maxSize,
	}
}

// Write adds a line to the buffer. Thread-safe.
func (rb *RingBuffer) Write(line string) {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	rb.data[rb.pos] = line
	rb.pos = (rb.pos + 1) % rb.maxSize
	if rb.count < rb.maxSize {
		rb.count++
	}
}

// Read returns the last N lines from the buffer, newest last.
// If tail <= 0 or tail > count, returns all lines.
func (rb *RingBuffer) Read(tail int) []string {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	if rb.count == 0 {
		return nil
	}
	if tail <= 0 || tail > rb.count {
		tail = rb.count
	}
	result := make([]string, tail)
	start := rb.pos - tail
	if start < 0 {
		start += rb.maxSize
	}
	for i := 0; i < tail; i++ {
		idx := (start + i) % rb.maxSize
		result[i] = rb.data[idx]
	}
	return result
}

// ReadAll returns all lines in order (oldest first).
func (rb *RingBuffer) ReadAll() []string {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	if rb.count == 0 {
		return nil
	}
	result := make([]string, rb.count)
	for i := 0; i < rb.count; i++ {
		idx := (rb.pos - rb.count + i) % rb.maxSize
		if idx < 0 {
			idx += rb.maxSize
		}
		result[i] = rb.data[idx]
	}
	return result
}

// Len returns the number of lines currently in the buffer.
func (rb *RingBuffer) Len() int {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	return rb.count
}

// Clear empties the buffer.
func (rb *RingBuffer) Clear() {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	rb.pos = 0
	rb.count = 0
}
