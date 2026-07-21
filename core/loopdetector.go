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
	turnToolCalls          map[string]int // key: toolName+hash(input) → count
	loopWarningThreshold   int            // same tool call count before warning
	loopInterruptThreshold int            // same tool call count before interrupt

	// Thinking-loop tracking — drives RecordThinkingDelta loop detection.
	// Complete lines (terminated by '\n') are hashed and counted; only lines
	// with at least minThinkWordCount words are counted so short repeated
	// bullets or separators do not false-positive. Code blocks and tool call
	// blocks are stripped before processing. thinkMaxRepeat tracks the highest
	// count seen for any single line in the current turn.
	thinkPending          string
	thinkLineCounts       map[string]int
	thinkMaxRepeat        int
	thinkWarningThreshold int
	thinkInterruptThreshold int

	// Error tracking (ring buffer). Populated by RecordToolResult; retained as
	// the integration point for a future (genuinely wired) error-rate check.
	errorHistory []bool // last N tool results (true = error)
	errorIdx     int

	// tempThinkDisabled and tempToolDisabled are per-session temporary overrides
	// that disable loop detection without modifying the persisted config.
	// Set via /config:temp:think_loop_detection:off and
	// /config:temp:tool_loop_detection:off slash commands.
	tempThinkDisabled bool
	tempToolDisabled  bool

	// persistThinkDisabled and persistToolDisabled come from the persisted
	// config (execution.disable_thinking_loop_detection /
	// disable_tool_loop_detection) and disable detection across sessions.
	persistThinkDisabled bool
	persistToolDisabled  bool
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
}

// DefaultLoopDetectorConfig returns sensible defaults for the loop detector.
func DefaultLoopDetectorConfig() LoopDetectorConfig {
	return LoopDetectorConfig{
		LoopWarning:           7,
		LoopInterrupt:         10,
		ThinkingLoopWarning:   4,
		ThinkingLoopInterrupt: 6,
	}
}

const loopErrorHistorySize = 10

// minThinkWordCount is the minimum number of words a line of reasoning must
// have before it contributes to thinking-loop counting. This excludes short
// repeated constructs (list markers, separators, single words) that
// legitimately recur. Changed from a character-based threshold (40 chars) to
// a word-based threshold (10 words) to provide more meaningful filtering.
const minThinkWordCount = 10

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
		persistThinkDisabled:    cfg.ThinkingDisabled,
		persistToolDisabled:     cfg.ToolDisabled,
	}
}

// RecordToolCall records a tool call and checks for loop patterns.
// Returns a warning level: LoopOK (normal), LoopWarning, or LoopInterrupt.
// Returns LoopOK immediately when tool-loop detection is disabled (either by
// config or by session-level temp override).
func (ld *LoopDetector) RecordToolCall(name, input string) LoopWarningLevel {
	ld.mu.Lock()
	defer ld.mu.Unlock()

	if ld.tempToolDisabled || ld.persistToolDisabled {
		return LoopOK
	}

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
		line := trimSpace(ld.thinkPending[:idx])
		ld.thinkPending = ld.thinkPending[idx+1:]

		line = processThinkingLine(line)
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
// Per-session temp overrides (TempThinkDisabled, TempToolDisabled) are
// preserved across resets so a single /config:temp command disables detection
// for the entire session until the user re-enables it.
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

	// Common programming-language keywords at the start of a line.
	keywords := []string{
		"func ", "def ", "class ", "interface ", "struct ", "enum ", "union ", "typedef ",
		"package ", "import ", "const ", "var ", "let ", "type ", "val ", "final ",
		"public ", "private ", "protected ", "static ", "void ", "int ", "bool ", "string ",
		"return ", "if ", "else ", "for ", "while ", "switch ", "case ", "default ", "break ", "continue ",
		"try ", "catch ", "finally ", "throw ", "new ", "delete ", "async ", "await ",
		"function ", "module ", "export ", "from ", "extends ", "implements ",
		"namespace ", "using ", "include ", "require ", "end ", "do ", "begin ",
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
