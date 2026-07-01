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
// Only tool-call repeat detection is wired into the AgentManager today. Earlier
// revisions advertised token-budget, error-rate, activity-timeout, and
// conversational-loop detection, but those code paths were never invoked at
// runtime — giving a false sense of safety. They have been removed (along with
// their config fields) so the surface area reflects reality. See STUB-1/BUG-11.
type LoopDetector struct {
	mu sync.Mutex

	// Tool call tracking — drives RecordToolCall loop detection.
	turnToolCalls          map[string]int // key: toolName+hash(input) → count
	loopWarningThreshold   int            // same tool call count before warning
	loopInterruptThreshold int            // same tool call count before interrupt

	// Error tracking (ring buffer). Populated by RecordToolResult; retained as
	// the integration point for a future (genuinely wired) error-rate check.
	errorHistory []bool // last N tool results (true = error)
	errorIdx     int
}

// LoopDetectorConfig holds configurable parameters for the loop detector.
// Only the tool-repeat thresholds are used; the unused token/error/activity
// fields were removed when their dead detection paths were deleted (STUB-1).
type LoopDetectorConfig struct {
	LoopWarning   int
	LoopInterrupt int
}

// DefaultLoopDetectorConfig returns sensible defaults for the loop detector.
func DefaultLoopDetectorConfig() LoopDetectorConfig {
	return LoopDetectorConfig{
		LoopWarning:   3,
		LoopInterrupt: 5,
	}
}

const loopErrorHistorySize = 10

// NewLoopDetector creates a loop detector with the given config.
func NewLoopDetector(cfg LoopDetectorConfig) *LoopDetector {
	return &LoopDetector{
		turnToolCalls:          make(map[string]int),
		errorHistory:           make([]bool, loopErrorHistorySize),
		loopWarningThreshold:   cfg.LoopWarning,
		loopInterruptThreshold: cfg.LoopInterrupt,
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

// Reset clears all loop detector state for a new session or turn.
func (ld *LoopDetector) Reset() {
	ld.mu.Lock()
	defer ld.mu.Unlock()

	ld.turnToolCalls = make(map[string]int)
	ld.errorHistory = make([]bool, len(ld.errorHistory))
	ld.errorIdx = 0
}

// hashInput creates a deterministic hash of the tool input for loop detection.
func hashInput(input string) string {
	h := sha256.Sum256([]byte(input))
	return fmt.Sprintf("%x", h[:8]) // first 8 hex chars is sufficient
}
