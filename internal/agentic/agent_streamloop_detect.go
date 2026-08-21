// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"context"
	"fmt"
	"strings"
	"unicode"
)

const (
	// defaultStreamLoopMaxStrikes is the number of stream-loop detections
	// after which the turn is stopped. Earlier detections are soft: the
	// looped round is abandoned, the model is warned with an ephemeral
	// hint, and the turn re-streams (execution.stream_loop_max_strikes).
	defaultStreamLoopMaxStrikes = 3
	// defaultStreamLoopResetAfter is the number of clean messages/tool
	// calls (no loop detected) after which the strike counter resets to
	// zero (execution.stream_loop_reset_after).
	defaultStreamLoopResetAfter = 10
)

// effectiveStreamLoopMaxStrikes resolves the strike limit, defaulting to 3.
func (a *Agent) effectiveStreamLoopMaxStrikes() int {
	if a.cfg.StreamLoopMaxStrikes > 0 {
		return a.cfg.StreamLoopMaxStrikes
	}
	return defaultStreamLoopMaxStrikes
}

// effectiveStreamLoopResetAfter resolves the clean-activity count that
// resets the strike counter, defaulting to 10.
func (a *Agent) effectiveStreamLoopResetAfter() int {
	if a.cfg.StreamLoopResetAfter > 0 {
		return a.cfg.StreamLoopResetAfter
	}
	return defaultStreamLoopResetAfter
}

// registerStreamLoopStrike records a stream-loop detection: the strike count
// increments and the clean-activity counter restarts. Returns the strike
// number (1-based).
func (a *Agent) registerStreamLoopStrike() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.streamLoopStrikes++
	a.streamLoopCleanCount = 0
	a.streamLoopStrikeThisRound = true
	return a.streamLoopStrikes
}

// noteStreamLoopCleanActivity records n clean messages/tool calls (no loop
// detected) and resets the strike counter once the configured clean streak
// (execution.stream_loop_reset_after, default 10) is reached.
func (a *Agent) noteStreamLoopCleanActivity(n int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.streamLoopStrikes == 0 || n <= 0 {
		return
	}
	a.streamLoopCleanCount += n
	if a.streamLoopCleanCount >= a.effectiveStreamLoopResetAfter() {
		a.cfg.Logger.Log(Info, "Stream-loop strike counter reset after %d clean messages/tool calls", a.streamLoopCleanCount)
		a.streamLoopStrikes = 0
		a.streamLoopCleanCount = 0
	}
}

// noteCleanStreamRound counts a stream round that completed without a loop
// strike as one clean message toward resetting the strike counter.
func (a *Agent) noteCleanStreamRound() {
	a.mu.Lock()
	strike := a.streamLoopStrikeThisRound
	a.mu.Unlock()
	if strike {
		return
	}
	a.noteStreamLoopCleanActivity(1)
}

// handleStreamLoopStrike applies the graduated stream-loop response: the
// detections below the strike limit are soft (the looped round is abandoned,
// the model is warned with an ephemeral hint, and the turn re-streams); the
// detection at the limit stops the turn with an error. Reports whether the
// turn continues (soft strike) or the terminal error (hard strike).
func (a *Agent) handleStreamLoopStrike(ctx context.Context) (toolCallsEncountered bool, err error) {
	strike := a.registerStreamLoopStrike()
	maxStrikes := a.effectiveStreamLoopMaxStrikes()
	if strike >= maxStrikes {
		a.cfg.Logger.Log(Warn, "Stream loop strike %d/%d: the model kept repeating; stopping the turn", strike, maxStrikes)
		return false, fmt.Errorf("stream loop detected: the assistant kept repeating the same text after %d warnings%s; turn stopped to prevent runaway context usage", strike-1, loopEvidenceSuffix(a.streamLoopSample))
	}
	a.cfg.Logger.Log(Warn, "Stream loop strike %d/%d: abandoning the looped round and warning the model", strike, maxStrikes)
	return a.recoverFromStreamLoop(ctx, strike, maxStrikes), nil
}

