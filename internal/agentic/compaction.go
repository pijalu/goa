// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package agentic provides the core agent SDK — message types, context compression,
// and tool execution primitives for building LLM-powered agents.
package agentic

import (
	"time"
)

// DefaultMicroCompactionConfig is the default configuration for micro compaction.
var DefaultMicroCompactionConfig = MicroCompactionConfig{
	KeepRecentMessages: 20,
	MinContentTokens:   100,
	CacheMissThreshold: 1 * time.Hour,
	TruncatedMarker:    "[Old tool result content cleared]",
	MinContextRatio:    0.5,
}

// MicroCompactionConfig controls the micro compaction strategy.
type MicroCompactionConfig struct {
	// KeepRecentMessages is the number of most recent messages to never touch.
	KeepRecentMessages int

	// MinContentTokens is the minimum content size in tokens before truncating.
	// Messages below this threshold are left intact.
	MinContentTokens int

	// CacheMissThreshold is how long the agent must have been idle before
	// micro compaction is triggered on the next turn.
	CacheMissThreshold time.Duration

	// TruncatedMarker is the replacement text for cleared tool results.
	TruncatedMarker string

	// MinContextRatio is the minimum context usage ratio to trigger compaction.
	// 0.5 means at least 50% of the context window must be in use.
	MinContextRatio float64
}

// microCompactForced is the forced variant of micro compaction. When force is
// true, the MinContextRatio check is skipped so a manual /compress invocation
// can run even when usage is below the configured ratio.
func (a *Agent) microCompactForced(force bool) {
	cfg := a.cfg.ContextCompression.MicroCompaction
	history := a.history
	if len(history) == 0 {
		return
	}

	contextRatio := a.contextRatio()
	if !force && contextRatio < cfg.MinContextRatio {
		return
	}

	keepIdx := computeKeepIdx(history, cfg.KeepRecentMessages, force)
	changed := a.truncateToolResults(history, keepIdx, cfg)
	if changed > 0 {
		if a.cfg.Logger != nil {
			a.cfg.Logger.Log(Info, "Applied micro compaction: truncated %d tool results, keepIdx=%d, ratio=%.1f%%",
				changed, keepIdx, contextRatio*100)
		}
		a.emitEvent(OutputEvent{Type: EventCompact, Text: "micro"})
	}
}

func (a *Agent) contextRatio() float64 {
	maxTokens := a.effectiveMaxTokens()
	if maxTokens == 0 {
		return 0
	}
	stats := a.computeContextStats()
	return float64(stats.EstimatedTokens) / float64(maxTokens)
}

func computeKeepIdx(history []Message, keepRecent int, force bool) int {
	if keepRecent > len(history)-1 {
		keepRecent = len(history) - 1
	}
	if keepRecent < 0 {
		keepRecent = 0
	}
	if force {
		keepRecent = capForcedKeepRecent(len(history), keepRecent)
	}
	keepIdx := len(history) - keepRecent
	if keepIdx < 1 {
		keepIdx = 1
	}
	return keepIdx
}

func capForcedKeepRecent(historyLen, keepRecent int) int {
	maxKeep := historyLen / 2
	if maxKeep < 1 {
		maxKeep = 1
	}
	if maxKeep > 5 {
		maxKeep = 5
	}
	if keepRecent > maxKeep {
		return maxKeep
	}
	return keepRecent
}

func (a *Agent) truncateToolResults(history []Message, keepIdx int, cfg MicroCompactionConfig) int {
	changed := 0
	for i := 1; i < keepIdx && i < len(history); i++ {
		msg := &history[i]
		if msg.Role != ToolRole {
			continue
		}
		contentTokens := len(msg.Content) / 4
		if contentTokens < cfg.MinContentTokens {
			continue
		}
		msg.Content = cfg.TruncatedMarker
		changed++
	}
	return changed
}
