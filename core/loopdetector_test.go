// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import "testing"

// TestLoopDetectorToolCallWarning verifies warning at threshold.
func TestLoopDetectorToolCallWarning(t *testing.T) {
	ld := NewLoopDetector(DefaultLoopDetectorConfig())

	// 1st call: OK
	if level := ld.RecordToolCall("read", `{"path":"test.txt"}`); level != LoopOK {
		t.Errorf("Call 1: got %d, want LoopOK", level)
	}

	// 2nd call: OK
	if level := ld.RecordToolCall("read", `{"path":"test.txt"}`); level != LoopOK {
		t.Errorf("Call 2: got %d, want LoopOK", level)
	}

	// 3rd call: Warning (threshold = 3)
	if level := ld.RecordToolCall("read", `{"path":"test.txt"}`); level != LoopWarning {
		t.Errorf("Call 3: got %d, want LoopWarning", level)
	}
}

// TestLoopDetectorToolCallInterrupt verifies interrupt at threshold.
func TestLoopDetectorToolCallInterrupt(t *testing.T) {
	ld := NewLoopDetector(DefaultLoopDetectorConfig())

	// Make 5 identical calls
	for i := 0; i < 4; i++ {
		ld.RecordToolCall("bash", `ls`)
	}
	// 5th call: Interrupt
	if level := ld.RecordToolCall("bash", `ls`); level != LoopInterrupt {
		t.Errorf("Call 5: got %d, want LoopInterrupt", level)
	}
}

// TestLoopDetectorDifferentToolsReset verifies different tools don't accumulate.
func TestLoopDetectorDifferentToolsReset(t *testing.T) {
	ld := NewLoopDetector(DefaultLoopDetectorConfig())

	for i := 0; i < 3; i++ {
		ld.RecordToolCall("read", `{"path":"a.txt"}`)
	}
	// Different tool should not trigger warning
	if level := ld.RecordToolCall("bash", `ls`); level != LoopOK {
		t.Errorf("Different tool: got %d, want LoopOK", level)
	}
	// Same tool with different input should not trigger warning
	if level := ld.RecordToolCall("read", `{"path":"b.txt"}`); level != LoopOK {
		t.Errorf("Different input: got %d, want LoopOK", level)
	}
}

// TestLoopDetectorReset verifies tool-call state is cleared.
func TestLoopDetectorReset(t *testing.T) {
	ld := NewLoopDetector(DefaultLoopDetectorConfig())

	ld.RecordToolCall("bash", `ls`)
	ld.RecordToolResult(true)
	ld.Reset()

	// After reset, tool-call tracking starts fresh.
	if level := ld.RecordToolCall("bash", `ls`); level != LoopOK {
		t.Errorf("After reset: got %d, want LoopOK", level)
	}
}
