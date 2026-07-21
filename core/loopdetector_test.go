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

	// 1st through 6th calls: OK
	for i := 0; i < 6; i++ {
		if level := ld.RecordToolCall("read", `{"path":"test.txt"}`); level != LoopOK {
			t.Errorf("Call %d: got %d, want LoopOK", i+1, level)
		}
	}

	// 7th call: Warning (threshold = 7)
	if level := ld.RecordToolCall("read", `{"path":"test.txt"}`); level != LoopWarning {
		t.Errorf("Call 7: got %d, want LoopWarning", level)
	}
}

// TestLoopDetectorToolCallInterrupt verifies interrupt at threshold.
func TestLoopDetectorToolCallInterrupt(t *testing.T) {
	ld := NewLoopDetector(DefaultLoopDetectorConfig())

	// Make 9 identical calls
	for i := 0; i < 9; i++ {
		ld.RecordToolCall("bash", `ls`)
	}
	// 10th call: Interrupt
	if level := ld.RecordToolCall("bash", `ls`); level != LoopInterrupt {
		t.Errorf("Call 10: got %d, want LoopInterrupt", level)
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

// TestLoopDetector_LegitRebuildCycleDoesNotTrip replays the false-positive
// incident from bugs.md ("Tool call loop detector"): a long frigolite
// session ran the byte-identical `go build ./...` command 11 times across
// dozens of turns (edit → build → edit → build …) and the lifetime-count
// detector interrupted the agent at the 10th. Legitimate reuse of an
// identical command with other work in between must never trip detection.
func TestLoopDetector_LegitRebuildCycleDoesNotTrip(t *testing.T) {
	ld := NewLoopDetector(DefaultLoopDetectorConfig())
	build := `{"command": "cd /Users/muaddib/dev/frigolite && go build ./... 2>&1", "timeout": 30}`

	// 11 identical builds — MORE than the old interrupt threshold — each
	// separated by different tool calls (the real session's pattern).
	for i := 0; i < 11; i++ {
		if level := ld.RecordToolCall("edit", fmt.Sprintf(`{"path":"f%d.go","old_string":"a","new_string":"b"}`, i)); level != LoopOK {
			t.Fatalf("edit %d: got %d, want LoopOK", i, level)
		}
		if level := ld.RecordToolCall("bash", build); level != LoopOK {
			t.Fatalf("build %d: got %d, want LoopOK (streak broken by the edit)", i, level)
		}
	}
}

// TestLoopDetector_TrueRunawayStillInterrupts proves the streak model keeps
// catching the real signature: the same call repeated back-to-back with
// nothing else in between.
func TestLoopDetector_TrueRunawayStillInterrupts(t *testing.T) {
	ld := NewLoopDetector(DefaultLoopDetectorConfig())
	call := `{"command": "cat output.txt"}`

	for i := 0; i < 9; i++ {
		ld.RecordToolCall("bash", call)
	}
	if level := ld.RecordToolCall("bash", call); level != LoopInterrupt {
		t.Fatalf("10th identical back-to-back call: got %d, want LoopInterrupt", level)
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

// TestLoopDetector_ThinkingLoop_IgnoresStructuralLines ensures that repeated
// code/JSON/XML structural elements (function signatures, keywords, braces)
// do not trigger false positives when the model iterates over code structure.
func TestLoopDetector_ThinkingLoop_IgnoresStructuralLines(t *testing.T) {
	ld := NewLoopDetector(DefaultLoopDetectorConfig())
	structuralLines := []string{
		"func effectiveInline(skill *skills.Skill, cfg *config.Config) bool {\n",
		"if skill.Meta.Category == \"\" {\n",
		"    return false\n",
		"}\n",
		"{\"key\": \"value\"}\n",
		"<xml>data</xml>\n",
	}
	for i := 0; i < 10; i++ {
		for _, line := range structuralLines {
			if lvl := ld.RecordThinkingDelta(line); lvl != LoopOK {
				t.Fatalf("structural line %q triggered %d at iter %d, want LoopOK", line, lvl, i)
			}
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

// sessionTriggerLine is the exact reasoning line that drove the production
// session (goa-export-20260720-232943) to LoopInterrupt: the model recited the
// B-tree cellPointerEnd formula six times while re-deriving leafHasRoom. It is
// a Go short variable declaration and must be treated as structural.
const sessionTriggerLine = "cellPtrEnd := coff + storage.CellPointerOffset + int(page.CellCount)*2 + 2"

// TestLoopDetector_ThinkingLoop_SessionGoDeclIsStructural reproduces the
// invalid-stop from the exported session: a repeated Go `:=` declaration line
// must not trip the thinking-loop detector. Before the isCodeOp fix, the ':'
// branch required a following space, so `:=` was not recognised as an
// assignment and the line was counted (wordCount == minThinkWordCount).
func TestLoopDetector_ThinkingLoop_SessionGoDeclIsStructural(t *testing.T) {
	if !isStructuralLine(sessionTriggerLine) {
		t.Fatalf("session trigger line should be structural: %q", sessionTriggerLine)
	}
	ld := NewLoopDetector(DefaultLoopDetectorConfig())
	// Six occurrences interrupted the production turn; twelve must stay LoopOK.
	for i := 0; i < 12; i++ {
		if lvl := ld.RecordThinkingDelta(sessionTriggerLine + "\n"); lvl != LoopOK {
			t.Fatalf("occurrence %d: got %d, want LoopOK (Go := decl must not count)", i+1, lvl)
		}
	}
}

// TestLoopDetector_ThinkingLoop_NoLatchAfterInterrupt guards the regression
// where a cancelled turn never emitted EventEnd, so the repeat counter stayed
// latched at the interrupt threshold and the *next* turn was killed on its
// first thinking delta (a single "The"). After ResetThinking, fresh deltas
// must not re-trigger.
func TestLoopDetector_ThinkingLoop_NoLatchAfterInterrupt(t *testing.T) {
	ld := NewLoopDetector(DefaultLoopDetectorConfig())

	// Drive the detector to interrupt with a genuine repeated paragraph.
	for i := 0; i < 6; i++ {
		ld.RecordThinkingDelta(longLine + "\n")
	}
	if lvl := ld.RecordThinkingDelta("x"); lvl != LoopInterrupt {
		t.Fatalf("precondition: detector should be latched at interrupt, got %d", lvl)
	}

	// Simulate the manager's interrupt path, which must reset the accumulator.
	ld.ResetThinking()

	// The next turn starts with a single token (production log: thinking: The).
	if lvl := ld.RecordThinkingDelta("The"); lvl != LoopOK {
		t.Fatalf("first delta of new turn re-triggered: got %d, want LoopOK", lvl)
	}
	if lvl := ld.RecordThinkingDelta(" quick sanity check\n"); lvl != LoopOK {
		t.Fatalf("second delta of new turn re-triggered: got %d, want LoopOK", lvl)
	}
}

// TestLoopDetector_ThinkingLoop_LatchDemonstratesBug documents the pre-fix
// failure mode so the latch cannot silently return: without ResetThinking the
// counter stays at the interrupt threshold and any further delta re-triggers,
// even one carrying no newline.
func TestLoopDetector_ThinkingLoop_LatchDemonstratesBug(t *testing.T) {
	ld := NewLoopDetector(DefaultLoopDetectorConfig())
	for i := 0; i < 6; i++ {
		ld.RecordThinkingDelta(longLine + "\n")
	}
	// Without a reset, a bare "The" (no newline, counts nothing new) still
	// reports LoopInterrupt because thinkMaxRepeat is latched.
	if lvl := ld.RecordThinkingDelta("The"); lvl != LoopInterrupt {
		t.Fatalf("expected latched interrupt without reset, got %d", lvl)
	}
}

// TestLoopDetector_ThinkingLoop_MultiLineCodeFenceNotAloop is the regression
// for the false positive in exports goa-export-20260721-142256/142545: the
// model quotes a code block (a SQL-parser snippet) inside a ``` fence while
// reasoning iteratively. The code lines repeat several times across the turn
// (the model re-quotes its own edit), but that is quoted artifact content,
// not model repetition — the detector must track the fence across lines and
// skip everything inside it, so the agent is NOT killed.
func TestLoopDetector_ThinkingLoop_MultiLineCodeFenceNotAloop(t *testing.T) {
	ld := NewLoopDetector(DefaultLoopDetectorConfig()) // interrupt at 6

	codeLine1 := `        if p.cur.Type == TokenKeyword && (p.cur.Value == "PRIMARY" || p.cur.Value == "UNIQUE" ||`
	codeLine2 := `            p.cur.Value == "CHECK" || p.cur.Value == "FOREIGN" || p.cur.Value == "CONSTRAINT") {`
	prose := `Let me check what skipTableConstraints looks like and why it does not consume the token.`

	// Reason iteratively: prose, then a fenced code quote, more prose, then the
	// same fenced quote again — nine total repetitions of the code lines, all
	// inside fences. Must never trip.
	for i := 0; i < 5; i++ {
		block := prose + "\n```go\n" + codeLine1 + "\n" + codeLine2 + "\n```\n"
		if lvl := ld.RecordThinkingDelta(block); lvl != LoopOK {
			t.Fatalf("fenced code quote triggered %d at iter %d, want LoopOK (quoted code is not model repetition)", lvl, i)
		}
	}
}

// TestLoopDetector_ThinkingLoop_UnterminatedFenceAcrossDeltas covers the
// streaming case: a code fence opened in one delta and closed in a later one
// must still suppress the code lines (fence state tracked across deltas).
func TestLoopDetector_ThinkingLoop_UnterminatedFenceAcrossDeltas(t *testing.T) {
	ld := NewLoopDetector(DefaultLoopDetectorConfig())
	codeLine := `            p.cur.Value == "CHECK" || p.cur.Value == "FOREIGN" || p.cur.Value == "CONSTRAINT") {`

	// Open the fence, then feed the code line several times across deltas
	// before the fence ever closes — all inside the (still open) block.
	ld.RecordThinkingDelta("Let me look at the parser code:\n```go\n")
	for i := 0; i < 8; i++ {
		if lvl := ld.RecordThinkingDelta(codeLine + "\n"); lvl != LoopOK {
			t.Fatalf("code line inside open fence triggered %d at iter %d, want LoopOK", lvl, i)
		}
	}
	ld.RecordThinkingDelta("```\nThat is the snippet. Now let me reason about why it fails here.\n")
}

// TestLoopDetector_ThinkingLoop_ProseRepetitionStillDetected ensures the
// fence fix does not weaken genuine repetition: the same prose sentence
// repeated past the interrupt threshold must still kill the turn.
func TestLoopDetector_ThinkingLoop_ProseRepetitionStillDetected(t *testing.T) {
	ld := NewLoopDetector(DefaultLoopDetectorConfig())
	prose := "The constraint keyword is not being consumed by the constraint loop."
	for i := 0; i < 5; i++ {
		ld.RecordThinkingDelta(prose + "\n")
	}
	if lvl := ld.RecordThinkingDelta(prose + "\n"); lvl != LoopInterrupt {
		t.Fatalf("genuine prose repetition: got %d, want LoopInterrupt", lvl)
	}
}
