// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"crypto/sha256"
	"fmt"
	"strings"
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
	// Consecutive-streak model ("Tool call loop detector: false
	// positives"): a true runaway loop repeats the same call(s) back-to-back
	// endlessly, while legitimate work REUSES an identical call across a long
	// session with other work in between (edit → `go build ./...` → edit →
	// rebuild — the exact session that was killed by the old lifetime-count
	// model: 11 identical builds over dozens of turns tripped the interrupt
	// at 10). So the detector tracks, per distinct call, the CURRENT streak:
	// any different call resets every streak. Only an unbroken run of
	// identical calls can reach the thresholds.
	toolStreaks            map[string]int // key: toolName+hash(input) → current consecutive streak
	lastToolKey            string         // key of the most recent call ("" = none)
	loopWarningThreshold   int            // same tool call streak before warning
	loopInterruptThreshold int            // same tool call streak before interrupt

	// Thinking-loop tracking — drives RecordThinkingDelta loop detection.
	// Complete lines (terminated by '\n') are hashed and counted; only lines
	// with at least minThinkWordCount words are counted so short repeated
	// bullets or separators do not false-positive. Code blocks and tool call
	// blocks are stripped before processing. thinkMaxRepeat tracks the highest
	// count seen for any single line in the current turn.
	thinkPending            string
	thinkLineCounts         map[string]int
	thinkMaxRepeat          int
	thinkWarningThreshold   int
	thinkInterruptThreshold int
	// thinkInCodeBlock tracks whether the accumulated thinking stream is
	// currently inside a ```-fenced code block. Fences span many lines, so the
	// state must persist across lines and deltas — stripping per single line
	// (after splitting on '\n') never sees the fence and let quoted code lines
	// count as repeated reasoning, killing legitimate deep-debugging turns
	// (thinking-loop false positive, exports 20260721-142256/142545).
	thinkInCodeBlock bool

	// Diversity-based loop detection. The exact-line counter above misses two
	// production failure modes (Issue 6): (a) the model ALTERNATES two
	// phrasings of the same intent ("Let me check the full file." / "Let me
	// read the full file."), so neither line's individual count crosses the
	// threshold; and (b) the looping lines are short (< minThinkWordCount
	// words), so they are filtered out before counting. Both are caught by
	// watching the DIVERSITY of recent prose lines: a genuine loop keeps only a
	// handful of distinct normalized lines in play while volume grows, whereas
	// legitimate reasoning keeps producing new lines.
	//
	// thinkRecentLines is a sliding window (ring) of the most recent
	// thinkWindowSize normalized prose lines; thinkRecentCounts counts each
	// distinct normalized line currently in the window. thinkWindowFull turns
	// true once the window has been filled at least once, so detection only
	// arms after enough volume to avoid false positives on short turns.
	thinkRecentLines  []string
	thinkRecentCounts map[string]int
	thinkRecentHead   int
	thinkRecentLen    int
	thinkWindowFull   bool

	// Error tracking (ring buffer). Populated by RecordToolResult; retained as
	// the integration point for a future (genuinely wired) error-rate check.
	errorHistory []bool // last N tool results (true = error)
	errorIdx     int

	// tempThinkDisabled, tempToolDisabled and tempStreamDisabled are
	// per-session temporary overrides that disable loop detection without
	// modifying the persisted config. Set via
	// /config:temp:<think|tool|stream>_loop_detection:off slash commands.
	// tempStallDisabled does the same for the thinking-stall watchdog via
	// /config:temp:thinking_stall_detection:off.
	tempThinkDisabled  bool
	tempToolDisabled   bool
	tempStreamDisabled bool
	tempStallDisabled  bool

	// persistThinkDisabled, persistToolDisabled and persistStreamDisabled
	// come from the persisted config (execution.disable_<kind>_loop_detection)
	// and disable detection across sessions. persistStallDisabled comes from
	// execution.disable_thinking_stall_detection.
	persistThinkDisabled  bool
	persistToolDisabled   bool
	persistStreamDisabled bool
	persistStallDisabled  bool

	// maxStreamRepeats is the live repeat threshold for the streaming text
	// loop detector (see LoopDetectorConfig.MaxStreamRepeats).
	maxStreamRepeats int
	// minStreamPeriod is the live minimum repeat-unit length (in characters)
	// for the streaming text loop detector (see
	// LoopDetectorConfig.MinStreamPeriod).
	minStreamPeriod int
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
	// ThinkingDisabled disables thinking-loop detection entirely.
	// Set via /config:temp:think_loop_detection:off for session-level override.
	ThinkingDisabled bool
	// ToolDisabled disables tool-call loop detection entirely.
	// Set via /config:temp:tool_loop_detection:off for session-level override.
	ToolDisabled bool
	// StreamDisabled disables the streaming text loop detector (repeated
	// suffix in the assistant's own streamed output) entirely. Set via
	// /config:temp:stream_loop_detection:off for session-level override.
	StreamDisabled bool
	// StallDisabled disables the thinking-stall watchdog (the guard that
	// stops the stream after an extended reasoning-only phase) entirely.
	// Set via /config:temp:thinking_stall_detection:off for session-level
	// override.
	StallDisabled bool
	// MaxStreamRepeats is the number of consecutive repeats of the same text
	// block required before the streaming loop detector stops the turn
	// (0 = default 5). From execution.stream_loop_max_repeats.
	MaxStreamRepeats int
	// MinStreamPeriod is the smallest repeated unit (in characters) the
	// streaming loop detector treats as a loop (0 = default 50). From
	// execution.stream_loop_min_period.
	MinStreamPeriod int
}