// recoverFromStreamLoop handles a soft stream-loop strike: the looped round
// is abandoned — its repetition-laden partial text is NOT committed to
// history — the user is shown a warning, and an ephemeral hint tells the
// model to continue without repeating, so the next round re-streams. When
// the round already buffered complete tool calls, they run through the
// normal execution path so the model keeps their results. Always reports
// that the turn continues (a soft strike never ends the turn directly; the
// tool-call path may still end it via the normal budget rules).
func (a *Agent) recoverFromStreamLoop(ctx context.Context, strike, maxStrikes int) bool {
	a.emitEvent(OutputEvent{
		Type: EventContent,
		Role: System,
		Text: fmt.Sprintf("Stream loop detected (warning %d of %d) — the reply was cut off%s; the model was told to continue without repeating.", strike, maxStrikes, loopEvidenceSuffix(a.streamLoopSample)),
		// stream_retry retracts the orphaned in-progress assistant bubble:
		// the looped partial text is discarded, not finalized, so without a
		// retraction it would linger next to the re-streamed answer.
		Metadata: map[string]string{"category": "system-notification", "stream_retry": "true"},
	})
	a.InjectEphemeralSystemMessage(
		"[goa-system] Internal control note (never show or mention to the user): your previous output started " +
			"repeating the same block of text over and over and was cut off. Do not repeat yourself. Continue the " +
			"task now: move forward, keep the answer concise, and do not restate earlier text.")
	if len(a.bufferedToolCalls) > 0 {
		// Complete tool calls arrived before the loop started: execute them
		// through the normal path so the model keeps their results. The
		// ephemeral warning rides along in history for the next round.
		return a.completeStreamTurn(ctx)
	}
	return true
}

// streamLoopScan is the detection core of checkStreamLoop: it reports whether
// the normalized buffer ends in a repeated unit, and if so returns the unit
// size and repeat count. Kept separate from the Agent method so the exact
// production scan can be exercised directly by tests.
//
// Detection policy (count-based rewrite after field failures in BOTH
// directions — a false positive on exploratory Option A/B/C analysis and a
// false negative on a ~90-copy paraphrase loop; see 2026-08-01):
//
//   - Detector A (exact chain): the trailing unit of length P
//     (P ≥ the configured min period, default streamLoopExactMinPeriod) is a
//     loop when it repeats BYTE-EXACT ≥ maxRepeats times (≥ 2 for
//     P ≥ streamLoopLongPeriod), allowing ≤ streamLoopMaxGap interlude bytes
//     between copies. No fuzzy matching, no progression analysis: exploratory
//     paragraphs never repeat 50+ exact bytes, and connector noise
//     ("the the the …") lives below the floor.
//   - Detector B (paraphrase coverage): a loop whose copies drift in wording
//     has no exact unit, but its words are almost all inside a handful of
//     repeated shingles. Fire when ≥ streamLoopMinHotShingles distinct
//     shingles are "hot" (≥ streamLoopShingleHot occurrences) AND they cover
//     ≥ streamLoopMinCoverage of the tail words. A 3–4 paragraph Option
//     A/B/C analysis has almost no hot shingles; repeating one TERM has too
//     few hot shingles; enumerated lists have unique shingles.
//   - Only the trailing streamLoopTailWindow bytes are scanned, bounding the
//     per-delta cost.
//
// The returned sample is the repeated sequence evidence surfaced in
// warning/stop messages (runaway-loop visibility): for Detector A it
// is one byte-exact repeat unit; for Detector B — a paraphrase loop has no
// exact unit — it is the scanned tail, which the hot shingles dominate.
func streamLoopScan(clean string, maxRepeats, minPeriod int) (period, repeats int, sample string, ok bool) {
	if maxRepeats < 2 {
		maxRepeats = 2
	}
	tail := clean
	if len(tail) > streamLoopTailWindow {
		tail = tail[len(tail)-streamLoopTailWindow:]
	}
	if uniqueWordCount(tail) < 3 {
		// A tail of one or two unique words ("the the the …", "ok ok …") is
		// connector noise, not repeated content; the loop detectors need at
		// least three distinct words to have an opinion.
		return 0, 0, "", false
	}
	if period, repeats, ok := streamExactChain(tail, maxRepeats, minPeriod); ok {
		return period, repeats, exactChainSample(tail, period), true
	}
	if period, repeats, ok := streamParaphraseLoop(tail, maxRepeats); ok {
		return period, repeats, tail, true
	}
	return 0, 0, "", false
}

