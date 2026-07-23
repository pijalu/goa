// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"strings"
	"testing"
)

// mockTool is a simple tool for testing
type mockTool struct {
	name   string
	schema ToolSchema
}

func (m mockTool) Schema() ToolSchema {
	return m.schema
}

func (m mockTool) Execute(input string) (string, error) {
	return "mock result", nil
}
func (m mockTool) IsRetryable(err error) bool { return false }

// hugeResultTool returns a result larger than the configured context limit.
type hugeResultTool struct {
	name   string
	schema ToolSchema
	size   int
}

func (m hugeResultTool) Schema() ToolSchema { return m.schema }
func (m hugeResultTool) Execute(input string) (string, error) {
	return strings.Repeat("x", m.size), nil
}
func (m hugeResultTool) IsRetryable(err error) bool { return false }

func TestToolRegistry_Get(t *testing.T) {
	registry := NewToolRegistry([]Tool{
		mockTool{name: "test_tool", schema: ToolSchema{Name: "test_tool", Description: "test"}},
	})

	tool, ok := registry.Get("test_tool")
	if !ok {
		t.Fatal("expected to find 'test_tool' tool")
	}

	schema := tool.Schema()
	if schema.Name != "test_tool" {
		t.Errorf("expected name 'test_tool', got '%s'", schema.Name)
	}
}

func TestToolRegistry_GetNotFound(t *testing.T) {
	registry := NewToolRegistry([]Tool{})

	_, ok := registry.Get("nonexistent")
	if ok {
		t.Error("expected not found")
	}
}

func TestToolRegistry_Schemas(t *testing.T) {
	registry := NewToolRegistry([]Tool{
		mockTool{name: "test_tool", schema: ToolSchema{Name: "test_tool", Description: "test"}},
	})

	schemas := registry.Schemas()
	if len(schemas) != 1 {
		t.Fatalf("expected 1 schema, got %d", len(schemas))
	}
	if schemas[0].Name != "test_tool" {
		t.Errorf("expected 'test_tool', got '%s'", schemas[0].Name)
	}
}

func TestToolRegistry_Schemas_Ordered(t *testing.T) {
	registry := NewToolRegistry([]Tool{
		mockTool{name: "gamma", schema: ToolSchema{Name: "gamma", Description: "g"}},
		mockTool{name: "alpha", schema: ToolSchema{Name: "alpha", Description: "a"}},
		mockTool{name: "beta", schema: ToolSchema{Name: "beta", Description: "b"}},
	})

	want := []string{"alpha", "beta", "gamma"}
	for attempt := 0; attempt < 3; attempt++ {
		schemas := registry.Schemas()
		if len(schemas) != len(want) {
			t.Fatalf("attempt %d: expected %d schemas, got %d", attempt, len(want), len(schemas))
		}
		for i, wantName := range want {
			if schemas[i].Name != wantName {
				t.Errorf("attempt %d: schemas[%d].Name = %q, want %q", attempt, i, schemas[i].Name, wantName)
			}
		}
	}
}

func TestValidate_ValidInput(t *testing.T) {
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"a": map[string]string{"type": "number"},
			"b": map[string]string{"type": "number"},
		},
		"required": []string{"a", "b"},
	}

	err := Validate(schema, `{"a": 10, "b": 20}`)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestValidate_InvalidInput(t *testing.T) {
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"a": map[string]string{"type": "number"},
			"b": map[string]string{"type": "number"},
		},
		"required": []string{"a", "b"},
	}

	err := Validate(schema, `{"a": 10}`)
	if err == nil {
		t.Error("expected error for missing required field")
	}
}

func TestValidate_InvalidJSON(t *testing.T) {
	schema := map[string]interface{}{
		"type": "object",
	}

	err := Validate(schema, `not valid json`)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

// Test a mock calculator tool
type testCalculator struct{}

func (c testCalculator) Schema() ToolSchema {
	return ToolSchema{
		Name:        "calculator",
		Description: "math operations",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"a":  map[string]string{"type": "number"},
				"b":  map[string]string{"type": "number"},
				"op": map[string]string{"type": "string"},
			},
			"required": []string{"a", "b", "op"},
		},
	}
}

func (c testCalculator) Execute(input string) (string, error) {
	return "42", nil
}
func (c testCalculator) IsRetryable(err error) bool { return false }

func TestCalculator_Execute(t *testing.T) {
	calc := testCalculator{}

	result, err := calc.Execute(`{"a": 10, "b": 5, "op": "+"}`)
	if err != nil {
		t.Errorf("unexpected error %v", err)
	}
	if result != "42" {
		t.Errorf("expected '42', got '%s'", result)
	}
}