// defaultStreamLoopMaxRepeats is the built-in repeat threshold for the
// streaming text loop detector. Five copies is far beyond any legitimate
// repetition (quoting evidence, comparing similar snippets) while still
// stopping a runaway loop after only a few hundred wasted tokens.
const defaultStreamLoopMaxRepeats = 5

// defaultStreamLoopMinPeriod is the built-in smallest repeated unit (in
// characters) the streaming text loop detector treats as a loop. Shorter
// exact repeats are punctuation/connector noise. The absolute scan floor is
// 8: below it periods are never scanned at all.
const defaultStreamLoopMinPeriod = 50

// DefaultLoopDetectorConfig returns sensible defaults for the loop detector.
func DefaultLoopDetectorConfig() LoopDetectorConfig {
	return LoopDetectorConfig{
		LoopWarning:           7,
		LoopInterrupt:         10,
		ThinkingLoopWarning:   4,
		ThinkingLoopInterrupt: 6,
		MaxStreamRepeats:      defaultStreamLoopMaxRepeats,
	}
}

const loopErrorHistorySize = 10

// minThinkWordCount is the minimum number of words a line of reasoning must
// have before it contributes to thinking-loop counting. This excludes short
// repeated constructs (list markers, separators, single words) that
// legitimately recur. Changed from a character-based threshold (40 chars) to
// a word-based threshold (10 words) to provide more meaningful filtering.
const minThinkWordCount = 10

// thinkWindowSize is the number of recent prose lines kept for diversity-based
// loop detection. thinkMinWindowFill is how many lines must accumulate before
// diversity detection arms; thinkMaxDistinctLines is the maximum number of
// distinct normalized lines allowed in a full window before it is treated as a
// loop. A genuine A/B alternation or short-line loop keeps the distinct count
// at 1–2 over a full window; legitimate reasoning produces many more.
const (
	thinkWindowSize       = 24
	thinkMinWindowFill    = 12
	thinkMaxDistinctLines = 3
)

