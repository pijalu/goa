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
		{"case folded", "Hello WORLD Café", "hello world café"},
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

func TestStreamLoopScan_ShortTextNeverDetects(t *testing.T) {
	texts := []string{
		"",
		"hello world",
		"hello world hello world hello", // 29 < 20*2
		"a",
	}
	for _, text := range texts {
		for _, threshold := range []int{2, 3, 5} {
			if streamLoopWouldDetect(text, threshold) {
				t.Errorf("short text %q detected as loop at threshold %d", text, threshold)
			}
		}
	}
}

func TestStreamLoopIntegration(t *testing.T) {
	t.Run("box drawing not detected as loop", func(t *testing.T) {
		text := "╭───╮\n│ a │\n╰───╯"
		clean := streamLoopNormalize(text)
		if clean != "a" {
			t.Errorf("box drawing normalized to %q, want 'a'", clean)
		}
		if streamLoopWouldDetect(clean, 2) {
			t.Error("box drawing should not trigger loop detection")
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
		if streamLoopWouldDetect(buf.String(), 3) {
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
		if streamLoopWouldDetect(buf.String(), 3) {
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
			if streamLoopWouldDetect(tt.text, 5) {
				t.Errorf("false positive loop detection on %q", tt.text)
			}
		})
	}
}

// streamLoopWouldDetect reports whether checkStreamLoop would set
// streamLoopDetected for the given buffer (exercises the production scan)
// with the given repeat threshold.
func streamLoopWouldDetect(text string, maxRepeats int) bool {
	_, _, _, ok := streamLoopScan(streamLoopNormalize(text), maxRepeats, streamLoopExactMinPeriod)
	return ok
}

func assertSingleWordRepeatNotDetected(t *testing.T) {
	t.Helper()
	text := "the the the the the the the the the the the the the the the the the the"
	for _, threshold := range []int{2, 3, 5} {
		if streamLoopWouldDetect(text, threshold) {
			t.Errorf("single-word repeat should not trigger loop detection at threshold %d", threshold)
		}
	}
}

func assertMultiWordRepeatDetected(t *testing.T) {
	t.Helper()
	text := "the quick brown the quick brown the quick brown the quick brown " +
		"the quick brown the quick brown the quick brown the quick brown"
	if !streamLoopWouldDetect(text, 3) {
		t.Error("genuine multi-word loop should be detected")
	}
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