func TestCalculator_Schema(t *testing.T) {
	calc := testCalculator{}
	schema := calc.Schema()

	if schema.Name != "calculator" {
		t.Errorf("expected name 'calculator', got '%s'", schema.Name)
	}
	if schema.Description != "math operations" {
		t.Errorf("expected description 'math operations', got '%s'", schema.Description)
	}
}

// ── Stream loop tests ──

func TestStreamLoopNormalize(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{"empty", "", ""},
		{"only punctuation", "!!! ┌─┐ │ └─┘ ╭─╮ ╰─╯", ""},
		{"normal text preserved", "hello world", "hello world"},
		{"punctuation stripped", "the, quick! brown? fox.", "the quick brown fox"},
		{"box drawing stripped", "│the│ ┌quick┐ brown", "the quick brown"},
		{"digits kept", "line 1 line 2", "line 1 line 2"},
		{"multiple spaces collapsed", "hello    world", "hello world"},
		{"leading/trailing spaces trimmed", "  hello world  ", "hello world"},
		{"newlines and tabs become spaces", "hello\nworld\tfoo", "hello world foo"},
		{"mixed unicode letters", "café résumé", "café résumé"},
		{"symbols stripped", "@#$%^&*()_+-=[]{}|;':\",./<>?" + "`~", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := streamLoopNormalize(tt.text)
			if got != tt.want {
				t.Errorf("streamLoopNormalize() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStreamHasMultipleUniqueWords(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{"empty", "", false},
		{"single word", "hello", false},
		{"same word repeated", "the the the the", false},
		{"same word twice", "hello hello", false},
		{"two different words", "hello world", true},
		{"multiple words some repeat", "the quick the quick", true},
		{"multiple unique words", "the quick brown fox", true},
		{"single char different", "a b", true},
		{"digits same", "123 123 123", false},
		{"digits different", "123 456", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := streamHasMultipleUniqueWords(tt.text)
			if got != tt.want {
				t.Errorf("streamHasMultipleUniqueWords(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

func TestStreamLoopWindowRange(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		wantOk bool
	}{
		{"empty", "", false},
		{"too short", "hello world", false},                                         // 11 < 40 (20*2)
		{"just enough", "hello world hello world hello world hello", true},         // 45 chars
		{"long text", "the quick brown fox jumps over the lazy dog " +
			"the quick brown fox jumps over the lazy dog the quick", true},
		{"very long", string(repeatString("hello ", 100)), true},                   // capped at 120
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertWindowRange(t, tt.text, tt.wantOk)
		})
	}
}

func assertWindowRange(t *testing.T, text string, wantOk bool) {
	t.Helper()
	gotMin, gotMax := streamLoopWindowRange(text)
	if !wantOk {
		if gotMin != 0 || gotMax != 0 {
			t.Errorf("streamLoopWindowRange() = (%d, %d), want (0, 0)", gotMin, gotMax)
		}
		return
	}
	if gotMin != 20 {
		t.Errorf("streamLoopWindowRange() min = %d, want %d", gotMin, 20)
	}
	expectedMax := len(text) / 2
	if expectedMax > 120 {
		expectedMax = 120
	}
	if gotMax != expectedMax {
		t.Errorf("streamLoopWindowRange() max = %d, want %d (len=%d/2=%d, capped=120)",
			gotMax, expectedMax, len(text), len(text)/2)
	}
}

func TestStreamHasRepeatedSuffix(t *testing.T) {
	tests := []struct {
		name          string
		text          string
		window        int
		repeatsNeeded int
		want          bool
	}{
		{"too short", "abc", 5, 3, false},
		{"no repeat", "hello world how are you", 5, 2, false},
		{"simple repeat", "abcabc", 3, 2, true},
		{"triple repeat", "abcabcabc", 3, 3, true},
		{"not enough repeats", "abcabcx", 3, 3, false},
		{"longer repeat", "thequickthequickthequick", 8, 3, true},
		{"hello repeated twice", "hellohello", 5, 2, true},
		{"no match due to change", "hello world goodbye", 5, 2, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := streamHasRepeatedSuffix(tt.text, tt.window, tt.repeatsNeeded)
			if got != tt.want {
				t.Errorf("streamHasRepeatedSuffix(%q, %d, %d) = %v, want %v",
					tt.text, tt.window, tt.repeatsNeeded, got, tt.want)
			}
		})
	}
}

func TestStreamLoopIntegration(t *testing.T) {
	t.Run("box drawing not detected as loop", func(t *testing.T) {
		text := "╭───╮\n│ a │\n╰───╯"
		clean := streamLoopNormalize(text)
		if clean != "a" {
			t.Errorf("box drawing normalized to %q, want 'a'", clean)
		}
		minW, _ := streamLoopWindowRange(clean)
		if minW != 0 {
			t.Errorf("box drawing should not trigger window range, got minWindow=%d", minW)
		}
	})

	t.Run("single word repeat not detected", func(t *testing.T) {
		assertSingleWordRepeatNotDetected(t)
	})

	t.Run("multi-word repeat detected", func(t *testing.T) {
		assertMultiWordRepeatDetected(t)
	})
}

// TestStreamLoop_NonIdenticalCopies is a regression test for a real incident
// (session export 2026-07-23): deepseek-v4-flash fell into a repetition loop
// and streamed the same paragraph four times. The copies were not byte
// identical — the blank line between copies two and three was missing — so
// the normalized text had a one-byte phase shift at that junction. The exact
// suffix matcher never fired and the full duplicated text streamed to the
// TUI, where the repeated markdown re-renders looked like screen corruption.
// The loop detector must catch this: stop the turn after the repeated copies,
// before the model rambles on.
func TestStreamLoop_NonIdenticalCopies(t *testing.T) {
	paragraph := "The project builds cleanly. Let me summarize the updates I made to HANDOVER.md:"
	// Exact delta sequence reconstructed from the exported session events:
	// copy1 + "\n\n", copy2 (no separator), copy3 + "\n\n", copy4 + "\n\n",
	// then the model finally moved on to the real content.
	copies := []string{
		paragraph + "\n\n",
		paragraph,
		paragraph + "\n\n",
		paragraph + "\n\n",
		"## Summary of Changes\n\nThe rest of the answer continues normally.",
	}

	// Feed the stream incrementally, checking after each delta exactly as
	// handleTextDelta does via checkStreamLoop. The detector must fire by the
	// time the third copy has streamed (long before the real content).
	var buf strings.Builder
	detectedAt := -1
	for i, chunk := range copies {
		buf.WriteString(chunk)
		if streamLoopWouldDetect(buf.String()) {
			detectedAt = i
			break
		}
	}
	if detectedAt < 0 {
		t.Fatal("loop not detected: four paragraph copies with a missing blank line escaped detection")
	}
	if detectedAt > 2 {
		t.Errorf("loop detected too late: fired at chunk %d, want by chunk 2 (third copy)", detectedAt)
	}

	// Stronger: feed the same stream in small token-sized fragments (as the
	// real SSE stream delivered them — "The", " project", " builds", …) and
	// verify detection still fires before the model moves on to new content.
	full := strings.Join(copies, "")
	buf.Reset()
	detectedAt = -1
	const fragSize = 9 // ~2 tokens, matching the provider's observed chunking
	for pos := 0; pos < len(full); pos += fragSize {
		end := pos + fragSize
		if end > len(full) {
			end = len(full)
		}
		buf.WriteString(full[pos:end])
		if streamLoopWouldDetect(buf.String()) {
			detectedAt = pos
			break
		}
	}
	if detectedAt < 0 {
		t.Fatal("loop not detected with token-sized deltas")
	}
	// Detection must happen before the fourth copy finishes streaming.
	fourthCopyEnd := len(copies[0]) + len(copies[1]) + len(copies[2]) + len(copies[3])
	if detectedAt >= fourthCopyEnd {
		t.Errorf("detected at byte %d, want before the fourth copy completes (%d)", detectedAt, fourthCopyEnd)
	}
}

// TestStreamLoop_NoFalsePositiveOnSimilarSentences guards the fuzzy matcher
// against flagging legitimate text: a paragraph followed by a similar (but
// not repeated) paragraph, and a repeated two-sentence answer ("say it
// twice" style) must not be treated as a loop.
func TestStreamLoop_NoFalsePositiveOnSimilarSentences(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{
			"two similar but distinct paragraphs",
			"The project builds cleanly. Let me summarize the updates I made to HANDOVER.md:\n\n" +
				"The project compiles without errors. Here is a summary of the changes made to README.md:\n\n" +
				"First, the repository structure section gained three new entries documenting the test layout.",
		},
		{
			"single paragraph followed by real content",
			"The project builds cleanly. Let me summarize the updates I made to HANDOVER.md:\n\n" +
				"## Summary of Changes\n\nI've updated the file from 393 to 462 lines with the following changes.",
		},
		{
			"short echoed phrase twice only",
			"Let me check the build output now. Let me check the build output now.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if streamLoopWouldDetect(tt.text) {
				t.Errorf("false positive loop detection on %q", tt.text)
			}
		})
	}
}