// NewLoopDetector creates a loop detector with the given config.
func NewLoopDetector(cfg LoopDetectorConfig) *LoopDetector {
	if cfg.ThinkingLoopWarning <= 0 {
		cfg.ThinkingLoopWarning = 4
	}
	if cfg.ThinkingLoopInterrupt <= 0 {
		cfg.ThinkingLoopInterrupt = 6
	}
	if cfg.MaxStreamRepeats < 2 {
		cfg.MaxStreamRepeats = defaultStreamLoopMaxRepeats
	}
	if cfg.MinStreamPeriod < 8 {
		cfg.MinStreamPeriod = defaultStreamLoopMinPeriod
	}
	return &LoopDetector{
		toolStreaks:             make(map[string]int),
		errorHistory:            make([]bool, loopErrorHistorySize),
		loopWarningThreshold:    cfg.LoopWarning,
		loopInterruptThreshold:  cfg.LoopInterrupt,
		thinkLineCounts:         make(map[string]int),
		thinkWarningThreshold:   cfg.ThinkingLoopWarning,
		thinkInterruptThreshold: cfg.ThinkingLoopInterrupt,
		thinkRecentLines:        make([]string, thinkWindowSize),
		thinkRecentCounts:       make(map[string]int),
		persistThinkDisabled:    cfg.ThinkingDisabled,
		persistToolDisabled:     cfg.ToolDisabled,
		persistStreamDisabled:   cfg.StreamDisabled,
		persistStallDisabled:    cfg.StallDisabled,
		maxStreamRepeats:        cfg.MaxStreamRepeats,
		minStreamPeriod:         cfg.MinStreamPeriod,
	}
}

// StreamMinPeriod returns the live minimum repeat-unit length (in
// characters) for the streaming text loop detector.
func (ld *LoopDetector) StreamMinPeriod() int {
	ld.mu.Lock()
	defer ld.mu.Unlock()
	return ld.minStreamPeriod
}

// SetStreamMinPeriod updates the live minimum repeat-unit length for the
// streaming text loop detector. Values below 8 (the absolute scan floor)
// restore the default; called when execution.stream_loop_min_period changes
// via /config set.
func (ld *LoopDetector) SetStreamMinPeriod(n int) {
	ld.mu.Lock()
	defer ld.mu.Unlock()
	if n < 8 {
		n = defaultStreamLoopMinPeriod
	}
	ld.minStreamPeriod = n
}

// StreamMaxRepeats returns the live repeat threshold for the streaming text
// loop detector.
func (ld *LoopDetector) StreamMaxRepeats() int {
	ld.mu.Lock()
	defer ld.mu.Unlock()
	return ld.maxStreamRepeats
}

// SetStreamMaxRepeats updates the live repeat threshold for the streaming
// text loop detector. Values below 2 restore the default; called when
// execution.stream_loop_max_repeats changes via /config set.
func (ld *LoopDetector) SetStreamMaxRepeats(n int) {
	ld.mu.Lock()
	defer ld.mu.Unlock()
	if n < 2 {
		n = defaultStreamLoopMaxRepeats
	}
	ld.maxStreamRepeats = n
}

// SetLoopThresholds updates the live tool-loop warning/interrupt thresholds.
// Non-positive values are ignored: a zero threshold would trip on the first
// recorded call, so it is never a legitimate configuration. Called when
// execution.loop_warning / execution.loop_interrupt change via /config set.
func (ld *LoopDetector) SetLoopThresholds(warning, interrupt int) {
	ld.mu.Lock()
	defer ld.mu.Unlock()
	if warning > 0 {
		ld.loopWarningThreshold = warning
	}
	if interrupt > 0 {
		ld.loopInterruptThreshold = interrupt
	}
}

