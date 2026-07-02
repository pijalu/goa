// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"crypto/sha256"
	"fmt"
	"sync"
)

// LoopWarningLevel indicates the severity of a loop detection warning.
type LoopWarningLevel int

const (
	LoopOK        LoopWarningLevel = 0
	LoopWarning   LoopWarningLevel = 1
	LoopCritical  LoopWarningLevel = 2
	LoopInterrupt LoopWarningLevel = 3
)

// LoopDetector monitors agent behavior for problematic patterns.
//
// Two detection paths are wired into the AgentManager today:
//   - tool-call repeat detection (RecordToolCall), and
//   - thinking/reasoning loop detection (RecordThinkingDelta), which catches
//     an assistant that emits the same reasoning paragraph over and over in a
//     single turn — a failure mode the tool-repeat check cannot see because no
//     tool is invoked.
//
// Earlier revisions advertised token-budget, error-rate, activity-timeout, and
// conversational-loop detection, but those code paths were never invoked at
// runtime — giving a false sense of safety. They have been removed (along with
// their config fields) so the surface area reflects reality. See STUB-1/BUG-11.
type LoopDetector struct {
	mu sync.Mutex

	// Tool call tracking — drives RecordToolCall loop detection.
	turnToolCalls          map[string]int // key: toolName+hash(input) → count
	loopWarningThreshold   int            // same tool call count before warning
	loopInterruptThreshold int            // same tool call count before interrupt

	// Thinking-loop tracking — drives RecordThinkingDelta loop detection.
	// Complete lines (terminated by '\n') are hashed and counted; only lines
	// longer than minThinkLineLen are counted so short repeated bullets or
	// separators do not false-positive. thinkMaxRepeat tracks the highest count
	// seen for any single line in the current turn.
	thinkPending          string
	thinkLineCounts       map[string]int
	thinkMaxRepeat        int
	thinkWarningThreshold int
	thinkInterruptThreshold int

	// Error tracking (ring buffer). Populated by RecordToolResult; retained as
	// the integration point for a future (genuinely wired) error-rate check.
	errorHistory []bool // last N tool results (true = error)
	errorIdx     int
}

// LoopDetectorConfig holds configurable parameters for the loop detector.
// Only the repeat thresholds are used; the unused token/error/activity fields
// were removed when their dead detection paths were deleted (STUB-1).
type LoopDetectorConfig struct {
	LoopWarning   int
	LoopInterrupt int
	// ThinkingLoopWarning/Interrupt bound how many times the same significant
	// line of reasoning may repeat within a single turn before action is taken.
	// Zero falls back to the defaults in DefaultLoopDetectorConfig.
	ThinkingLoopWarning   int
	ThinkingLoopInterrupt int
}

// DefaultLoopDetectorConfig returns sensible defaults for the loop detector.
func DefaultLoopDetectorConfig() LoopDetectorConfig {
	return LoopDetectorConfig{
		LoopWarning:           3,
		LoopInterrupt:         5,
		ThinkingLoopWarning:   4,
		ThinkingLoopInterrupt: 6,
	}
}

const loopErrorHistorySize = 10

// minThinkLineLen is the minimum length a line of reasoning must reach before
// it contributes to thinking-loop counting. This excludes short repeated
// constructs (list markers, separators, single words) that legitimately recur.
const minThinkLineLen = 40

// NewLoopDetector creates a loop detector with the given config.
func NewLoopDetector(cfg LoopDetectorConfig) *LoopDetector {
	if cfg.ThinkingLoopWarning <= 0 {
		cfg.ThinkingLoopWarning = 4
	}
	if cfg.ThinkingLoopInterrupt <= 0 {
		cfg.ThinkingLoopInterrupt = 6
	}
	return &LoopDetector{
		turnToolCalls:           make(map[string]int),
		errorHistory:            make([]bool, loopErrorHistorySize),
		loopWarningThreshold:    cfg.LoopWarning,
		loopInterruptThreshold:  cfg.LoopInterrupt,
		thinkLineCounts:         make(map[string]int),
		thinkWarningThreshold:   cfg.ThinkingLoopWarning,
		thinkInterruptThreshold: cfg.ThinkingLoopInterrupt,
	}
}