// streamLoopWouldDetect reports whether checkStreamLoop would set
// streamLoopDetected for the given buffer (exercises the production scan).
func streamLoopWouldDetect(text string) bool {
	_, _, ok := streamLoopScan(streamLoopNormalize(text))
	return ok
}

func assertSingleWordRepeatNotDetected(t *testing.T) {
	t.Helper()
	text := "the the the the the the the the the the the the the the the the the the"
	clean := streamLoopNormalize(text)
	minW, maxW := streamLoopWindowRange(clean)
	if minW == 0 {
		t.Fatal("should have valid window range")
	}
	for window := minW; window <= maxW; window++ {
		suffix := clean[len(clean)-window:]
		if streamHasMultipleUniqueWords(suffix) {
			repeats := streamLoopRepeatsNeeded(window)
			if streamHasRepeatedSuffix(clean, window, repeats) {
				t.Errorf("single-word repeat should not trigger loop detection at window=%d, suffix=%q", window, suffix)
			}
		}
	}
}

func assertMultiWordRepeatDetected(t *testing.T) {
	t.Helper()
	text := "the quick brown the quick brown the quick brown the quick brown " +
		"the quick brown the quick brown the quick brown the quick brown"
	clean := streamLoopNormalize(text)
	minW, maxW := streamLoopWindowRange(clean)
	if minW == 0 {
		t.Fatal("should have valid window range")
	}
	found := false
	for window := minW; window <= maxW; window++ {
		repeats := streamLoopRepeatsNeeded(window)
		if streamHasRepeatedSuffix(clean, window, repeats) {
			suffix := clean[len(clean)-window:]
			if streamHasMultipleUniqueWords(suffix) {
				found = true
				break
			}
		}
	}
	if !found {
		t.Error("genuine multi-word loop should be detected")
	}
}