// RecordToolCall records a tool call and checks for loop patterns.
// Returns a warning level: LoopOK (normal), LoopWarning, or LoopInterrupt.
// Returns LoopOK immediately when tool-loop detection is disabled (either by
// config or by session-level temp override).
//
// The count is a CONSECUTIVE streak: when the incoming call differs from the
// previous one, all streaks reset — a real loop never alternates, while
// legitimate long sessions reuse identical commands with other work in
// between (see the struct comment for the false-positive incident).
func (ld *LoopDetector) RecordToolCall(name, input string) LoopWarningLevel {
	ld.mu.Lock()
	defer ld.mu.Unlock()

	if ld.tempToolDisabled || ld.persistToolDisabled {
		return LoopOK
	}

	key := name + ":" + hashInput(input)
	if key != ld.lastToolKey {
		// A different call broke the run: every streak starts over. With
		// exactly one live streak at a time, an alternating A-B-A-B cycle
		// keeps each streak at 1 — detectable only with pair tracking, a
		// deliberate trade-off documented on the struct (back-to-back
		// repetition is the observed runaway signature).
		ld.toolStreaks = make(map[string]int)
		ld.lastToolKey = key
	}
	ld.toolStreaks[key]++

	count := ld.toolStreaks[key]
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

// stripCodeBlocks removes fenced code blocks (```...```) from text.
func stripCodeBlocks(text string) string {
	var result strings.Builder
	result.Grow(len(text))
	for {
		start := strings.Index(text, "```")
		if start < 0 {
			result.WriteString(text)
			break
		}
		result.WriteString(text[:start])
		text = text[start+3:]
		// Find closing ```
		end := strings.Index(text, "```")
		if end < 0 {
			// No closing fence — keep rest as-is
			result.WriteString(text)
			break
		}
		text = text[end+3:]
	}
	return result.String()
}

// stripXMLBlock strips all occurrences of an XML block with the given tag.
// Returns the text with all <tag>...</tag> blocks removed.
func stripXMLBlock(text, tag, endTag string) string {
	for {
		start := strings.Index(text, tag)
		if start < 0 {
			break
		}
		end := strings.Index(text[start:], endTag)
		if end < 0 {
			text = text[:start]
			break
		}
		text = text[:start] + text[start+end+len(endTag):]
	}
	return text
}

// isJSONToolCallStart reports whether a line looks like the start of a JSON
// tool call block (one of the known tool call keys).
func isJSONToolCallStart(trimmed string) bool {
	return strings.HasPrefix(trimmed, `{"name":`) ||
		strings.HasPrefix(trimmed, `{"function":`) ||
		strings.HasPrefix(trimmed, `{"tool_name":`)
}

// stripToolCallBlocks strips tool call blocks from reasoning text. These are
// blocks that look like XML tool_use elements or JSON tool-call structures.
func stripToolCallBlocks(text string) string {
	text = stripXMLBlock(text, "<tool_use>", "</tool_use>")
	text = stripXMLBlock(text, "<function_call>", "</function_call>")
	return stripJSONToolCalls(text)
}

// stripJSONToolCalls removes JSON tool call blocks ({"name": ..., {"function": ...,
// {"tool_name": ...) from text by tracking brace depth across lines.
func stripJSONToolCalls(text string) string {
	lines := strings.Split(text, "\n")
	if len(lines) <= 1 {
		return text
	}
	var kept []string
	inJSONBlock := false
	braceDepth := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !inJSONBlock && isJSONToolCallStart(trimmed) {
			inJSONBlock = true
			braceDepth = countBraceDepth(trimmed)
			continue
		}
		if inJSONBlock {
			braceDepth += countBraceDepth(trimmed)
			if braceDepth <= 0 {
				inJSONBlock = false
			}
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// countBraceDepth returns the net brace depth change of a line.
func countBraceDepth(s string) int {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
		}
	}
	return depth
}

// wordCount returns the number of whitespace-separated words in s.
func wordCount(s string) int {
	if len(s) == 0 {
		return 0
	}
	count := 1
	inWord := false
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r' {
			inWord = false
		} else if !inWord {
			count++
			inWord = true
		}
	}
	return count
}

