// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import "sync"

// SteeringQueue buffers user messages sent while the agent is still running.
// They are merged and delivered as a single follow-up turn when the current
// turn completes.
type SteeringQueue struct {
	mu      sync.Mutex
	pending []string
}

// NewSteeringQueue creates an empty steering queue.
func NewSteeringQueue() *SteeringQueue {
	return &SteeringQueue{}
}

// Append adds a user message to the queue.
func (sq *SteeringQueue) Append(input string) {
	sq.mu.Lock()
	defer sq.mu.Unlock()
	sq.pending = append(sq.pending, input)
}

// Flush returns all pending messages and clears the queue.
func (sq *SteeringQueue) Flush() []string {
	sq.mu.Lock()
	defer sq.mu.Unlock()
	pending := sq.pending
	sq.pending = nil
	return pending
}

// Len returns the number of pending steering messages.
func (sq *SteeringQueue) Len() int {
	sq.mu.Lock()
	defer sq.mu.Unlock()
	return len(sq.pending)
}
