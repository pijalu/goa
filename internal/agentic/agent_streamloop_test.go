// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// Regression tests for the stream-loop false positive (session export
// 2026-07-31): a debugging turn was killed mid-analysis because the model
// pasted two near-identical SQL statements (test "3.4" vs "3.5" — same query
// shape, different constant) next to each other in its thinking. After
// normalization, the fuzzy matcher saw a "92-byte suffix repeated 2 times"
// and stopped a legitimate turn. Two merely-similar copies must never count
// as a loop; only byte-exact long blocks (2x) or fuzzy 3x+ may trigger.

// The exact adjacent evidence pair from the incident's thinking buffer.
const streamLoopFPEvidence = "So the failure at line 196 is the \"3.5\" query (a=5), NOT \"3.4\" (a=4). " +
	"Both are EXPLAIN QUERY PLAN with inline comments.\n" +
	"3.4: a = 4 AND b BETWEEN 20 AND 80 -- Matches 80 rows\n AND\n c BETWEEN 150 AND 160 -- Matches 10 rows\n" +
	"3.5: `a = 5 AND b BETWEEN 20 AND 80 -- Matches 1 row\n AND\n c BETWEEN 150 AND 160 -- Matches 10 rows"

func TestStreamLoop_NoFalsePositiveOnQuotedEvidence(t *testing.T) {
	if streamLoopWouldDetect(streamLoopFPEvidence, 5) {
		t.Error("false positive: two near-identical quoted SQL statements detected as a loop")
	}

	// Stream the evidence in token-sized fragments exactly as handleTextDelta
	// does: no prefix of the buffer may trigger either.
	var buf strings.Builder
	const fragSize = 9
	for pos := 0; pos < len(streamLoopFPEvidence); pos += fragSize {
		end := pos + fragSize
		if end > len(streamLoopFPEvidence) {
			end = len(streamLoopFPEvidence)
		}
		buf.WriteString(streamLoopFPEvidence[pos:end])
		if streamLoopWouldDetect(buf.String(), 5) {
			t.Fatalf("false positive mid-stream at byte %d of quoted evidence", end)
		}
	}
}

// The repeat count is the only loop signal: below the configured threshold
// nothing fires, at the threshold the turn stops.
func TestStreamLoop_ThresholdControlsDetection(t *testing.T) {
	para := "The quick brown fox jumps over the lazy dog while the diligent tester watches the whole test suite fail"

	twoCopies := "Intro sentence. " + para + " " + para
	if streamLoopWouldDetect(twoCopies, 5) {
		t.Error("2 copies must not trigger the default threshold of 5")
	}
	if !streamLoopWouldDetect(twoCopies, 2) {
		t.Error("2 copies must trigger a user-configured threshold of 2")
	}

	fourCopies := "Intro sentence. " + strings.Repeat(para+" ", 4)
	if streamLoopWouldDetect(fourCopies, 5) {
		t.Error("4 copies must not trigger the default threshold of 5")
	}
	fiveCopies := fourCopies + para
	if !streamLoopWouldDetect(fiveCopies, 5) {
		t.Error("5 copies must trigger the default threshold of 5")
	}
}

// Three fuzzy copies of a long paragraph (small same-length variations, so no
// two adjacent copies are byte-exact) confirm a loop — the count, not the
// similarity, is the evidence.
func TestStreamLoop_FuzzyTripleLongBlockStillDetected(t *testing.T) {
	para := "The project builds cleanly. Let me summarize every update I made to the handover document for the team:"
	copies := []string{
		para,
		strings.Replace(para, "builds", "bullds", 1),   // 1-byte variation
		strings.Replace(para, "handover", "handovar", 1), // 1-byte variation
	}
	// Lead-in text mirrors real streamed answers and lets the window align to
	// copy boundaries (3 bare copies alone are one byte short of 3 windows).
	text := "Analyzing the failure now. " + strings.Join(copies, "\n\n")
	if !streamLoopWouldDetect(text, 3) {
		t.Error("fuzzy 3x long-block loop not detected")
	}
}

// Two fuzzy copies must NOT trigger even at the most aggressive threshold —
// that was the incident.
func TestStreamLoop_FuzzyPairLongBlockNotDetected(t *testing.T) {
	para := "The project builds cleanly. Let me summarize every update I made to the handover document for the team:"
	other := strings.Replace(para, "builds", "compiles", 1)
	text := "Analyzing now. " + para + "\n\n" + other
	for _, threshold := range []int{2, 3, 5} {
		if streamLoopWouldDetect(text, threshold) {
			t.Errorf("false positive at threshold %d: two similar long paragraphs detected as a loop", threshold)
		}
	}
}

// The detection can be disabled (session temp override or persisted config)
// via Config.StreamLoopDisabled; it is consulted per delta.
func TestCheckStreamLoop_DisabledByConfig(t *testing.T) {
	para := "The quick brown fox jumps over the lazy dog while the diligent tester watches the whole test suite fail"
	loopText := strings.Repeat(para+" ", 6)

	disabled := true
	a := NewAgent(Config{
		Model:              testModel(provider.ApiOpenAICompletions),
		StreamLoopDisabled: func() bool { return disabled },
	})
	a.checkStreamLoop(loopText)
	if a.streamLoopDetected {
		t.Error("streamLoopDetected set while detection disabled")
	}

	// Re-enable mid-stream (temp toggle back on): the same text must trigger.
	disabled = false
	a.checkStreamLoop(loopText)
	if !a.streamLoopDetected {
		t.Error("streamLoopDetected not set after re-enabling detection")
	}
}

// A nil StreamLoopDisabled hook means detection is enabled (zero config), and
// a nil StreamLoopMaxRepeats hook means the default threshold of 5.
func TestCheckStreamLoop_NilHookMeansEnabled(t *testing.T) {
	para := "The quick brown fox jumps over the lazy dog while the diligent tester watches the whole test suite fail"
	a := NewAgent(Config{Model: testModel(provider.ApiOpenAICompletions)})

	a.checkStreamLoop(strings.Repeat(para+" ", 4))
	if a.streamLoopDetected {
		t.Error("4 copies must not trigger the default threshold of 5")
	}
	a.checkStreamLoop(strings.Repeat(para+" ", 6))
	if !a.streamLoopDetected {
		t.Error("6 copies must trigger the default threshold of 5")
	}
}

// The StreamLoopMaxRepeats hook drives the threshold live.
func TestCheckStreamLoop_MaxRepeatsHook(t *testing.T) {
	para := "The quick brown fox jumps over the lazy dog while the diligent tester watches the whole test suite fail"
	threshold := 8
	a := NewAgent(Config{
		Model:                testModel(provider.ApiOpenAICompletions),
		StreamLoopMaxRepeats: func() int { return threshold },
	})
	a.checkStreamLoop(strings.Repeat(para+" ", 6))
	if a.streamLoopDetected {
		t.Error("6 copies must not trigger a configured threshold of 8")
	}
	threshold = 3
	a.checkStreamLoop(strings.Repeat(para+" ", 4))
	if !a.streamLoopDetected {
		t.Error("4 copies must trigger a reconfigured threshold of 3")
	}
}
