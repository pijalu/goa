// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"fmt"
	"testing"
)

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

// longLine is a representative reasoning paragraph (well over minThinkLineLen)
// mimicking the failure captured in the bug report: the assistant re-emits the
// same block of reasoning many times during a single streaming turn.
const longLine = "I can see the main.ts files are very similar. The pbl version has additional imports from SDK runtime."

// TestLoopDetector_ThinkingLoop_DetectsRepeatedParagraph streams the same
// reasoning line repeatedly and expects warning then interrupt.
func TestLoopDetector_ThinkingLoop_DetectsRepeatedParagraph(t *testing.T) {
	ld := NewLoopDetector(DefaultLoopDetectorConfig()) // warn 4 / interrupt 6

	// Feed the same line 3 times (each on its own line): still OK.
	for i := 0; i < 3; i++ {
		if lvl := ld.RecordThinkingDelta(longLine + "\n"); lvl != LoopOK {
			t.Fatalf("call %d: got %d, want LoopOK", i, lvl)
		}
	}
	// 4th occurrence: warning.
	if lvl := ld.RecordThinkingDelta(longLine + "\n"); lvl != LoopWarning {
		t.Fatalf("call 4: got %d, want LoopWarning", lvl)
	}
	// 6th occurrence: interrupt.
	for i := 0; i < 1; i++ {
		ld.RecordThinkingDelta(longLine + "\n")
	}
	if lvl := ld.RecordThinkingDelta(longLine + "\n"); lvl != LoopInterrupt {
		t.Fatalf("call 6: got %d, want LoopInterrupt", lvl)
	}
}

// TestLoopDetector_ThinkingLoop_IgnoresShortLines ensures short repeated
// lines (bullets/separators) do not trigger false positives.
func TestLoopDetector_ThinkingLoop_IgnoresShortLines(t *testing.T) {
	ld := NewLoopDetector(DefaultLoopDetectorConfig())
	for i := 0; i < 20; i++ {
		if lvl := ld.RecordThinkingDelta("- yes\n"); lvl != LoopOK {
			t.Fatalf("short repeated line triggered %d at iter %d, want LoopOK", lvl, i)
		}
	}
}

// TestLoopDetector_ThinkingLoop_StreamedAcrossDeltas verifies detection works
// when a single line arrives split across several deltas (no newline until the
// end) and when distinct reasoning lines do not accumulate.
func TestLoopDetector_ThinkingLoop_StreamedAcrossDeltas(t *testing.T) {
	ld := NewLoopDetector(DefaultLoopDetectorConfig())

	// Distinct, non-repeating lines must never warn.
	for i := 0; i < 10; i++ {
		line := fmt.Sprintf("Reasoning about a genuinely different topic number %d that is long enough to count here.", i)
		if lvl := ld.RecordThinkingDelta(line + "\n"); lvl != LoopOK {
			t.Fatalf("distinct line %d: got %d, want LoopOK", i, lvl)
		}
	}
}

// TestLoopDetector_ResetThinking_ClearsAccumulation confirms that resetting
// between turns lets the same reasoning recur on a later turn without firing.
func TestLoopDetector_ResetThinking_ClearsAccumulation(t *testing.T) {
	ld := NewLoopDetector(DefaultLoopDetectorConfig())
	for i := 0; i < 5; i++ {
		ld.RecordThinkingDelta(longLine + "\n")
	}
	ld.ResetThinking()
	// Same line again, a few times, must start from zero.
	for i := 0; i < 3; i++ {
		if lvl := ld.RecordThinkingDelta(longLine + "\n"); lvl != LoopOK {
			t.Fatalf("after reset, call %d: got %d, want LoopOK", i, lvl)
		}
	}
}