// SetTempOverride sets a session-level temporary override for loop detection.
// When disabled is true, the detection is disabled. These overrides are not
// persisted and are cleared when the session ends or on Reset().
func (ld *LoopDetector) SetTempOverride(kind string, disabled bool) {
	ld.mu.Lock()
	defer ld.mu.Unlock()
	switch kind {
	case "think":
		ld.tempThinkDisabled = disabled
	case "tool":
		ld.tempToolDisabled = disabled
	case "stream":
		ld.tempStreamDisabled = disabled
	case "stall":
		ld.tempStallDisabled = disabled
	}
}

// SetPersistOverride sets the persistent (config-saved) override for loop
// detection. This is applied across sessions until the config is changed.
func (ld *LoopDetector) SetPersistOverride(kind string, disabled bool) {
	ld.mu.Lock()
	defer ld.mu.Unlock()
	switch kind {
	case "think":
		ld.persistThinkDisabled = disabled
	case "tool":
		ld.persistToolDisabled = disabled
	case "stream":
		ld.persistStreamDisabled = disabled
	case "stall":
		ld.persistStallDisabled = disabled
	}
}

// TempOverride returns the current temp override state for the given kind.
func (ld *LoopDetector) TempOverride(kind string) bool {
	ld.mu.Lock()
	defer ld.mu.Unlock()
	switch kind {
	case "think":
		return ld.tempThinkDisabled
	case "tool":
		return ld.tempToolDisabled
	case "stream":
		return ld.tempStreamDisabled
	case "stall":
		return ld.tempStallDisabled
	}
	return false
}

// Disabled reports whether detection is effectively off for the given kind,
// whether by a session-level temp override or a persisted config override.
func (ld *LoopDetector) Disabled(kind string) bool {
	ld.mu.Lock()
	defer ld.mu.Unlock()
	switch kind {
	case "think":
		return ld.tempThinkDisabled || ld.persistThinkDisabled
	case "tool":
		return ld.tempToolDisabled || ld.persistToolDisabled
	case "stream":
		return ld.tempStreamDisabled || ld.persistStreamDisabled
	case "stall":
		return ld.tempStallDisabled || ld.persistStallDisabled
	}
	return false
}

// processThinkingLine strips code/tool blocks, checks word count, and returns
// the cleaned line (or empty if it should be skipped).
func processThinkingLine(line string) string {
	line = stripCodeBlocks(line)
	line = stripToolCallBlocks(line)
	line = strings.TrimSpace(line)
	if wordCount(line) < minThinkWordCount {
		return ""
	}
	return line
}

// RecordThinkingDelta accumulates streamed reasoning text and detects when the
// assistant repeats the same line of thought within a turn. It returns
// LoopInterrupt when a significant line repeats beyond the interrupt threshold,
// LoopWarning beyond the warning threshold, and LoopOK otherwise. Returns
// LoopOK immediately when thinking-loop detection is disabled (either by
// config or by session-level temp override). Complete (newline-terminated)
// lines are evaluated incrementally; code blocks and tool call blocks are
// stripped before analysis. Lines with fewer than minThinkWordCount words are
// ignored to avoid false positives.
func (ld *LoopDetector) RecordThinkingDelta(text string) LoopWarningLevel {
	if text == "" {
		return LoopOK
	}
	ld.mu.Lock()
	defer ld.mu.Unlock()

	if ld.tempThinkDisabled || ld.persistThinkDisabled {
		return LoopOK
	}

	ld.thinkPending += text
	for {
		idx := indexByte(ld.thinkPending, '\n')
		if idx < 0 {
			break
		}
		raw := trimSpace(ld.thinkPending[:idx])
		ld.thinkPending = ld.thinkPending[idx+1:]

		if ld.skipCodeFenceLine(raw) {
			continue
		}

		// Diversity tracking runs on the raw prose line (normalized), BEFORE the
		// minThinkWordCount filter drops short lines — a short-line loop is still
		// a loop (Issue 6). Code/tool blocks and structural lines are
		// excluded so legitimate output never lowers diversity.
		ld.trackProseForDiversity(raw)

		line := processThinkingLine(raw)
		if line == "" {
			continue
		}
		if isStructuralLine(line) {
			continue
		}
		h := hashInput(line)
		ld.thinkLineCounts[h]++
		if c := ld.thinkLineCounts[h]; c > ld.thinkMaxRepeat {
			ld.thinkMaxRepeat = c
		}
	}

	return ld.thinkLevel()
}

