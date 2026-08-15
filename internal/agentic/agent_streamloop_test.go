// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

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

// Field fixtures for the count-based detector rework (2026-08-01):
// a false positive on exploratory Option A/B/C thinking, and a false negative
// on a ~90-copy paraphrase loop.

// streamLoopFPOptions is the exploratory-thinking excerpt that produced the
// false positive: parallel-phrased, semantically distinct options with ZERO
// repeated blocks. It must never trip the detector.
const streamLoopFPOptions = "The Prepare path parses SQL and returns statements. To store the ORIGINAL SQL text, I need to carry it to execCreateTable. Options:\n" +
	"Option A: Add a field to sql.CreateTableStmt like RawSQL string — the LALR parser could capture the raw text IF it knows the input. The LALR parser's ParseSQL has the input string. I could capture the raw statement text... but the parse happens per-token; capturing the raw span of the CREATE statement requires position tracking.\n" +
	"Hmm — actually! The tokens have Pos. The parser processes the input with tok.Next(). For a CREATE TABLE statement, I could record the input position of the first token (CREATE) and the last token (RParen/SEMI) — but the rule handlers don't have direct access to the full input...\n" +
	"Actually ParseSQL DOES have the input and the parser state. Let me look at whether there's a simpler way.\n" +
	"Option B: In the ENGINE's Prepare, split the raw SQL by statements (using parse to find boundaries) and attach the raw text to each statement. That's the \"carry raw SQL\" approach: after parse.ParseSQL(sqlStr), map statements back to their raw text. But statements don't carry positions either...\n" +
	"Wait — actually, ParseSQL's stmts are collected at ecmd rules. Could I ALSO capture the raw text per statement? The parser has access to the input via... hmm, the Parser doesn't hold the input. ParseSQL does.\n" +
	"Option C: The pragmatic one — implement the constraint rules in the LALR parser. It's more code but self-contained. Actually, let me reconsider: what does buildCreateTableSQL need to produce? It serializes CREATE TABLE name (cols). The cols come from s.Columns ([]ColumnDef)."

// streamLoopTPParaphrase is the missed real loop: the same short intent
// re-paraphrased every copy (~90 in the incident, trimmed here). No long
// byte-exact block exists — detection requires the shingle-coverage path.
const streamLoopTPParaphrase = "No test.db cleanup in the preamble. Let me check how widespread the sqlite3 db test.db reopen pattern is in Tier-1, and how processDB handles close:" +
	"Let me check how widespread the reopen pattern is in Tier-1 tests and how processDB handles close:" +
	"Let me check the reopen pattern usage in Tier-1 and processDB's close handling:" +
	"Let me check how widespread the sqlite3 db test.db pattern is:" +
	"Let me check how many Tier-1 tests use the reopen pattern:" +
	"Let me check the reopen pattern in Tier-1 tests:" +
	"Let me check processDB's close handling and the reopen pattern usage:" +
	"Let me check how processDB handles close:" +
	"Let me check how many Tier-1 tests use the reopen pattern:" +
	"Let me check the reopen usage in Tier-1 tests:" +
	"Let me check processDB's \"close\" handling:" +
	"Let me check how many Tier-1 tests reopen db:" +
	"Let me check the reopen pattern:" +
	"Let me check how processDB handles close:" +
	"Let me check the reopen pattern in Tier-1 tests:" +
	"Let me check how processDB handles close and Tier-1 usage:" +
	"Let me check the reopen pattern:"

// TestStreamLoop_NoFalsePositiveOnExploratoryOptions is the FP regression
// from the field: Option A/B/C analysis must never trip.
func TestStreamLoop_NoFalsePositiveOnExploratoryOptions(t *testing.T) {
	if streamLoopWouldDetect(streamLoopFPOptions, 3) {
		t.Error("false positive: exploratory Option A/B/C analysis detected as a loop")
	}
	// No prefix may trip either (the detector runs per delta).
	var buf strings.Builder
	const fragSize = 9
	for pos := 0; pos < len(streamLoopFPOptions); pos += fragSize {
		end := pos + fragSize
		if end > len(streamLoopFPOptions) {
			end = len(streamLoopFPOptions)
		}
		buf.WriteString(streamLoopFPOptions[pos:end])
		if streamLoopWouldDetect(buf.String(), 3) {
			t.Fatalf("false positive mid-stream at byte %d of exploratory options", end)
		}
	}
}

// TestStreamLoop_ParaphraseLoopDetected is the TP regression from the field
// a high-count paraphrase loop MUST trip, even mid-stream.
func TestStreamLoop_ParaphraseLoopDetected(t *testing.T) {
	if !streamLoopWouldDetect(streamLoopTPParaphrase, 3) {
		t.Error("paraphrase loop not detected: ~13 drifting copies of the same intent")
	}
	// It must not trip on the first few copies (analysis), but must trip by
	// the time the paraphrase storm is underway.
	var buf strings.Builder
	firstTrip := -1
	const fragSize = 31
	for pos := 0; pos < len(streamLoopTPParaphrase); pos += fragSize {
		end := pos + fragSize
		if end > len(streamLoopTPParaphrase) {
			end = len(streamLoopTPParaphrase)
		}
		buf.WriteString(streamLoopTPParaphrase[pos:end])
		if streamLoopWouldDetect(buf.String(), 3) {
			firstTrip = end
			break
		}
	}
	if firstTrip < 0 {
		t.Fatal("paraphrase loop never detected mid-stream")
	}
	t.Logf("paraphrase loop first detected at byte %d of %d", firstTrip, len(streamLoopTPParaphrase))
}

