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
			gotMin, gotMax := streamLoopWindowRange(tt.text)
			if !tt.wantOk {
				if gotMin != 0 || gotMax != 0 {
					t.Errorf("streamLoopWindowRange() = (%d, %d), want (0, 0)", gotMin, gotMax)
				}
				return
			}
			// min should always be 20
			if gotMin != 20 {
				t.Errorf("streamLoopWindowRange() min = %d, want %d", gotMin, 20)
			}
			// max should be len(text)/2, capped at 120, clamped to ≥ minWindow
			expectedMax := len(tt.text) / 2
			if expectedMax > 120 {
				expectedMax = 120
			}
			if gotMax != expectedMax {
				t.Errorf("streamLoopWindowRange() max = %d, want %d (len=%d/2=%d, capped=120)",
					gotMax, expectedMax, len(tt.text), len(tt.text)/2)
			}
		})
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
	// Test full pipeline: checkStreamLoop via the pure functions it calls.
	// We can't easily instantiate a real Agent in this test file due to its
	// many dependencies, so we verify the building blocks end-to-end.

	t.Run("box drawing not detected as loop", func(t *testing.T) {
		// Box-drawing characters like ─ ┐ └ │ should be stripped by normalize
		text := "╭───╮\n│ a │\n╰───╯"
		clean := streamLoopNormalize(text)
		if clean != "a" {
			t.Errorf("box drawing normalized to %q, want 'a'", clean)
		}
		// Empty enough that loop detection doesn't trigger
		minW, _ := streamLoopWindowRange(clean)
		if minW != 0 {
			t.Errorf("box drawing should not trigger window range, got minWindow=%d", minW)
		}
	})

	t.Run("single word repeat not detected", func(t *testing.T) {
		// Even if repeated, a single word should not trigger
		text := "the the the the the the the the the the the the the the the the the the"
		clean := streamLoopNormalize(text)
		minW, maxW := streamLoopWindowRange(clean)
		if minW == 0 {
			t.Fatal("should have valid window range")
		}
		// Check that each window fails the word-count check
		for window := minW; window <= maxW; window++ {
			suffix := clean[len(clean)-window:]
			if streamHasMultipleUniqueWords(suffix) {
				// Only fail if streamHasRepeatedSuffix also matches
				repeats := streamLoopRepeatsNeeded(window)
				if streamHasRepeatedSuffix(clean, window, repeats) {
					t.Errorf("single-word repeat should not trigger loop detection at window=%d, suffix=%q", window, suffix)
				}
			}
		}
	})

	t.Run("multi-word repeat detected", func(t *testing.T) {
		// A genuine multi-word loop should be detected
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
	})
}

// repeatString returns s repeated n times.
func repeatString(s string, n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(s)
	}
	return b.String()
}
