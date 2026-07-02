// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// AgentLogRingCapacity is the number of recent agent log lines retained in
// memory for inclusion in diagnostic exports, even when file logging is
// disabled (logging.file unset). This guarantees the provider-context trace
// (message roles, tool_call/tool_result presence per round, stream errors,
// retries) is always available to diagnose issues like a tool result never
// being sent back to the model.
const AgentLogRingCapacity = 4000

// AgentLogLine is a single captured log line.
type AgentLogLine struct {
	Time    time.Time
	Level   Level
	Message string
}

// agentLogRing is the global, always-on in-memory ring buffer of agent log
// lines. Every Logger.Log call appends here (in addition to its configured
// stdlib logger), so diagnostics never depend on logging.file being set.
var agentLogRing = newRingBuffer(AgentLogRingCapacity)

type ringBuffer struct {
	mu      sync.Mutex
	entries []AgentLogLine
	pos     int
	count   int
	cap     int
}

func newRingBuffer(capacity int) *ringBuffer {
	if capacity < 1 {
		capacity = 1
	}
	return &ringBuffer{
		entries: make([]AgentLogLine, capacity),
		cap:     capacity,
	}
}

func (r *ringBuffer) add(lv Level, msg string) {
	r.mu.Lock()
	r.entries[r.pos] = AgentLogLine{Time: time.Now(), Level: lv, Message: msg}
	r.pos = (r.pos + 1) % r.cap
	if r.count < r.cap {
		r.count++
	}
	r.mu.Unlock()
}

func (r *ringBuffer) snapshot() []AgentLogLine {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.count == 0 {
		return nil
	}
	out := make([]AgentLogLine, r.count)
	start := r.pos - r.count
	if start < 0 {
		start += r.cap
	}
	for i := 0; i < r.count; i++ {
		out[i] = r.entries[(start+i)%r.cap]
	}
	return out
}

func (r *ringBuffer) clear() {
	r.mu.Lock()
	r.pos = 0
	r.count = 0
	r.mu.Unlock()
}

// captureToRing appends a formatted line to the global agent log ring. It is
// called by Logger.Log for every emitted line.
func captureToRing(lv Level, msg string, args ...interface{}) {
	formatted := strings.TrimSpace(msg)
	if len(args) > 0 {
		formatted = sprintfSafe(msg, args...)
	}
	agentLogRing.add(lv, formatted)
}

// sprintfSafe formats without panicking on arg mismatches.
func sprintfSafe(format string, args ...interface{}) string {
	defer func() { _ = recover() }()
	return fmt.Sprintf(format, args...)
}

// AgentLogSnapshot returns the most recent agent log lines (oldest first),
// captured regardless of whether file logging is enabled.
func AgentLogSnapshot() []AgentLogLine {
	return agentLogRing.snapshot()
}

// ResetAgentLogRing clears the in-memory agent log ring. Primarily for tests.
func ResetAgentLogRing() {
	agentLogRing.clear()
}