// TestStreamLoop_ExactChainRules covers Detector A: byte-exact repeats.
func TestStreamLoop_ExactChainRules(t *testing.T) {
	// Varied-word blocks: no internal self-similarity, so Detector B has no
	// opinion on pairs and Detector A's exact-chain rules decide alone.
	block200 := ("the parser processes each token in sequence while the engine evaluates constraints " +
		"and the planner rewrites the query tree into an execution pipeline over indexed storage " +
		"layers with buffered iterators and spill files across remote shards")[:200]
	block1k := block200 + " — second stanza with different vocabulary: " + strings.Repeat("mango telescope verdict umbrella glacier walnut ", 20)
	block1k = block1k[:1024]

	t.Run("three exact 200-byte copies trip", func(t *testing.T) {
		text := "lead in. " + block200 + block200 + block200
		if !streamLoopWouldDetect(text, 3) {
			t.Error("3x byte-exact 200-byte block not detected")
		}
	})
	t.Run("two exact 200-byte copies do not trip", func(t *testing.T) {
		text := "lead in. " + block200 + block200
		if streamLoopWouldDetect(text, 3) {
			t.Error("2x 200-byte block must not trip (needs 3 copies)")
		}
	})
	t.Run("two exact 1KB copies trip", func(t *testing.T) {
		text := "lead in. " + block1k + block1k
		if !streamLoopWouldDetect(text, 3) {
			t.Error("2x byte-exact 1KB block not detected (long-block rule)")
		}
	})
	t.Run("exact copies with small interlude trip", func(t *testing.T) {
		junk := " wait a moment here "
		text := "lead in. " + block200 + junk + block200 + junk + block200
		if !streamLoopWouldDetect(text, 3) {
			t.Error("exact copies separated by small interludes not detected")
		}
	})
	t.Run("connector noise does not trip", func(t *testing.T) {
		text := strings.Repeat("the ", 30)
		if streamLoopWouldDetect(text, 3) {
			t.Error("single-word connector noise must not trip")
		}
	})
}