// RecordToolCall records a tool call and checks for loop patterns.
// Returns a warning level: LoopOK (normal), LoopWarning, or LoopInterrupt.
func (ld *LoopDetector) RecordToolCall(name, input string) LoopWarningLevel {
	ld.mu.Lock()
	defer ld.mu.Unlock()

	key := name + ":" + hashInput(input)
	ld.turnToolCalls[key]++

	count := ld.turnToolCalls[key]
	switch {
	case count >= ld.loopInterruptThreshold:
		return LoopInterrupt
	case count >= ld.loopWarningThreshold:
		return LoopWarning
	default:
		return LoopOK
	}
}

// RecordToolResult records a tool execution result for error rate tracking.
// The recorded history is retained for future error-rate detection; it is not
// yet consulted by any wired check.
func (ld *LoopDetector) RecordToolResult(err bool) {
	ld.mu.Lock()
	defer ld.mu.Unlock()

	ld.errorHistory[ld.errorIdx%len(ld.errorHistory)] = err
	ld.errorIdx++
}

// RecordThinkingDelta accumulates streamed reasoning text and detects when the
// assistant repeats the same line of thought within a turn. It returns
// LoopInterrupt when a significant line repeats beyond the interrupt threshold,
// LoopWarning beyond the warning threshold, and LoopOK otherwise. Complete
// (newline-terminated) lines are evaluated incrementally; short lines are
// ignored to avoid false positives.
func (ld *LoopDetector) RecordThinkingDelta(text string) LoopWarningLevel {
	if text == "" {
		return LoopOK
	}
	ld.mu.Lock()
	defer ld.mu.Unlock()

	ld.thinkPending += text
	for {
		idx := indexByte(ld.thinkPending, '\n')
		if idx < 0 {
			break
		}
		line := trimSpace(ld.thinkPending[:idx])
		ld.thinkPending = ld.thinkPending[idx+1:]
		if len(line) < minThinkLineLen {
			continue
		}
		h := hashInput(line)
		ld.thinkLineCounts[h]++
		if c := ld.thinkLineCounts[h]; c > ld.thinkMaxRepeat {
			ld.thinkMaxRepeat = c
		}
	}

	switch {
	case ld.thinkMaxRepeat >= ld.thinkInterruptThreshold:
		return LoopInterrupt
	case ld.thinkMaxRepeat >= ld.thinkWarningThreshold:
		return LoopWarning
	default:
		return LoopOK
	}
}

// ResetThinking clears the per-turn thinking accumulation so each assistant
// turn is evaluated independently. Called by the AgentManager on turn finalize.
func (ld *LoopDetector) ResetThinking() {
	ld.mu.Lock()
	defer ld.mu.Unlock()
	ld.thinkPending = ""
	ld.thinkLineCounts = make(map[string]int)
	ld.thinkMaxRepeat = 0
}

// indexByte is a small wrapper kept for testability/readability.
func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// trimSpace removes leading/trailing ASCII whitespace. Using a local copy
// avoids importing strings solely for the detector's hot path.
func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && isSpace(s[start]) {
		start++
	}
	for end > start && isSpace(s[end-1]) {
		end--
	}
	return s[start:end]
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\v' || b == '\f'
}

// Reset clears all loop detector state for a new session or turn.
func (ld *LoopDetector) Reset() {
	ld.mu.Lock()
	defer ld.mu.Unlock()

	ld.turnToolCalls = make(map[string]int)
	ld.errorHistory = make([]bool, len(ld.errorHistory))
	ld.errorIdx = 0
	ld.thinkPending = ""
	ld.thinkLineCounts = make(map[string]int)
	ld.thinkMaxRepeat = 0
}

// hashInput creates a deterministic hash of the tool input for loop detection.
func hashInput(input string) string {
	h := sha256.Sum256([]byte(input))
	return fmt.Sprintf("%x", h[:8]) // first 8 hex chars is sufficient
}