// thinkLevel combines the exact-line counter and the diversity-based detector
// into a single warning level. Either path may escalate; the higher wins.
func (ld *LoopDetector) thinkLevel() LoopWarningLevel {
	exact := LoopOK
	switch {
	case ld.thinkMaxRepeat >= ld.thinkInterruptThreshold:
		exact = LoopInterrupt
	case ld.thinkMaxRepeat >= ld.thinkWarningThreshold:
		exact = LoopWarning
	}
	if d := ld.diversityLevel(); d > exact {
		return d
	}
	return exact
}

// normalizeProseLine reduces a reasoning line to a canonical form for
// diversity comparison: lowercase, punctuation stripped, whitespace collapsed.
// This lets "Let me check the full file." and "Let me check the full file,"
// (and case variants) count as the same line, so near-identical filler is
// recognized as repetition.
func normalizeProseLine(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	b.Grow(len(s))
	lastSpace := true // collapse leading whitespace too
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			lastSpace = false
		case r == ' ' || r == '\t':
			if !lastSpace {
				b.WriteByte(' ')
			}
			lastSpace = true
		default:
			// drop punctuation and other symbols
		}
	}
	return trimSpace(b.String())
}

// trackProseForDiversity records a raw reasoning line in the sliding diversity
// window when it is prose (non-structural, non code/tool). Structural lines and
// lines that are pure code/markup are skipped so legitimate iterative output
// does not look like a low-diversity loop.
func (ld *LoopDetector) trackProseForDiversity(raw string) {
	cleaned := stripToolCallBlocks(stripCodeBlocks(raw))
	cleaned = trimSpace(cleaned)
	if cleaned == "" {
		return
	}
	if isStructuralLine(cleaned) {
		return
	}
	norm := normalizeProseLine(cleaned)
	if norm == "" {
		return
	}
	// Require a minimum of substance so list markers ("- yes", "1. no"), bullets
	// and short genuine sentences that legitimately recur across iterative code
	// quotes (a real reasoning sentence repeated while re-quoting a
	// code block is NOT a loop) do not dominate the window. Five words is low
	// enough to capture short filler loops ("let me check the full file") while
	// leaving longer genuine reasoning to the exact-line counter.
	if wordCount(norm) < 5 {
		return
	}
	ld.pushRecentLine(norm)
}

// pushRecentLine appends a normalized line to the sliding window, evicting the
// oldest when full, and maintains the distinct-count map.
func (ld *LoopDetector) pushRecentLine(norm string) {
	if ld.thinkRecentLen == thinkWindowSize {
		evict := ld.thinkRecentLines[ld.thinkRecentHead]
		if c := ld.thinkRecentCounts[evict]; c <= 1 {
			delete(ld.thinkRecentCounts, evict)
		} else {
			ld.thinkRecentCounts[evict] = c - 1
		}
		ld.thinkRecentLines[ld.thinkRecentHead] = norm
		ld.thinkRecentHead = (ld.thinkRecentHead + 1) % thinkWindowSize
		ld.thinkRecentCounts[norm]++
		ld.thinkWindowFull = true
		return
	}
	ld.thinkRecentLines[ld.thinkRecentLen] = norm
	ld.thinkRecentLen++
	ld.thinkRecentCounts[norm]++
	if ld.thinkRecentLen >= thinkMinWindowFill {
		ld.thinkWindowFull = true
	}
}