// TestStreamLoop_NoFalsePositiveOnTopicalRepetition: repeating one TERM is
// topical emphasis, not a loop — coverage and distinct-hot-shingle guards.
func TestStreamLoop_NoFalsePositiveOnTopicalRepetition(t *testing.T) {
	// Ten genuinely different sentences that each mention the goal tool once.
	sentences := []string{
		"The goal tool returned an error on the first attempt.",
		"Debugging took a while because the goal tool output was truncated.",
		"After lunch I wired the goal tool into the regression harness.",
		"Nobody reviewed what the goal tool printed yesterday.",
		"Our docs explain when the goal tool should be preferred.",
		"A timeout made the goal tool look broken, but it was the network.",
		"She benchmarked the goal tool against three alternatives.",
		"Next sprint we might retire the goal tool entirely.",
		"The migration guide mentions the goal tool only once.",
		"Finally, the goal tool succeeded after the config fix.",
	}
	if streamLoopWouldDetect(strings.Join(sentences, " "), 3) {
		t.Error("false positive: topical repetition of one term detected as a loop")
	}
}

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
	if streamLoopWouldDetect(twoCopies, 2) {
		t.Error("2 sub-kilobyte copies must not trigger even at threshold 2: quoted-evidence protection — pairs only count at ≥ 1 KB")
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
// two adjacent copies are byte-exact) must NOT trip: three similar paragraphs
// can be analysis (the Option A/B/C false positive, 2026-08-01) — the
// copy count, not the similarity, is the evidence. When the family keeps
// growing, Detector B's shingle coverage confirms the loop.
func TestStreamLoop_FuzzyCopiesNeedHighCount(t *testing.T) {
	para := "The project builds cleanly. Let me summarize every update I made to the handover document for the team:"
	fuzz := []string{
		para,
		strings.Replace(para, "builds", "bullds", 1),      // 1-byte variation
		strings.Replace(para, "handover", "handovar", 1),  // 1-byte variation
		strings.Replace(para, "summarize", "sumarize", 1), // 1-byte variation
		strings.Replace(para, "team", "teem", 1),
		strings.Replace(para, "cleanly", "cleanli", 1),
	}
	text := "Analyzing the failure now. " + strings.Join(fuzz[:3], "\n\n")
	if streamLoopWouldDetect(text, 3) {
		t.Error("false positive: three fuzzy paragraphs detected as a loop (analysis territory)")
	}
	text = "Analyzing the failure now. " + strings.Join(fuzz, "\n\n")
	if !streamLoopWouldDetect(text, 3) {
		t.Error("six drifting copies of the same paragraph not detected")
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

// Regression test for the enumerated-list false positive (session export
// 2026-07-31, goa-export-20260731-224934.zip): a planning turn was killed
// because the model drafted a verifyCommand enumerating test packages —
// ./testgen/select3/ ./testgen/select4/ … ./testgen/selectB/. After
// normalization each ~15-byte item differs from the next only in the
// incrementing digit/letter, and the fuzzy matcher called 5 adjacent
// 30-byte windows a runaway loop. An enumerated sequence is not a loop:
// every element differs systematically (a walking counter).
const streamLoopFPEnumeration = "Goal 7: Tier 1 completion sweep objective: \"Complete Tier 1: fix all " +
	"remaining Tier 1 packages not covered by earlier goals.\" verifyCommand: \"go test -tags testgen " +
	"./testgen/select1/ ./testgen/select2/ ./testgen/insert/ ./testgen/delete/ ./testgen/update/ " +
	"./testgen/null/ ./testgen/affinity/ ./testgen/expr/ ./testgen/types/ ./testgen/cast/ " +
	"./testgen/between/ ./testgen/coalesce/ ./testgen/literal/ ./testgen/istrue/ ./testgen/numcast/ " +
	"./testgen/subtype/ ./testgen/strict/ ./testgen/intpkey/ ./testgen/intreal/ ./testgen/nulls/ " +
	"./testgen/select3/ ./testgen/select4/ ./testgen/select5/ ./testgen/select6/ ./testgen/select7/ " +
	"./testgen/select8/ ./testgen/select9/ ./testgen/selectA/ ./testgen/selectB/ ./testgen/selectC/ " +
	"./testgen/selectD/ ./testgen/selectE/ ./testgen/selectF/ ./testgen/selectG/ ./testgen/selectH/\""

func TestStreamLoop_NoFalsePositiveOnEnumeratedLists(t *testing.T) {
	for _, threshold := range []int{2, 3, 5} {
		if streamLoopWouldDetect(streamLoopFPEnumeration, threshold) {
			t.Errorf("false positive at threshold %d: enumerated package list detected as a loop", threshold)
		}
	}

	// Stream the enumeration in token-sized fragments: no prefix of the
	// buffer may trigger either (the incident fired mid-stream).
	var buf strings.Builder
	const fragSize = 9
	for pos := 0; pos < len(streamLoopFPEnumeration); pos += fragSize {
		end := pos + fragSize
		if end > len(streamLoopFPEnumeration) {
			end = len(streamLoopFPEnumeration)
		}
		buf.WriteString(streamLoopFPEnumeration[pos:end])
		if streamLoopWouldDetect(buf.String(), 5) {
			t.Fatalf("false positive mid-stream at byte %d of enumerated list", end)
		}
	}

	// Additional enumeration shapes that must never count as loops.
	moreEnumerations := []string{
		"ports 8001 8002 8003 8004 8005 8006 8007 are all in use by the dev server",
		"versions v1.2.0 v1.3.0 v1.4.0 v1.5.0 v1.6.0 v1.7.0 all fail the same test",
		"rows row_a1 row_a2 row_a3 row_a4 row_a5 row_a6 row_a7 were skipped by the migration",
	}
	for _, text := range moreEnumerations {
		for _, threshold := range []int{3, 5} {
			if streamLoopWouldDetect(text, threshold) {
				t.Errorf("false positive at threshold %d on enumeration %q", threshold, text)
			}
		}
	}
}

// Regression test for the long-period false negative (same-day field
// report): the model looped a ~230-byte paragraph 14 times and the detector
// never fired because its window was hard-capped at 120 bytes — the loop's
// period was longer than the cap. The scan must cover periods up to
// len(buffer)/maxRepeats.
const streamLoopLongPeriodUnit = "There are TWO parsers:\n" +
	"1. internal/parse/parser.go — the go-lemon LALR(1) parser (primary)\n" +
	"2. The recursive-descent parser in internal/sql/parser.go\n" +
	"Let me look at internal/parse/parser.go line 83-112 to understand the flow — it seems there's a fallback to the RD parser (rdParser.Parse()):"

func TestStreamLoop_LongPeriodLoopDetected(t *testing.T) {
	// The incident unit is ~230 bytes after normalization — beyond the old
	// 120-byte window cap.
	if n := len(streamLoopNormalize(streamLoopLongPeriodUnit)); n <= 120 {
		t.Fatalf("fixture unit is %d normalized bytes, want > 120 to reproduce the incident", n)
	}

	looped := "Intro. " + strings.Repeat(streamLoopLongPeriodUnit+"\n", 6)
	if !streamLoopWouldDetect(looped, 5) {
		t.Error("long-period loop not detected: ~230-byte unit repeated 6 times escaped the scan")
	}

	// Streamed in token-sized fragments, detection must fire before the
	// seventh copy completes.
	var buf strings.Builder
	const fragSize = 9
	detectedAt := -1
	for pos := 0; pos < len(looped); pos += fragSize {
		end := pos + fragSize
		if end > len(looped) {
			end = len(looped)
		}
		buf.WriteString(looped[pos:end])
		if streamLoopWouldDetect(buf.String(), 5) {
			detectedAt = end
			break
		}
	}
	if detectedAt < 0 {
		t.Fatal("long-period loop not detected with token-sized deltas")
	}
	sixCopiesEnd := len("Intro. ") + 6*(len(streamLoopLongPeriodUnit)+1)
	if detectedAt > sixCopiesEnd {
		t.Errorf("loop detected too late: fired at byte %d, want by end of sixth copy (%d)", detectedAt, sixCopiesEnd)
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

// The StreamLoopMinPeriod hook drives Detector A's minimum repeat-unit
// length live: a 53-char unit (52 chars + joining space) repeated 3x trips
// at the default floor of 50 but not at a reconfigured floor of 60.
func TestCheckStreamLoop_MinPeriodHook(t *testing.T) {
	unit := "the quick brown fox jumps over the lazy dog while go" // 52 chars
	if len(unit) != 52 {
		t.Fatalf("fixture unit must be 52 chars, got %d", len(unit))
	}
	text := unit + strings.Repeat(" "+unit, 5) // 6 copies: above the default maxRepeats of 5

	minPeriod := 60
	a := NewAgent(Config{
		Model:               testModel(provider.ApiOpenAICompletions),
		StreamLoopMinPeriod: func() int { return minPeriod },
	})
	a.checkStreamLoop(text)
	if a.streamLoopDetected {
		t.Error("53-char period must not trip a min period of 60")
	}
	minPeriod = 50
	a.checkStreamLoop(text)
	if !a.streamLoopDetected {
		t.Error("53-char period must trip a min period of 50")
	}
}

// TestCheckStreamLoop_MinPeriodDefaults: nil hook and out-of-range hook
// values fall back to the built-in default of 50.
func TestCheckStreamLoop_MinPeriodDefaults(t *testing.T) {
	a := NewAgent(Config{Model: testModel(provider.ApiOpenAICompletions)})
	if got := a.streamLoopMinPeriod(); got != streamLoopExactMinPeriod {
		t.Errorf("nil hook min period = %d, want default %d", got, streamLoopExactMinPeriod)
	}
	a.cfg.StreamLoopMinPeriod = func() int { return 0 }
	if got := a.streamLoopMinPeriod(); got != streamLoopExactMinPeriod {
		t.Errorf("0 hook min period = %d, want default %d", got, streamLoopExactMinPeriod)
	}
	a.cfg.StreamLoopMinPeriod = func() int { return 3 }
	if got := a.streamLoopMinPeriod(); got != streamLoopExactMinPeriod {
		t.Errorf("below-floor hook min period = %d, want default %d", got, streamLoopExactMinPeriod)
	}
	a.cfg.StreamLoopMinPeriod = func() int { return 42 }
	if got := a.streamLoopMinPeriod(); got != 42 {
		t.Errorf("hook min period = %d, want 42", got)
	}
}

// TestStreamLoopScan_MinPeriodBelowFloor: a genuine micro unit (period 28,
// distinct words) with only 4 copies is below Detector A's certainty bar at
// every floor — periods ≥ minPeriod need max(maxRepeats, 3) copies, and the
// configured floor only re-bands small periods, never large trailing
// repeats (3 chained copies of any trailing 50 chars always fire).
func TestStreamLoopScan_MinPeriodBelowFloor(t *testing.T) {
	unit := distinctWordUnit(t, 24)
	period := len(unit) + 1
	text := streamLoopNormalize(unit + strings.Repeat(" "+unit, 3)) // 4 copies
	if _, _, _, ok := streamLoopScan(text, 5, 50); ok {
		t.Errorf("%d-char micro period x4 must not trip at maxRepeats 5", period)
	}
	// Lowering the floor does not rescue it either: 4 < 5 required copies.
	if _, _, _, ok := streamLoopScan(text, 5, 20); ok {
		t.Errorf("%d-char micro period x4 must not trip at maxRepeats 5 even at min period 20", period)
	}
	// But with 6 copies the lowered floor fires (6 >= 5 required).
	text6 := streamLoopNormalize(unit + strings.Repeat(" "+unit, 5))
	if _, _, _, ok := streamLoopScan(text6, 5, 20); !ok {
		t.Errorf("%d-char period x6 must trip at maxRepeats 5 with min period 20", period)
	}
}

// distinctWordUnit builds a space-joined unit of at least minLen chars from
// pairwise-distinct words, so the only exact repeats in the joined copies
// are the full-unit period and its multiples.
func distinctWordUnit(t *testing.T, minLen int) string {
	t.Helper()
	words := make([]string, 0, minLen/4)
	for i := 0; ; i++ {
		words = append(words, fmt.Sprintf("w%dx", i))
		unit := strings.Join(words, " ")
		if len(unit) >= minLen {
			return unit
		}
	}
}

// ---------------------------------------------------------------------------
// Graduated response: soft strikes warn the model and re-stream; only the
// strike at the configured limit stops the turn. The counter resets after a
// configurable number of clean messages/tool calls.
// ---------------------------------------------------------------------------

func TestStreamLoopStrike_DefaultsAndReset(t *testing.T) {
	a := NewAgent(Config{Model: testModel(provider.ApiOpenAICompletions)})
	if got := a.effectiveStreamLoopMaxStrikes(); got != 3 {
		t.Errorf("default max strikes = %d, want 3", got)
	}
	if got := a.effectiveStreamLoopResetAfter(); got != 10 {
		t.Errorf("default reset-after = %d, want 10", got)
	}

	// Strikes accumulate and the clean counter restarts on each strike.
	if s := a.registerStreamLoopStrike(); s != 1 {
		t.Fatalf("first strike = %d, want 1", s)
	}
	a.noteStreamLoopCleanActivity(5)
	if s := a.registerStreamLoopStrike(); s != 2 {
		t.Fatalf("second strike = %d, want 2", s)
	}

	// 9 clean activities do not reach the default reset streak of 10.
	a.noteStreamLoopCleanActivity(9)
	if a.streamLoopStrikes != 2 {
		t.Fatalf("strikes reset too early: clean=9, strikes=%d, want 2", a.streamLoopStrikes)
	}
	// The 10th clean message/tool call resets the counter.
	a.noteStreamLoopCleanActivity(1)
	if a.streamLoopStrikes != 0 || a.streamLoopCleanCount != 0 {
		t.Errorf("strikes = %d after 10 clean activities, want 0 (clean=%d)", a.streamLoopStrikes, a.streamLoopCleanCount)
	}

	// Configured values override the defaults.
	a2 := NewAgent(Config{
		Model:                testModel(provider.ApiOpenAICompletions),
		StreamLoopMaxStrikes: 5,
		StreamLoopResetAfter: 3,
	})
	if got := a2.effectiveStreamLoopMaxStrikes(); got != 5 {
		t.Errorf("configured max strikes = %d, want 5", got)
	}
	a2.registerStreamLoopStrike()
	a2.noteStreamLoopCleanActivity(2)
	if a2.streamLoopStrikes != 1 {
		t.Fatalf("strikes reset too early with reset-after=3: strikes=%d", a2.streamLoopStrikes)
	}
	a2.noteStreamLoopCleanActivity(1)
	if a2.streamLoopStrikes != 0 {
		t.Errorf("strikes = %d after 3 clean activities (reset-after=3), want 0", a2.streamLoopStrikes)
	}
}

// streamLoopStrikeUnit is a >20-byte repeated unit used by the integration
// fixtures: five exact copies trip the detector at the default threshold.
const streamLoopStrikeUnit = "The quick brown fox jumps over the lazy dog while the diligent tester watches the whole test suite fail"

// loopingTextEvents emits the unit as consecutive deltas with no separator:
// the buffer ends in exact period-sized copies, so the detector fires mid-stream.
func loopingTextEvents(unit string, copies int) []provider.AssistantMessageEvent {
	evts := make([]provider.AssistantMessageEvent, 0, copies)
	for i := 0; i < copies; i++ {
		evts = append(evts, provider.AssistantMessageEvent{Type: provider.EventTextDelta, Delta: unit})
	}
	return evts
}

// runStrikeTurn drives a full turn against a scripted provider, collecting
// the emitted content texts, and returns the turn error, the texts, and the
// agent (for inspecting the strike counter after the turn).
func runStrikeTurn(t *testing.T, p *scriptedStreamProvider, cfg Config) (error, []string, *Agent) {
	t.Helper()
	provider.RegisterApiProvider(p)
	cfg.Model = provider.Model{
		ID:         "strike-test",
		Api:        p.API(),
		Provider:   provider.ProviderCustom,
		InputTypes: []string{"text"},
	}
	if cfg.Logger == nil {
		cfg.Logger = NewLogger(Error)
	}
	if cfg.SystemPrompt == "" {
		cfg.SystemPrompt = "test"
	}
	agent := NewAgent(cfg)

	obs := &mockEventObserver{}
	agent.AddObserver(obs)
	go func() {
		for range agent.Output {
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := agent.Run(ctx, "prompt")

	var texts []string
	for _, ev := range obs.Events() {
		if ev.Type == EventContent && ev.Text != "" {
			texts = append(texts, ev.Text)
		}
	}
	return err, texts, agent
}

func newStrikeProvider(name string, steps ...scriptedStreamStep) *scriptedStreamProvider {
	return &scriptedStreamProvider{
		api:   provider.Api(fmt.Sprintf("%s-%d", name, testProviderCounter.Add(1))),
		steps: steps,
	}
}

// A looped round is abandoned, the model is warned, and the turn re-streams:
// a model that recovers produces its answer and the turn ends without error.
func TestStreamLoopStrike_WarnsThenRecovers(t *testing.T) {
	p := newStrikeProvider("test-softstrike",
		scriptedStreamStep{events: loopingTextEvents(streamLoopStrikeUnit, 6)},
		scriptedStreamStep{events: []provider.AssistantMessageEvent{
			{Type: provider.EventTextDelta, Delta: "All done — concise answer without repetition."},
		}},
	)
	err, texts, agent := runStrikeTurn(t, p, Config{})
	if err != nil {
		t.Fatalf("turn failed after soft strike: %v", err)
	}
	if p.Calls() != 2 {
		t.Errorf("provider calls = %d, want 2 (looped round + re-stream)", p.Calls())
	}
	joined := strings.Join(texts, "\n")
	if !strings.Contains(joined, "Stream loop detected (warning 1 of 3)") {
		t.Errorf("warning bubble missing from output; got:\n%s", joined)
	}
	if !strings.Contains(joined, "All done") {
		t.Errorf("re-streamed answer missing from output; got:\n%s", joined)
	}
	// One strike was registered; the single clean final round is not enough
	// to reset it (default reset-after is 10).
	if agent.streamLoopStrikes != 1 {
		t.Errorf("strikes = %d after one soft strike and a clean round, want 1", agent.streamLoopStrikes)
	}
}

// A model that keeps looping exhausts the warnings; the third detection
// stops the turn with the loop error.
func TestStreamLoopStrike_ThirdStrikeStopsTurn(t *testing.T) {
	p := newStrikeProvider("test-hardstrike",
		scriptedStreamStep{events: loopingTextEvents(streamLoopStrikeUnit, 6)},
	)
	err, texts, _ := runStrikeTurn(t, p, Config{})
	if err == nil {
		t.Fatal("turn succeeded despite the model looping on every round")
	}
	if !strings.Contains(err.Error(), "stream loop detected") {
		t.Errorf("turn error = %v, want it to mention 'stream loop detected'", err)
	}
	if !strings.Contains(err.Error(), "after 2 warnings") {
		t.Errorf("turn error = %v, want it to mention 'after 2 warnings'", err)
	}
	if p.Calls() != 3 {
		t.Errorf("provider calls = %d, want 3 (2 soft strikes + 1 hard stop)", p.Calls())
	}
	joined := strings.Join(texts, "\n")
	if !strings.Contains(joined, "Stream loop detected (warning 1 of 3)") ||
		!strings.Contains(joined, "Stream loop detected (warning 2 of 3)") {
		t.Errorf("expected both soft-strike warnings in output; got:\n%s", joined)
	}
}

// StreamLoopMaxStrikes=1 restores the pre-feature behavior: the very first
// detection stops the turn.
func TestStreamLoopStrike_MaxStrikesOneStopsImmediately(t *testing.T) {
	p := newStrikeProvider("test-onestrike",
		scriptedStreamStep{events: loopingTextEvents(streamLoopStrikeUnit, 6)},
	)
	err, _, _ := runStrikeTurn(t, p, Config{StreamLoopMaxStrikes: 1})
	if err == nil {
		t.Fatal("turn succeeded despite maxStrikes=1 and a looping model")
	}
	if !strings.Contains(err.Error(), "stream loop detected") {
		t.Errorf("turn error = %v, want it to mention 'stream loop detected'", err)
	}
	if p.Calls() != 1 {
		t.Errorf("provider calls = %d, want 1 (immediate stop)", p.Calls())
	}
}

// ---------------------------------------------------------------------------
// Thinking-stall watchdog: a separate guard with its own flag, error message
// and disable switch — it must never surface as "stream loop detected" and
// must not be affected by the stream-loop toggle.
//
// Semantics (session export 2026-08-15, goa-export-20260815-015148.zip): the
// watchdog must only fire when NO thinking deltas arrive for longer than the
// stop threshold — a true stream gap/hang. A slow local model that streams
// reasoning tokens continuously for longer than the threshold is making
// progress and must never be stopped. The pre-fix code measured from the
// first thinking delta of the phase, so a continuously-streaming locallm was
// killed exactly thinking_stall_stop_seconds after its first delta despite
// 2603 subsequent deltas ("No stall but marked as stall").
// ---------------------------------------------------------------------------

// waitForThinkingStall polls for the watchdog flag set by the stall timer
// goroutine. The flag is written asynchronously, so a fixed sleep would be
// racy (and -race-flagged without the mutex); polling keeps tests fast and
// deterministic.
func waitForThinkingStall(t *testing.T, a *Agent, want bool, timeout time.Duration, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		a.mu.Lock()
		got := a.thinkingStalled
		a.mu.Unlock()
		if got == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	a.mu.Lock()
	got := a.thinkingStalled
	a.mu.Unlock()
	t.Fatalf("%s: thinkingStalled = %v, want %v (after %v)", msg, got, want, timeout)
}

// The exported-incident regression: thinking deltas arriving continuously
// (here faster than the stop threshold) for LONGER than the threshold must
// never trip the watchdog — every delta is progress.
func TestThinkingStall_ContinuousDeltasNotStalled(t *testing.T) {
	const stop = 120 * time.Millisecond
	a := NewAgent(Config{
		Model:             testModel(provider.ApiOpenAICompletions),
		Logger:            NewLogger(Error),
		ThinkingStallWarn: 40 * time.Millisecond,
		ThinkingStallStop: stop,
	})
	go func() {
		for range a.Output {
		}
	}()

	// Drip deltas every stop/4 for ~4x the stop threshold: cumulative
	// thinking-phase duration far exceeds the budget, but no gap does.
	for i := 0; i < 16; i++ {
		a.handleThinkingDelta(provider.AssistantMessageEvent{Type: provider.EventThinkingDelta, Delta: "reasoning"})
		a.mu.Lock()
		stalled := a.thinkingStalled
		a.mu.Unlock()
		if stalled {
			t.Fatalf("delta %d: thinkingStalled set while deltas were still arriving (gap < stop threshold)", i)
		}
		time.Sleep(stop / 4)
	}
	waitForThinkingStall(t, a, false, 2*stop, "active thinking stream longer than the stop threshold")
}

// A true hang: thinking deltas stop arriving for longer than the stop
// threshold. No further deltas arrive to re-evaluate the per-delta check,
// so the stall must be detected by the re-armed stall timer alone.
func TestThinkingStall_NoDeltaGapStops(t *testing.T) {
	const stop = 60 * time.Millisecond
	a := NewAgent(Config{
		Model:             testModel(provider.ApiOpenAICompletions),
		Logger:            NewLogger(Error),
		ThinkingStallStop: stop,
	})
	go func() {
		for range a.Output {
		}
	}()

	a.handleThinkingDelta(provider.AssistantMessageEvent{Type: provider.EventThinkingDelta, Delta: "some reasoning"})
	// No more deltas arrive — the timer must fire on its own.
	waitForThinkingStall(t, a, true, 5*stop, "no thinking deltas for longer than the stop threshold")
	a.mu.Lock()
	elapsed := a.thinkingStallElapsed
	a.mu.Unlock()
	if elapsed < stop {
		t.Errorf("thinkingStallElapsed = %v, want >= %v (the silence gap)", elapsed, stop)
	}

	done, _, err := a.handleStreamEvent(context.Background(), nil, provider.AssistantMessageEvent{Type: provider.EventThinkingDelta, Delta: "x"})
	if !done || err == nil {
		t.Fatalf("handleStreamEvent = (done=%v, err=%v), want done=true with a stall error", done, err)
	}
	if !strings.Contains(err.Error(), "thinking stalled") {
		t.Errorf("stall error = %v, want it to mention 'thinking stalled'", err)
	}
}

// The stall error must still be reported under its own name and must not
// leak into the stream-loop guard.
func TestThinkingStall_SeparateFlagAndError(t *testing.T) {
	a := NewAgent(Config{
		Model:             testModel(provider.ApiOpenAICompletions),
		Logger:            NewLogger(Error),
		ThinkingStallStop: 50 * time.Millisecond,
	})
	go func() {
		for range a.Output {
		}
	}()
	// Simulate a model whose reasoning stream has gone silent for far longer
	// than the configured stop duration: the last delta arrived 10 minutes
	// ago, so this new delta lands after a gap that exceeds the threshold.
	a.thinkingStallStart = time.Now().Add(-10 * time.Minute)
	a.handleThinkingDelta(provider.AssistantMessageEvent{Type: provider.EventThinkingDelta, Delta: "still reasoning"})

	a.mu.Lock()
	stalled := a.thinkingStalled
	looped := a.streamLoopDetected
	a.mu.Unlock()
	if !stalled {
		t.Fatal("thinkingStalled not set after a reasoning-only silence gap beyond the stop threshold")
	}
	if looped {
		t.Error("streamLoopDetected set by the thinking-stall watchdog — the guards must stay separate")
	}

	done, _, err := a.handleStreamEvent(context.Background(), nil, provider.AssistantMessageEvent{Type: provider.EventThinkingDelta, Delta: "x"})
	if !done || err == nil {
		t.Fatalf("handleStreamEvent = (done=%v, err=%v), want done=true with a stall error", done, err)
	}
	if !strings.Contains(err.Error(), "thinking stalled") {
		t.Errorf("stall error = %v, want it to mention 'thinking stalled'", err)
	}
	if strings.Contains(err.Error(), "stream loop detected") {
		t.Errorf("stall error = %v, must NOT be misreported as a stream loop", err)
	}
}

// The warn progress event fires when thinking has been silent past the warn
// threshold, and the stop timer then declares the stall after the stop
// threshold of silence. Once stalled the decision is final: a late content
// delta disarms the timers (forward progress) but must NOT resurrect a turn
// that is already being stopped with the stall error.
func TestThinkingStall_WarnOnSilenceThenStallIsFinal(t *testing.T) {
	const warn = 30 * time.Millisecond
	const stop = 90 * time.Millisecond
	a := NewAgent(Config{
		Model:             testModel(provider.ApiOpenAICompletions),
		Logger:            NewLogger(Error),
		ThinkingStallWarn: warn,
		ThinkingStallStop: stop,
	})
	progress := make(chan string, 8)
	a.AddObserver(OutputObserverFunc(func(ev OutputEvent) {
		if ev.Type == EventProgress {
			select {
			case progress <- ev.Text:
			default:
			}
		}
	}))
	go func() {
		for range a.Output {
		}
	}()

	a.handleThinkingDelta(provider.AssistantMessageEvent{Type: provider.EventThinkingDelta, Delta: "reasoning"})
	select {
	case text := <-progress:
		if !strings.Contains(text, "without producing output") {
			t.Errorf("warn progress text = %q, want it to mention missing output", text)
		}
		// warn emitted after the warn threshold of silence
	case <-time.After(5 * stop):
		t.Fatal("no warn progress event after thinking silence exceeded the warn threshold")
	}

	waitForThinkingStall(t, a, true, 5*stop, "thinking silence past the stop threshold")

	// A content delta arrives after the stall was declared: forward progress
	// disarms the timers and clears the phase clock, but the stall flag is
	// sticky — the turn is already being stopped with the stall error.
	a.handleTextDelta(provider.AssistantMessageEvent{Type: provider.EventTextDelta, Delta: "answer"})
	a.mu.Lock()
	start := a.thinkingStallStart
	stalled := a.thinkingStalled
	a.mu.Unlock()
	if !start.IsZero() {
		t.Error("thinkingStallStart not cleared by content progress")
	}
	if !stalled {
		t.Error("thinkingStalled must stay set once declared — the stall stop is final")
	}
	// The stall error still surfaces on the next handled event.
	done, _, err := a.handleStreamEvent(context.Background(), nil, provider.AssistantMessageEvent{Type: provider.EventTextDelta, Delta: "x"})
	if !done || err == nil || !strings.Contains(err.Error(), "thinking stalled") {
		t.Errorf("handleStreamEvent = (done=%v, err=%v), want done=true with a 'thinking stalled' error", done, err)
	}
}

// Stall timers must not survive a stream round: after resetStreamRoundState
// a fresh reasoning phase starts with a clean clock.
func TestThinkingStall_TimersStopAtRoundBoundary(t *testing.T) {
	const stop = 60 * time.Millisecond
	a := NewAgent(Config{
		Model:             testModel(provider.ApiOpenAICompletions),
		Logger:            NewLogger(Error),
		ThinkingStallStop: stop,
	})
	go func() {
		for range a.Output {
		}
	}()

	a.handleThinkingDelta(provider.AssistantMessageEvent{Type: provider.EventThinkingDelta, Delta: "reasoning"})
	a.resetStreamRoundState()
	a.mu.Lock()
	start := a.thinkingStallStart
	a.mu.Unlock()
	if !start.IsZero() {
		t.Fatal("thinkingStallStart must be cleared at the round boundary")
	}
	waitForThinkingStall(t, a, false, 3*stop, "stale stall timer after round reset")
}

func TestThinkingStall_DisabledByHook(t *testing.T) {
	disabled := true
	a := NewAgent(Config{
		Model:                 testModel(provider.ApiOpenAICompletions),
		Logger:                NewLogger(Error),
		ThinkingStallStop:     50 * time.Millisecond,
		ThinkingStallDisabled: func() bool { return disabled },
	})
	go func() {
		for range a.Output {
		}
	}()
	a.thinkingStallStart = time.Now().Add(-10 * time.Minute)
	a.handleThinkingDelta(provider.AssistantMessageEvent{Type: provider.EventThinkingDelta, Delta: "still reasoning"})
	a.mu.Lock()
	stalled := a.thinkingStalled
	a.mu.Unlock()
	if stalled {
		t.Fatal("thinkingStalled set while the watchdog was disabled")
	}
	// No timer may be armed while disabled, so nothing can fire later either.
	waitForThinkingStall(t, a, false, 5*50*time.Millisecond, "watchdog disabled")

	// Re-enable mid-stream: the same over-long silence gap must now stop.
	disabled = false
	a.thinkingStallStart = time.Now().Add(-10 * time.Minute)
	a.handleThinkingDelta(provider.AssistantMessageEvent{Type: provider.EventThinkingDelta, Delta: "more reasoning"})
	a.mu.Lock()
	stalled = a.thinkingStalled
	a.mu.Unlock()
	if !stalled {
		t.Error("thinkingStalled not set after re-enabling the watchdog on an over-long silence gap")
	}
}

// TestStreamLoop_TUIRepetitionSampleDetected is the exact flood from the
// TUI shows unexpected repetition on normal messages:entry: the
// model repeated one accusation with walking casing/punctuation variants
// ("is never called —" / "is NEVER called!" / "is NEVER CALLED."). After
// case folding and punctuation stripping the copies are byte-exact, so the
// chain detector must fire — this class previously reached the TUI
// unchecked.
func TestStreamLoop_TUIRepetitionSampleDetected(t *testing.T) {
	unit := "isIgnoreableConflict is never called — the error comes from a different path. Let me find all callers of checkConstraints:"
	variants := []string{
		"isIgnoreableConflict is never called — the error comes from a different path. Let me find all callers of checkConstraints:",
		"isIgnoreableConflict is NEVER called! The error must come from a different path. Let me find all callers of checkConstraints:",
		"isIgnoreableConflict is NEVER CALLED. The error must come from a different path. Let me find all callers of checkConstraints:",
		"isIgnoreableConflict is never called — the error comes from a different path. Let me find all callers of checkConstraints:",
		"isIgnoreableConflict is NEVER CALLED! The error must come from a different path. Let me find all callers of checkConstraints:",
		"isIgnoreableConflict is NEVER called. The error must come from a different path. Let me find all callers of checkConstraints:",
	}
	var b strings.Builder
	b.WriteString("Wait — the insert (4,1) goes through execInsert → insertRow. insertRow line 116: checkConstraints → error → isIgnoreableConflict (debug would print). But NO debug print — so either: ")
	for _, v := range variants {
		b.WriteString(v)
	}
	_ = unit
	if !streamLoopWouldDetect(b.String(), 3) {
		t.Error("TUI repetition sample (casing/punctuation variants of one accusation) must trip the detector")
	}
}

// TestStreamLoop_NoFalsePositiveOnStructuredHeaders is the false-positive
// validation for the runaway-loop visibility bug (2026-08-03): a
// long structured report whose sections share markdown headers and table
// separators — but whose per-section content is genuinely varied — is
// legitimate near-repetition, not a loop, and must never trip the detector
// (neither the byte-exact chain nor the shingle-coverage path).
func TestStreamLoop_NoFalsePositiveOnStructuredHeaders(t *testing.T) {
	// Varied per-section analysis lines: shared headers, distinct content.
	analysis := []string{
		"The tokenizer now accepts nested brackets, which fixed the precedence regression seen in the previous pass.",
		"Coverage dipped slightly because the new guard clause short-circuits before the metrics hook runs.",
		"A flaky timing assertion was replaced with a fake clock, so the scheduler tests are deterministic again.",
		"The exporter batch size was raised after profiling showed the flush dominated total runtime.",
		"Retry backoff now caps at thirty seconds; longer waits hid genuine outages in staging last week.",
		"Index compaction moved off the request path, removing the p99 latency spike under write bursts.",
		"The schema migrator gained a dry-run mode so reviewers can inspect the plan before applying it.",
		"Connection pooling was reconfigured to match the database's actual max_connections setting.",
		"A bounds check in the ring buffer prevented the overwrite that corrupted metrics every few hours.",
		"The cache key now includes the locale, fixing the wrong-language snippets served to some users.",
		"Log sampling was tuned to keep error traces while dropping routine health-check chatter.",
		"The rollout finished without incident; error rates stayed flat across all twelve availability zones.",
	}
	var b strings.Builder
	for i, line := range analysis {
		fmt.Fprintf(&b, "## Test Results — Iteration %d\n", i+1)
		b.WriteString("| Metric | Value |\n| --- | --- |\n")
		fmt.Fprintf(&b, "| coverage | %d.%d%% |\n| duration | %dms |\n\n", 75+i, i, 90+i*13)
		b.WriteString(line + "\n\n")
	}
	text := b.String()
	if streamLoopWouldDetect(text, 5) {
		t.Error("false positive: structured report with repeated headers detected as a loop")
	}
	// No prefix may trip either (the detector runs per delta).
	var buf strings.Builder
	const fragSize = 31
	for pos := 0; pos < len(text); pos += fragSize {
		end := pos + fragSize
		if end > len(text) {
			end = len(text)
		}
		buf.WriteString(text[pos:end])
		if streamLoopWouldDetect(buf.String(), 5) {
			t.Fatalf("false positive mid-stream at byte %d of structured report", end)
		}
	}
}

// TestExactChainSample covers the evidence window extraction: a sample
// starting on a word boundary is returned as-is; a mid-word cut (Detector A
// fires at the smallest qualifying period) snaps back to the nearest space,
// bounded by streamLoopSampleSnap, so the displayed repeat reads as full
// words.
func TestExactChainSample(t *testing.T) {
	tests := []struct {
		name   string
		tail   string
		period int
		want   string
	}{
		{"word boundary unchanged", "the quick brown", 5, "brown"},
		{"mid-word snaps to previous space", "see sentence repeats", 14, "sentence repeats"},
		{"no space within snap window keeps start", "aaaaaaaaaaaaaaaaaaaa b", 2, " b"},
		{"whole tail", "whole", 5, "whole"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := exactChainSample(tt.tail, tt.period); got != tt.want {
				t.Errorf("exactChainSample(%q, %d) = %q, want %q", tt.tail, tt.period, got, tt.want)
			}
		})
	}
}