// uniqueWordCount counts distinct space-separated words in s, stopping early
// at 3 (only the <3 case matters to the caller).
func uniqueWordCount(s string) int {
	seen := make(map[string]struct{}, 8)
	for _, w := range strings.Fields(s) {
		seen[w] = struct{}{}
		if len(seen) >= 3 {
			break
		}
	}
	return len(seen)
}

const (
	// streamLoopExactMinPeriod is the default smallest repeated unit
	// Detector A considers (execution.stream_loop_min_period overrides it):
	// shorter exact repeats are punctuation/connector noise. All field
	// false positives were NON-exact, so exact-only matching is safe at
	// this floor; a genuine micro-loop with a shorter unit also repeats
	// at a multiple of the unit, which qualifies.
	streamLoopExactMinPeriod = 50
	// streamLoopLongPeriod is the unit size from which two byte-exact copies
	// already count as a loop: nobody legitimately repeats a kilobyte twice.
	streamLoopLongPeriod = 1024
	// streamLoopMaxGap bounds the interlude allowed between chained copies
	// so "repeat with a one-line interjection" loops still trip.
	streamLoopMaxGap = 64
	// streamLoopTailWindow bounds the scanned tail (and per-delta cost).
	streamLoopTailWindow = 4096
	// streamLoopSampleSnap bounds how far exactChainSample looks backward
	// for a word boundary when the minimal firing period cut the repeated
	// unit mid-word.
	streamLoopSampleSnap = 20
	// streamLoopSmallPeriod is the smallest period scanned at all; below it
	// only connector noise lives ("the the the …").
	streamLoopSmallPeriod = 8
	// streamLoopShingleWords is the shingle size for Detector B.
	streamLoopShingleWords = 3
	// streamLoopShingleHot is the base occurrence count making a shingle
	// "hot" (raised to the configured maxRepeats when that is higher).
	streamLoopShingleHot = 4
	// streamLoopMinHotShingles is the number of distinct hot shingles a
	// paraphrase loop must have: a couple of repeated terms is topical
	// emphasis, not a loop.
	streamLoopMinHotShingles = 4
	// streamLoopMinWords is the tail word floor for Detector B.
	streamLoopMinWords = 80
	// streamLoopMinCoverage is the fraction of tail words that must sit
	// inside hot shingles: paraphrase loops are dominated by their template,
	// while topical repetition keeps repeated fragments a small minority.
	streamLoopMinCoverage = 0.4
)

// exactChainSample extracts Detector A's repeated unit — the trailing
// period bytes of the tail — as display evidence. Detector A fires at the
// smallest qualifying period, which can cut the true repeated unit mid-word
// ("entence repeats…"); when the window does not start on a word boundary,
// the start snaps backward to the nearest space (bounded by
// streamLoopSampleSnap) so the rendered evidence reads as full words.
func exactChainSample(tail string, period int) string {
	start := len(tail) - period
	if start <= 0 || tail[start-1] == ' ' {
		return tail[start:]
	}
	lo := start - streamLoopSampleSnap
	if lo < 0 {
		lo = 0
	}
	if sp := strings.LastIndexByte(tail[lo:start], ' '); sp >= 0 {
		start = lo + sp + 1
	}
	return tail[start:]
}

// streamExactChain implements Detector A: for each candidate period, chain
// byte-exact copies of the trailing unit backward through the tail.
//
// Required copy count (certainty rises with unit size and count):
//   - P ≥ streamLoopLongPeriod: 2 copies (nobody repeats a kilobyte twice)
//   - minPeriod ≤ P < long: max(maxRepeats, 3) — a pair of
//     sub-kilobyte quotes is evidence, not a loop, at any knob setting
//   - streamLoopSmallPeriod ≤ P < minPeriod: max(maxRepeats, 8) — micro-loops
//     need overwhelming count
func streamExactChain(tail string, maxRepeats, minPeriod int) (period, repeats int, ok bool) {
	for p := streamLoopSmallPeriod; p <= len(tail)/2; p++ {
		required, gap, skip := chainRules(tail, p, maxRepeats, minPeriod)
		if skip {
			continue
		}
		if n := chainCopies(tail, p, gap); n >= required {
			return p, n, true
		}
	}
	return 0, 0, false
}