// diversityLevel reports a loop level based on how few DISTINCT normalized
// lines occupy the recent window. Once the window has enough volume
// (thinkWindowFull), a distinct count at or below thinkMaxDistinctLines means
// the model is recycling a tiny set of phrasings — an alternating or
// short-line loop that exact-line matching cannot see.
func (ld *LoopDetector) diversityLevel() LoopWarningLevel {
	if !ld.thinkWindowFull {
		return LoopOK
	}
	distinct := len(ld.thinkRecentCounts)
	// Diversity detection targets CYCLING among a small set of phrasings (the
	// A/B alternation that per-line exact matching cannot see, Issue 6)
	// and the single-SHORT-line loop that falls under the exact counter's
	// minThinkWordCount floor. A single repeated LONG genuine sentence is left
	// to the exact-line counter (one real reasoning line repeated while
	// re-quoting code is not a loop — fence false positive).
	if distinct == 1 {
		if !ld.singleShortLineLoop() {
			return LoopOK
		}
	} else if distinct > thinkMaxDistinctLines {
		return LoopOK
	}
	// Low diversity over a full window. Interrupt when the window is completely
	// saturated (thinkRecentLen reached the cap) with a tiny phrase set; warn
	// once it is merely filled past the arming point.
	if ld.thinkRecentLen >= thinkWindowSize {
		return LoopInterrupt
	}
	return LoopWarning
}

// singleShortLineLoop reports whether the window is dominated by one repeated
// short line (fewer than minThinkWordCount words, so the exact-line counter
// never sees it) recurring CONSECUTIVELY. A genuine reasoning sentence repeated
// across iterative code quotes is interleaved with the (skipped) fenced content
// and other prose, so its occurrences are not back-to-back; a filler loop
// repeats the same short line with nothing in between (Issue 6). We
// detect the loop only when the single short line also forms a long run.
func (ld *LoopDetector) singleShortLineLoop() bool {
	for line := range ld.thinkRecentCounts {
		// Only one entry exists (distinct == 1). Act only when that single
		// recycled line is short enough to escape the exact counter.
		return wordCount(line) < minThinkWordCount
	}
	return false
}

// skipCodeFenceLine tracks ```-fenced code blocks across lines and reports
// whether the given line is inside one (or is a fence boundary) and must be
// skipped. A fence marker line toggles thinkInCodeBlock and is itself skipped;
// while inside a block every line is skipped until the closing marker. A line
// that opens and closes a fence on the same line (```code```) is skipped
// without changing the state. This runs BEFORE processThinkingLine so quoted
// code never reaches the repetition counter.
func (ld *LoopDetector) skipCodeFenceLine(line string) bool {
	fences := strings.Count(line, "```")
	if fences == 0 {
		return ld.thinkInCodeBlock
	}
	if fences%2 == 0 {
		// Even number of fence markers on one line: any blocks open and close
		// within the line — skip the line, state unchanged.
		return true
	}
	// Odd count: the block state flips. Skip the boundary line either way.
	ld.thinkInCodeBlock = !ld.thinkInCodeBlock
	return true
}