// repeatString returns s repeated n times.
func repeatString(s string, n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(s)
	}
	return b.String()
}

// TestToolRegistry_Schemas_Cached verifies that Schemas() is computed once and
// cached (returns the same slice on subsequent calls). This is the contract the
// agent relies on: it calls Schemas() every stream round and retry.
func TestToolRegistry_Schemas_Cached(t *testing.T) {
	registry := NewToolRegistry([]Tool{
		mockTool{name: "alpha", schema: ToolSchema{Name: "alpha"}},
		mockTool{name: "beta", schema: ToolSchema{Name: "beta"}},
	})

	first := registry.Schemas()
	second := registry.Schemas()
	if len(first) != 2 {
		t.Fatalf("expected 2 schemas, got %d", len(first))
	}
	// Same backing array => cached, not recomputed/reallocated each call.
	if cap(first) != cap(second) || &first[0] != &second[0] {
		t.Error("Schemas() should return the cached slice on repeated calls")
	}
}

// loopAnnotatedTool is a test Tool that supplies loop-controller metadata.
type loopAnnotatedTool struct {
	mockTool
	hints ToolLoopHints
}

func (l loopAnnotatedTool) LoopHints() ToolLoopHints { return l.hints }

// TestToolRegistry_LoopHints_CollectsFromLoopAnnotated verifies the registry
// discovers LoopAnnotated tools and caches the result (wiring for the
// name-agnostic ToolLoopController).
func TestToolRegistry_LoopHints_CollectsFromLoopAnnotated(t *testing.T) {
	status := func(string) string { return "custom-status" }
	registry := NewToolRegistry([]Tool{
		mockTool{name: "plain", schema: ToolSchema{Name: "plain"}},
		loopAnnotatedTool{
			mockTool: mockTool{name: "special", schema: ToolSchema{Name: "special"}},
			hints:    ToolLoopHints{OneShot: true, HealArg: "code", Status: status},
		},
	})

	hints := registry.LoopHints()
	if len(hints) != 1 {
		t.Fatalf("expected 1 annotated tool, got %d", len(hints))
	}
	got, ok := hints["special"]
	if !ok {
		t.Fatal("missing hints for annotated tool 'special'")
	}
	if !got.OneShot || got.HealArg != "code" || got.Status == nil {
		t.Errorf("unexpected hints: %+v", got)
	}
	if _, present := hints["plain"]; present {
		t.Error("non-annotated tool should not appear in hints")
	}
	// Cached: second call returns the same map instance.
	if registry.LoopHints() == nil || len(registry.LoopHints()) != 1 {
		t.Error("LoopHints should be cached and stable")
	}
}