// chainRules returns the required copy count and interlude gap for a
// candidate period, and whether the period must be skipped entirely.
func chainRules(tail string, p, maxRepeats, minPeriod int) (required, gap int, skip bool) {
	switch {
	case p >= streamLoopLongPeriod:
		return 2, streamLoopMaxGap, false
	case p >= minPeriod:
		if maxRepeats < 3 {
			maxRepeats = 3
		}
		return maxRepeats, streamLoopMaxGap, false
	default:
		// Micro-units must be real word content: word fragments ("reopen pa"
		// riding a repeated term) are not loops.
		if len(strings.Fields(tail[len(tail)-p:])) < 3 {
			return 0, 0, true
		}
		if maxRepeats < 8 {
			maxRepeats = 8
		}
		// Tight chaining only: scattered occurrences are topical, not loops.
		return maxRepeats, p / 2, false
	}
}

// chainCopies counts how many byte-exact copies of the trailing p-byte unit
// chain backward through the tail, allowing up to gap interlude bytes
// between copies.
func chainCopies(tail string, p, gap int) int {
	unit := tail[len(tail)-p:]
	n, pos := 1, len(tail)-p
	for {
		lo := pos - p - gap
		if lo < 0 {
			lo = 0
		}
		idx := strings.LastIndex(tail[lo:pos], unit)
		if idx < 0 {
			return n
		}
		n++
		pos = lo + idx
	}
}

// streamParaphraseLoop implements Detector B: count word shingles in the
// tail and fire when enough distinct shingles are hot and they cover most of
// the tail words. The hot threshold tracks the configured repeat tolerance
// (never below streamLoopShingleHot), so a high maxRepeats knob also raises
// the paraphrase bar.
func streamParaphraseLoop(tail string, maxRepeats int) (period, repeats int, ok bool) {
	words := strings.Fields(tail)
	if len(words) < streamLoopMinWords {
		return 0, 0, false
	}
	hot := max(streamLoopShingleHot, maxRepeats)
	n := streamLoopShingleWords
	counts := shingleCounts(words, n)
	if hotShingleKeys(counts, hot) < streamLoopMinHotShingles {
		return 0, 0, false
	}
	coveredN := shingleCoveredWords(words, counts, hot, n)
	if float64(coveredN)/float64(len(words)) < streamLoopMinCoverage {
		return 0, 0, false
	}
	return n, coveredN / n, true
}

// shingleCounts counts overlapping n-word shingles over words.
func shingleCounts(words []string, n int) map[string]int {
	counts := make(map[string]int, len(words))
	for i := 0; i+n <= len(words); i++ {
		counts[strings.Join(words[i:i+n], " ")]++
	}
	return counts
}

// hotShingleKeys counts distinct shingles occurring at least hot times.
func hotShingleKeys(counts map[string]int, hot int) int {
	hotKeys := 0
	for _, c := range counts {
		if c >= hot {
			hotKeys++
		}
	}
	return hotKeys
}

// shingleCoveredWords counts word positions covered by any hot shingle.
func shingleCoveredWords(words []string, counts map[string]int, hot, n int) int {
	covered := make([]bool, len(words))
	coveredN := 0
	for i := 0; i+n <= len(words); i++ {
		if counts[strings.Join(words[i:i+n], " ")] < hot {
			continue
		}
		for j := i; j < i+n; j++ {
			if !covered[j] {
				covered[j] = true
				coveredN++
			}
		}
	}
	return coveredN
}

// streamLoopNormalize strips everything except letters, digits, and spaces,
// folds case, then collapses runs of spaces. This prevents punctuation,
// symbols, box-drawing characters, and casing drift from causing false
// positive (or false negative) loop detections.
func streamLoopNormalize(text string) string {
	var b strings.Builder
	b.Grow(len(text) / 2)
	prevSpace := false
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
			prevSpace = false
		} else if unicode.IsSpace(r) && !prevSpace {
			b.WriteRune(' ')
			prevSpace = true
		}
	}
	return strings.TrimSpace(b.String())
}
