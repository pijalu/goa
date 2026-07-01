// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package multiagent implements agent collaboration patterns via the
// ForegroundOrchestrator: pair (planner → coder), review (companion),
// companion-minor (per-turn review), and explicit workflow pipelines.
//
// The deprecated, goroutine-leaking PairOrchestrator / ReviewerOrchestrator
// types (formerly in this file) were removed in W4 — they spawned agents on
// context.Background() with no cancellation tied to Stop(), raced on shared
// counters, and had a blocking emit(). ForegroundOrchestrator supersedes them.
package multiagent

import "time"

// OrchestratorMessage represents a message between agents or to the user.
type OrchestratorMessage struct {
	From      string    `json:"from"`
	To        string    `json:"to"`
	Content   string    `json:"content"`
	Kind      string    `json:"kind"` // "content", "thinking_start", "thinking_chunk", "thinking_end"
	Turn      int       `json:"turn"`
	Timestamp time.Time `json:"timestamp"`
}