// ResetThinking clears the per-turn thinking accumulation so each assistant
// turn is evaluated independently. Called by the AgentManager on turn finalize.
func (ld *LoopDetector) ResetThinking() {
	ld.mu.Lock()
	defer ld.mu.Unlock()
	ld.thinkPending = ""
	ld.thinkLineCounts = make(map[string]int)
	ld.thinkMaxRepeat = 0
	ld.thinkInCodeBlock = false
	// Reset diversity-tracking state so the next turn is evaluated fresh and a
	// latched low-diversity window cannot kill the first delta of a new turn.
	ld.thinkRecentLines = make([]string, thinkWindowSize)
	ld.thinkRecentCounts = make(map[string]int)
	ld.thinkRecentHead = 0
	ld.thinkRecentLen = 0
	ld.thinkWindowFull = false
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
// Per-session temp overrides (TempThinkDisabled, TempToolDisabled) are
// preserved across resets so a single /config:temp command disables detection
// for the entire session until the user re-enables it.
func (ld *LoopDetector) Reset() {
	ld.mu.Lock()
	defer ld.mu.Unlock()

	ld.toolStreaks = make(map[string]int)
	ld.lastToolKey = ""
	ld.errorHistory = make([]bool, len(ld.errorHistory))
	ld.errorIdx = 0
	ld.thinkPending = ""
	ld.thinkLineCounts = make(map[string]int)
	ld.thinkMaxRepeat = 0
}

// isStructuralLine reports whether a line looks like a code, JSON, or XML
// structural element that legitimately repeats during reasoning (function
// signatures/calls, keywords, braces, tags, assignments). Such lines are
// excluded from thinking-loop counting to avoid false positives when the model
// iterates over code structure.
func isStructuralLine(line string) bool {
	s := trimSpace(line)
	if len(s) == 0 {
		return false
	}

	// Structural punctuation at the start of a line.
	switch s[0] {
	case '{', '}', '[', ']', '(', ')', '<', '>', '"', '\'', '`', '/', '\\':
		return true
	}

	// Common programming-language keywords at the start of a line. Declarative
	// keywords that collide with common English prose openers ("let me", "do
	// not", "new information", "type of", "final answer") are intentionally
	// omitted: treating them as code disabled thinking-loop detection for the
	// most frequent reasoning-filler prefixes (Issue 6 — the
	// "Let me check/read the full file." loop went undetected because every
	// "Let me …" line matched the JS "let " keyword). Go declarations are still
	// caught by startsWithIdentifierAndCode below; other languages' code is
	// expected inside ``` fences, which skipCodeFenceLine handles separately.
	keywords := []string{
		"func ", "def ", "class ", "interface ", "struct ", "enum ", "union ", "typedef ",
		"package ", "import ", "const ", "var ",
		"public ", "private ", "protected ", "static ", "void ",
		"return ", "if ", "else ", "for ", "while ", "switch ", "case ", "default ", "break ", "continue ",
		"try ", "catch ", "finally ", "throw ", "delete ", "async ", "await ",
		"function ", "module ", "export ", "extends ", "implements ",
		"namespace ", "include ", "require ",
	}
	lower := strings.ToLower(s)
	for _, kw := range keywords {
		if strings.HasPrefix(lower, kw) {
			return true
		}
	}

	// Identifier followed by code syntax: function call, assignment, or
	// type/key annotation (e.g. "writeFmt(...)", "x := 5", "key: value").
	return startsWithIdentifierAndCode(s)
}

// startsWithIdentifierAndCode reports whether s starts with an identifier
// (letter/underscore followed by word characters) immediately followed by a
// structural code operator: '(', ':=', '=', or ':'.
func startsWithIdentifierAndCode(s string) bool {
	if len(s) == 0 || !isIdentStart(s[0]) {
		return false
	}
	i := 1
	for i < len(s) && isIdentCont(s[i]) {
		i++
	}
	for i < len(s) && isSpace(s[i]) {
		i++
	}
	if i >= len(s) {
		return false
	}
	return isCodeOp(s[i], i+1 < len(s), s[i+1:])
}

// isCodeOp reports whether the byte at the end of an identifier introduces a
// code construct: function call '(', key/type annotation ':', assignment '=',
// or Go short variable declaration ':='.
func isCodeOp(b byte, hasRest bool, rest string) bool {
	switch b {
	case '(':
		return true
	case ':':
		// "key: value" annotation or Go "x := value" declaration.
		return hasRest && (isSpace(rest[0]) || rest[0] == '=')
	case '=':
		return !hasRest || rest[0] != '='
	}
	return false
}

func isIdentStart(b byte) bool { return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b == '_' }
func isIdentCont(b byte) bool  { return isIdentStart(b) || (b >= '0' && b <= '9') }

// hashInput creates a deterministic hash of the tool input for loop detection.
func hashInput(input string) string {
	h := sha256.Sum256([]byte(input))
	return fmt.Sprintf("%x", h[:8]) // first 8 hex chars is sufficient
}
