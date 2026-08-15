// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		expected int // approximate; tests heuristic bounds
	}{
		{"empty", "", 0},
		{"ascii_short", "hello world", 3}, // 11 ascii × 10/33 ≈ 3
		{"ascii_long", strings.Repeat("a", 100), 30},
		{"cjk", "你好世界", 4},       // 4 CJK ≈ 4
		{"mixed", "hello 你好", 3}, // 6 ascii × 10/33 (=1) + 2 CJK ≈ 3
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := estimateTokens(tt.text)
			if got != tt.expected {
				t.Errorf("estimateTokens(%q) = %d, want %d", tt.text, got, tt.expected)
			}
		})
	}
}

func TestContextStats(t *testing.T) {
	agent := NewAgent(Config{
		SystemPrompt: strings.Repeat("You are helpful. ", 50), // longer system prompt
		ContextCompression: ContextCompressionConfig{
			MaxTokens: 100,
		},
	})

	// Add some history
	history := []Message{
		{Type: Content, Role: System, Content: agent.cfg.SystemPrompt},
		{Type: Content, Role: User, Content: "Hello!"},
		{Type: Content, Role: Assistant, Content: "Hi there!"},
	}
	agent.SetHistory(history)

	stats := agent.ContextStats()
	if stats.Messages != 3 {
		t.Errorf("Messages = %d, want 3", stats.Messages)
	}
	if stats.MaxTokens != 100 {
		t.Errorf("MaxTokens = %d, want 100", stats.MaxTokens)
	}
	if stats.UsagePercent == 0 {
		t.Error("UsagePercent should be > 0")
	}
}

// TestContextStats_UsesModelWindowWhenLarger verifies that when the model's
// advertised context window is larger than the configured compression limit,
// the displayed total reflects the actual model capacity. The compression
// threshold (MaxTokens) still drives proactive compression elsewhere.
func TestContextStats_UsesModelWindowWhenLarger(t *testing.T) {
	agent := NewAgent(Config{
		SystemPrompt: "You are helpful.",
		Model:        provider.Model{ContextWindow: 1_000_000},
		ContextCompression: ContextCompressionConfig{
			MaxTokens: 8192,
		},
	})
	agent.SetHistory([]Message{
		{Type: Content, Role: System, Content: "You are helpful."},
		{Type: Content, Role: User, Content: "hi"},
	})

	stats := agent.ContextStats()
	if stats.MaxTokens != 1_000_000 {
		t.Errorf("MaxTokens = %d, want 1_000_000 (model window)", stats.MaxTokens)
	}
	if !stats.AutoMax {
		t.Error("AutoMax should be true when display total comes from model metadata")
	}
}

// TestContextStats_RespectsModelWindowWhenExplicitMaxExceedsIt verifies that
// when the configured MaxTokens exceeds the model window, the displayed total
// still reflects the actual model capacity. The model cannot hold more than
// its advertised context window, so the UI should show that limit.
func TestContextStats_RespectsModelWindowWhenExplicitMaxExceedsIt(t *testing.T) {
	agent := NewAgent(Config{
		SystemPrompt: "You are helpful.",
		Model:        provider.Model{ContextWindow: 8192},
		ContextCompression: ContextCompressionConfig{
			MaxTokens: 100_000,
		},
	})
	agent.SetHistory([]Message{
		{Type: Content, Role: System, Content: "You are helpful."},
	})

	stats := agent.ContextStats()
	if stats.MaxTokens != 8192 {
		t.Errorf("MaxTokens = %d, want 8192 (model window)", stats.MaxTokens)
	}
	if !stats.AutoMax {
		t.Error("AutoMax should be true when display total comes from model metadata")
	}
}

func TestCompressToolElision(t *testing.T) {
	agent := NewAgent(Config{
		SystemPrompt: "You are helpful.",
		ContextCompression: ContextCompressionConfig{
			MaxTokens:           10000,
			ThresholdPercent:    50,
			Strategy:            CompressionToolElision,
			PreserveRecentTurns: 1,
		},
	})

	// Build history with multiple turns so older tool calls/results get elided
	history := []Message{
		{Type: Content, Role: System, Content: "You are helpful."},
		{Type: Content, Role: User, Content: "Run a command"},
		{
			Type:    Content,
			Role:    Assistant,
			Content: "",
			ToolCalls: []ToolCallInfo{
				{ID: "1", Type: "function", Name: "run_command", Arguments: `{"command":"echo hello"}`},
			},
		},
		{Type: Content, Role: ToolRole, Content: strings.Repeat("tool output ", 50), ToolCallID: "1"},
		{Type: Content, Role: User, Content: "Run another command"},
		{
			Type:    Content,
			Role:    Assistant,
			Content: "",
			ToolCalls: []ToolCallInfo{
				{ID: "2", Type: "function", Name: "run_command", Arguments: `{"command":"echo world"}`},
			},
		},
		{Type: Content, Role: ToolRole, Content: strings.Repeat("more output ", 50), ToolCallID: "2"},
		{Type: Content, Role: User, Content: "Thanks"},
		{Type: Content, Role: Assistant, Content: "You're welcome!"},
	}
	agent.SetHistory(history)

	// Trigger compression
	beforeTokens := estimateTokensFromHistory(history)
	agent.compressToolElision(false)
	newHistory := agent.GetHistory()
	afterTokens := estimateTokensFromHistory(newHistory)
	// System should be unchanged
	if newHistory[0].Content != "You are helpful." {
		t.Error("System message was modified")
	}

	// Old tool call arguments should be elided
	foundElided := false
	for _, m := range newHistory {
		for _, tc := range m.ToolCalls {
			if tc.Arguments == "[elided]" {
				foundElided = true
			}
		}
	}
	if !foundElided {
		t.Error("Tool call arguments were not elided")
	}

	// Old tool result should be elided
	foundElidedResult := false
	for _, m := range newHistory {
		if m.Role == ToolRole && strings.Contains(m.Content, "[tool result elided]") {
			foundElidedResult = true
		}
	}
	if !foundElidedResult {
		t.Error("Tool result was not elided")
	}

	// Token count should have decreased.
	if afterTokens >= beforeTokens {
		t.Errorf("Token count did not decrease: %d -> %d", beforeTokens, afterTokens)
	}

	// Recent assistant message should be untouched
	lastAssistant := newHistory[len(newHistory)-1]
	if lastAssistant.Content != "You're welcome!" {
		t.Error("Recent assistant message was modified")
	}
}

func TestCompressSelective(t *testing.T) {
	agent := NewAgent(Config{
		SystemPrompt: "You are helpful.",
		ContextCompression: ContextCompressionConfig{
			MaxTokens:           10000,
			ThresholdPercent:    50,
			Strategy:            CompressionSelective,
			PreserveRecentTurns: 1,
		},
	})

	history := []Message{
		{Type: Content, Role: System, Content: "You are helpful."},
		{Type: Content, Role: User, Content: "Question 1"},
		{Type: Content, Role: Assistant, Content: "Answer 1"},
		{Type: Content, Role: User, Content: "Question 2"},
		{Type: Content, Role: Assistant, Content: "Answer 2"},
	}
	agent.SetHistory(history)

	agent.compressSelective()

	newHistory := agent.GetHistory()
	// Should keep system + last turn
	if len(newHistory) != 3 {
		t.Errorf("Expected 3 messages, got %d", len(newHistory))
	}
	if newHistory[0].Role != System {
		t.Error("First message should be system")
	}
	if newHistory[1].Content != "Question 2" {
		t.Error("Expected to keep last user message")
	}
}

func TestCompressToolElision_ReducesTokensOnSmallHistory(t *testing.T) {
	agent := NewAgent(Config{
		SystemPrompt: "You are helpful.",
		ContextCompression: ContextCompressionConfig{
			MaxTokens:           100000,
			ThresholdPercent:    80,
			Strategy:            CompressionToolElision,
			PreserveRecentTurns: 2,
		},
	})

	// 10-message history matching the bug report shape: system, user, assistant
	// with tool call, large tool result, repeated a few times.
	history := []Message{
		{Type: Content, Role: System, Content: "You are helpful."},
		{Type: Content, Role: User, Content: "step 1"},
		{Type: Content, Role: Assistant, Content: "ok", ToolCalls: []ToolCallInfo{{ID: "1", Name: "run", Arguments: `{"command":"echo 1"}`}}},
		{Type: Content, Role: ToolRole, Content: strings.Repeat("output line one\n", 100), ToolCallID: "1"},
		{Type: Content, Role: User, Content: "step 2"},
		{Type: Content, Role: Assistant, Content: "ok", ToolCalls: []ToolCallInfo{{ID: "2", Name: "run", Arguments: `{"command":"echo 2"}`}}},
		{Type: Content, Role: ToolRole, Content: strings.Repeat("output line two\n", 100), ToolCallID: "2"},
		{Type: Content, Role: User, Content: "step 3"},
		{Type: Content, Role: Assistant, Content: "ok", ToolCalls: []ToolCallInfo{{ID: "3", Name: "run", Arguments: `{"command":"echo 3"}`}}},
		{Type: Content, Role: ToolRole, Content: strings.Repeat("output line three\n", 100), ToolCallID: "3"},
	}
	agent.SetHistory(history)

	before := agent.ContextStats().EstimatedTokens
	agent.compressToolElision(true)
	after := agent.ContextStats().EstimatedTokens

	if after >= before {
		t.Errorf("tool_elision did not reduce tokens: %d -> %d", before, after)
	}
	if after == 0 {
		t.Error("token count became zero unexpectedly")
	}
}

func TestCompressToolElision_ForcedSixMessages(t *testing.T) {
	agent := NewAgent(Config{
		SystemPrompt: "You are helpful.",
		ContextCompression: ContextCompressionConfig{
			MaxTokens:           100000,
			ThresholdPercent:    80,
			Strategy:            CompressionToolElision,
			PreserveRecentTurns: 2,
		},
	})

	// 6-message history: the large tool result sits right before the recent
	// assistant summary. Without the forced fallback, the boundary would be 1
	// and nothing would get elided.
	history := []Message{
		{Type: Content, Role: System, Content: "You are helpful."},
		{Type: Content, Role: User, Content: "read go.sum"},
		{Type: Content, Role: Assistant, Content: "", ToolCalls: []ToolCallInfo{{ID: "1", Name: "read", Arguments: `{"file_path":"go.sum","limit":500}`}}},
		{Type: Content, Role: ToolRole, Content: strings.Repeat("checksum line\n", 200), ToolCallID: "1"},
		{Type: Content, Role: User, Content: "thanks"},
		{Type: Content, Role: Assistant, Content: "done"},
	}
	agent.SetHistory(history)

	before := agent.ContextStats().EstimatedTokens
	agent.compressToolElision(true)
	after := agent.ContextStats().EstimatedTokens

	if after >= before {
		t.Errorf("forced tool_elision did not reduce tokens on 6-message history: %d -> %d", before, after)
	}
}

func TestMicroCompactForced_ReducesTokensOnSmallHistory(t *testing.T) {
	agent := NewAgent(Config{
		SystemPrompt: "You are helpful.",
		ContextCompression: ContextCompressionConfig{
			MaxTokens:        100000,
			ThresholdPercent: 80,
			Strategy:         CompressionMicro,
			MicroCompaction:  DefaultMicroCompactionConfig,
		},
	})

	// 10 messages with large old tool results.
	history := []Message{
		{Type: Content, Role: System, Content: "You are helpful."},
		{Type: Content, Role: User, Content: "step 1"},
		{Type: Content, Role: Assistant, Content: "ok"},
		{Type: Content, Role: ToolRole, Content: strings.Repeat("x", 2000)},
		{Type: Content, Role: User, Content: "step 2"},
		{Type: Content, Role: Assistant, Content: "ok"},
		{Type: Content, Role: ToolRole, Content: strings.Repeat("y", 2000)},
		{Type: Content, Role: User, Content: "step 3"},
		{Type: Content, Role: Assistant, Content: "ok"},
		{Type: Content, Role: ToolRole, Content: strings.Repeat("z", 2000)},
	}
	agent.SetHistory(history)

	before := agent.ContextStats().EstimatedTokens
	agent.microCompactForced(true)
	after := agent.ContextStats().EstimatedTokens

	if after >= before {
		t.Errorf("forced micro compaction did not reduce tokens: %d -> %d", before, after)
	}
}

func TestCompressHybrid(t *testing.T) {
	// Hybrid escalates elision → selective when usage is at/above the
	// escalation level (effectiveHard−5). With an explicit HardPercent=15 the
	// escalation level is 10%; the history below sits above it before hybrid
	// and drops below it after selective, so hybrid stops before the summarize
	// stage (which would need a live provider).
	agent := NewAgent(Config{
		SystemPrompt: "You are helpful.",
		ContextCompression: ContextCompressionConfig{
			MaxTokens:           500,
			Thresholds:          CompressionThresholds{HardPercent: 15},
			Strategy:            CompressionHybrid,
			PreserveRecentTurns: 1,
		},
	})

	var history []Message
	history = append(history, Message{Type: Content, Role: System, Content: "You are helpful."})
	for i := 0; i < 20; i++ {
		history = append(history, Message{Type: Content, Role: User, Content: strings.Repeat("a", 20)})
		history = append(history, Message{Type: Content, Role: Assistant, Content: strings.Repeat("b", 20)})
	}
	agent.SetHistory(history)

	// Hybrid should apply at least one strategy
	before := len(agent.GetHistory())
	err := agent.compressHybrid(context.Background())
	if err != nil {
		t.Fatalf("compressHybrid failed: %v", err)
	}
	after := len(agent.GetHistory())

	if after >= before {
		t.Errorf("Expected history to shrink, before=%d after=%d", before, after)
	}
}

func TestMaybeCompress_Disabled(t *testing.T) {
	agent := NewAgent(Config{
		SystemPrompt: "You are helpful.",
		// ContextCompression is zero value = disabled
	})

	// Fill history
	var history []Message
	history = append(history, Message{Type: Content, Role: System, Content: "You are helpful."})
	for i := 0; i < 50; i++ {
		history = append(history, Message{Type: Content, Role: User, Content: strings.Repeat("x", 100)})
	}
	agent.SetHistory(history)

	// Should not compress when disabled
	err := agent.maybeCompress(context.Background())
	if err != nil {
		t.Fatalf("maybeCompress should not error when disabled: %v", err)
	}

	if len(agent.GetHistory()) != len(history) {
		t.Error("History was modified when compression is disabled")
	}
}

func TestMaybeCompress_Triggers(t *testing.T) {
	agent := NewAgent(Config{
		SystemPrompt: "You are helpful.",
		ContextCompression: ContextCompressionConfig{
			MaxTokens:           500,
			ThresholdPercent:    10,
			Strategy:            CompressionSelective,
			Strategies:          CompressionLayerStrategies{Hard: CompressionSelective}, // pin hard layer: these tests exercise trigger mechanics offline // selective actually removes messages
			PreserveRecentTurns: 1,
		},
	})

	// Build history that exceeds threshold
	var history []Message
	history = append(history, Message{Type: Content, Role: System, Content: "You are helpful."})
	for i := 0; i < 20; i++ {
		history = append(history, Message{Type: Content, Role: User, Content: strings.Repeat("a", 50)})
		history = append(history, Message{Type: Content, Role: Assistant, Content: strings.Repeat("b", 50)})
	}
	agent.SetHistory(history)

	stats := agent.ContextStats()
	if stats.UsagePercent < 10 {
		t.Fatalf("Test setup: usage %d%% is below threshold 10%%", stats.UsagePercent)
	}

	err := agent.maybeCompress(context.Background())
	if err != nil {
		t.Fatalf("maybeCompress failed: %v", err)
	}

	// After compression, usage should drop
	newStats := agent.ContextStats()
	if newStats.UsagePercent >= stats.UsagePercent {
		t.Errorf("Usage did not decrease: %d%% -> %d%%", stats.UsagePercent, newStats.UsagePercent)
	}
}

// TestMaybeCompress_FallsBackToModelWindow verifies that when no explicit
// MaxTokens is configured but the model has a known context window, proactive
// compression uses that window as its limit.
func TestMaybeCompress_FallsBackToModelWindow(t *testing.T) {
	agent := NewAgent(Config{
		SystemPrompt: "You are helpful.",
		Model:        provider.Model{ContextWindow: 500},
		ContextCompression: ContextCompressionConfig{
			ThresholdPercent:    10,
			Strategy:            CompressionSelective,
			Strategies:          CompressionLayerStrategies{Hard: CompressionSelective}, // pin hard layer: these tests exercise trigger mechanics offline
			PreserveRecentTurns: 1,
		},
	})

	var history []Message
	history = append(history, Message{Type: Content, Role: System, Content: "You are helpful."})
	for i := 0; i < 20; i++ {
		history = append(history, Message{Type: Content, Role: User, Content: strings.Repeat("a", 50)})
		history = append(history, Message{Type: Content, Role: Assistant, Content: strings.Repeat("b", 50)})
	}
	agent.SetHistory(history)

	stats := agent.ContextStats()
	if stats.MaxTokens != 500 {
		t.Fatalf("MaxTokens = %d, want 500 (model window)", stats.MaxTokens)
	}
	if stats.UsagePercent < 10 {
		t.Fatalf("Test setup: usage %d%% is below threshold 10%%", stats.UsagePercent)
	}

	err := agent.maybeCompress(context.Background())
	if err != nil {
		t.Fatalf("maybeCompress failed: %v", err)
	}

	newStats := agent.ContextStats()
	if newStats.UsagePercent >= stats.UsagePercent {
		t.Errorf("Usage did not decrease: %d%% -> %d%%", stats.UsagePercent, newStats.UsagePercent)
	}
}

// TestEnforceContextCeiling verifies that when compression is disabled and the
// history exceeds the hard ceiling, old messages are dropped until the context
// fits.
// TestCheckContextLimit_AfterCompression verifies that a context above the
// hard ceiling can be brought under the limit by enforceContextCeiling, so
// checkContextLimit no longer errors. This regression-guards the ordering fix
// where checkContextLimit is called after prepareTurn (which runs compression).
func TestCheckContextLimit_AfterCompression(t *testing.T) {
	agent := NewAgent(Config{
		SystemPrompt: "You are helpful.",
		Model:        provider.Model{ContextWindow: 500},
	})

	var history []Message
	history = append(history, Message{Type: Content, Role: System, Content: "You are helpful."})
	for i := 0; i < 50; i++ {
		history = append(history, Message{Type: Content, Role: User, Content: strings.Repeat("x", 100)})
	}
	agent.SetHistory(history)

	if err := agent.checkContextLimit(); err == nil {
		t.Fatal("expected context limit error before compression")
	}

	agent.enforceContextCeiling()

	if err := agent.checkContextLimit(); err != nil {
		t.Errorf("expected no error after enforcing ceiling: %v", err)
	}
}

func TestEnforceContextCeiling(t *testing.T) {
	agent := NewAgent(Config{
		SystemPrompt: "You are helpful.",
		Model:        provider.Model{ContextWindow: 500},
	})

	var history []Message
	history = append(history, Message{Type: Content, Role: System, Content: "You are helpful."})
	for i := 0; i < 50; i++ {
		history = append(history, Message{Type: Content, Role: User, Content: strings.Repeat("x", 100)})
	}
	agent.SetHistory(history)

	before := len(agent.GetHistory())
	agent.enforceContextCeiling()
	after := len(agent.GetHistory())

	if after >= before {
		t.Errorf("History was not reduced: %d -> %d", before, after)
	}
	if estimateTokensFromHistory(agent.GetHistory()) > 500*95/100 {
		t.Errorf("Context still above ceiling: %d tokens", estimateTokensFromHistory(agent.GetHistory()))
	}
	// System prompt must be preserved.
	if agent.GetHistory()[0].Role != System {
		t.Errorf("First message role = %s, want System", agent.GetHistory()[0].Role)
	}
}

func TestIsContextLengthError(t *testing.T) {
	tests := []struct {
		err      string
		expected bool
	}{
		// Patterns covered by the surviving isContextLengthError detector.
		{"context length exceeded", true}, // "context length"
		{"maximum context length", true},  // "maximum context"
		{"too many tokens", true},         // "too many tokens"
		{"token limit exceeded", true},    // "token limit"
		{"context window exceeded", true}, // "context window"
		{"context_length_exceeded", true}, // exact
		{"max_tokens", true},              // exact
		// Patterns recognised by hooks.IsContextOverflow (which we now delegate to
		// before string matching). These are also legitimate context overflow
		// indicators from various providers.
		{"prompt is too long", true},
		{"input length exceeded", true},
		{"reduce the length", false},
		{"random error", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.err, func(t *testing.T) {
			got := isContextLengthError(___castErr(tt.err))
			if got != tt.expected {
				t.Errorf("isContextLengthError(%q) = %v, want %v", tt.err, got, tt.expected)
			}
		})
	}
}

func ___castErr(s string) error {
	if s == "" {
		return nil
	}
	return ___testErr(s)
}

type ___testErr string

func (e ___testErr) Error() string { return string(e) }

// ---------------------------------------------------------------------------
// Elided tool-call serialization ("Model imitates the [elided]
// tool-call placeholder" and "/compress:summarize rejected by provider:
// elided tool-call arguments are not valid JSON" — shared root cause).
// ---------------------------------------------------------------------------

// elidedPairHistory builds a history whose older tool call/result pairs get
// elided by compressToolElision while the most recent turn stays intact.
// With preserve=1 the boundary lands between call_2 and its result, so the
// second pair also exercises the boundary-straddle case (elided call whose
// result message is NOT itself elided).
func elidedPairHistory() []Message {
	return []Message{
		{Type: Content, Role: System, Content: "You are helpful."},
		{Type: Content, Role: User, Content: "Run a command"},
		{
			Type: Content, Role: Assistant,
			ToolCalls: []ToolCallInfo{
				{ID: "call_1", Type: "function", Name: "bash", Arguments: `{"command":"echo hello"}`},
			},
		},
		{Type: Content, Role: ToolRole, Content: "hello", ToolCallID: "call_1"},
		{Type: Content, Role: User, Content: "Edit a file"},
		{
			Type: Content, Role: Assistant,
			ToolCalls: []ToolCallInfo{
				{ID: "call_2", Type: "function", Name: "edit", Arguments: `{"path":"a.txt"}`},
			},
		},
		{Type: Content, Role: ToolRole, Content: "edited", ToolCallID: "call_2"},
		{Type: Content, Role: User, Content: "Thanks"},
		{Type: Content, Role: Assistant, Content: "You're welcome!"},
	}
}

func newElisionAgent() *Agent {
	return NewAgent(Config{
		SystemPrompt: "You are helpful.",
		ContextCompression: ContextCompressionConfig{
			MaxTokens:           10000,
			ThresholdPercent:    50,
			Strategy:            CompressionToolElision,
			PreserveRecentTurns: 1,
		},
	})
}

// assertToolPairingConsistent fails the test when the outbound payload holds
// a tool_call with invalid-JSON arguments, an "[elided]" placeholder, or a
// tool_result referencing a call the payload does not contain.
func assertToolPairingConsistent(t *testing.T, msgs []provider.Message) {
	t.Helper()
	assertNoOrphanToolResults(t, msgs, collectOutboundCallIDs(t, msgs))
}

// collectOutboundCallIDs validates every outbound tool_call's arguments and
// returns the set of call IDs present in the payload.
func collectOutboundCallIDs(t *testing.T, msgs []provider.Message) map[string]bool {
	t.Helper()
	callIDs := map[string]bool{}
	for _, m := range msgs {
		for _, b := range m.Content {
			if b.Type != provider.ContentBlockToolCall {
				continue
			}
			if b.ToolArguments == elidedToolCallArguments {
				t.Errorf("provider-bound tool_call %q still carries the elision placeholder", b.ToolCallID)
			}
			if !json.Valid([]byte(b.ToolArguments)) {
				t.Errorf("provider-bound tool_call %q arguments are not valid JSON: %q", b.ToolCallID, b.ToolArguments)
			}
			callIDs[b.ToolCallID] = true
		}
	}
	return callIDs
}

func assertNoOrphanToolResults(t *testing.T, msgs []provider.Message, callIDs map[string]bool) {
	t.Helper()
	for _, m := range msgs {
		for _, b := range m.Content {
			if b.Type == provider.ContentBlockToolResult && !callIDs[b.ToolCallID] {
				t.Errorf("orphan tool_result %q: no matching tool_call in payload", b.ToolCallID)
			}
		}
	}
}

// historyHasElisionMarker reports whether any in-history tool call still
// carries the elision marker (serialization must be non-destructive).
func historyHasElisionMarker(history []Message) bool {
	for _, m := range history {
		for _, tc := range m.ToolCalls {
			if tc.Arguments == elidedToolCallArguments {
				return true
			}
		}
	}
	return false
}

// assertNoToolResultsFor fails the test when the payload carries a
// tool_result block for any of the given call IDs.
func assertNoToolResultsFor(t *testing.T, msgs []provider.Message, ids ...string) {
	t.Helper()
	for _, m := range msgs {
		for _, b := range m.Content {
			if b.Type != provider.ContentBlockToolResult {
				continue
			}
			for _, id := range ids {
				if b.ToolCallID == id {
					t.Errorf("result of elided call %q leaked into provider payload", id)
				}
			}
		}
	}
}

func payloadTexts(msgs []provider.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		for _, c := range m.Content {
			b.WriteString(c.Text)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// TestBuildProviderHistoryElidedCallsBecomeNotes guards the imitation bug:
// after elision, the provider-bound history must contain NO invocable
// tool_call exemplar with placeholder arguments — elided calls become
// plain-text notes and their results are dropped (pairing stays consistent).
func TestBuildProviderHistoryElidedCallsBecomeNotes(t *testing.T) {
	agent := newElisionAgent()
	agent.SetHistory(elidedPairHistory())
	agent.compressToolElision(false)

	// Serialization must be non-destructive: the in-history marker remains.
	if !historyHasElisionMarker(agent.GetHistory()) {
		t.Fatal("in-history elision marker missing: serialization must not mutate history")
	}

	msgs := agent.buildProviderHistory()
	assertToolPairingConsistent(t, msgs)

	text := payloadTexts(msgs)
	for _, want := range []string{"[earlier call to bash elided]", "[earlier call to edit elided]"} {
		if !strings.Contains(text, want) {
			t.Errorf("provider-bound history missing note %q; payload:\n%s", want, text)
		}
	}
	// The straddle case: call_2's result was NOT elided in history, but its
	// call was — the result must be dropped, not shipped as an orphan.
	assertNoToolResultsFor(t, msgs, "call_1", "call_2")
}

// TestMigrateMessagesElidedArgumentsValidJSON guards the summarize bug at the
// migrate layer: no assistant tool_call leaving migrateMessages may carry
// non-JSON arguments.
func TestMigrateMessagesElidedArgumentsValidJSON(t *testing.T) {
	agent := newElisionAgent()
	agent.SetHistory(elidedPairHistory())
	agent.compressToolElision(false)

	assertToolPairingConsistent(t, migrateMessages(agent.GetHistory()))
}

// TestMigrateMessagesDropsElidedPairs covers parallel tool calls: one
// assistant message with two elided calls yields one plural note and both
// matching results are dropped, while a live pair survives untouched.
func TestMigrateMessagesDropsElidedPairs(t *testing.T) {
	msgs := []Message{
		{
			Type: Content, Role: Assistant,
			ToolCalls: []ToolCallInfo{
				{ID: "e1", Type: "function", Name: "bash", Arguments: elidedToolCallArguments},
				{ID: "e2", Type: "function", Name: "edit", Arguments: elidedToolCallArguments},
			},
		},
		{Type: Content, Role: ToolRole, Content: "[tool result elided]", ToolCallID: "e1"},
		{Type: Content, Role: ToolRole, Content: "[tool result elided]", ToolCallID: "e2"},
		{
			Type: Content, Role: Assistant,
			ToolCalls: []ToolCallInfo{
				{ID: "live", Type: "function", Name: "bash", Arguments: `{"command":"ls"}`},
			},
		},
		{Type: Content, Role: ToolRole, Content: "a.txt", ToolCallID: "live"},
	}

	out := migrateMessages(msgs)
	assertToolPairingConsistent(t, out)

	text := payloadTexts(out)
	if !strings.Contains(text, "[earlier calls to bash, edit elided]") {
		t.Errorf("missing plural elision note; payload:\n%s", text)
	}
	liveFound, liveResultFound := false, false
	for _, m := range out {
		for _, b := range m.Content {
			if b.Type == provider.ContentBlockToolCall && b.ToolCallID == "live" {
				liveFound = true
			}
			if b.Type == provider.ContentBlockToolResult && b.ToolCallID == "live" {
				liveResultFound = true
			}
		}
	}
	if !liveFound || !liveResultFound {
		t.Errorf("live tool pair damaged: call=%v result=%v", liveFound, liveResultFound)
	}
}

// TestMigrateMessagesDropsResultsForMissingAssistant is the defense-in-depth
// regression for the export-20260805-180955 bug: when the owning
// assistant(tool_calls) message is removed entirely (not just elided) by
// enforceContextCeiling or any other history mutation, migrateMessages must
// drop the orphaned tool result — a tool message with no preceding tool_calls
// is rejected by strict providers (HTTP 400).
func TestMigrateMessagesDropsResultsForMissingAssistant(t *testing.T) {
	// The assistant that issued "orphan_call" is absent from this snapshot;
	// only its tool result survives. migrateMessages must drop it.
	msgs := []Message{
		{Type: Content, Role: System, Content: "sys"},
		// missing: Assistant with ToolCalls{ID:"orphan_call"}
		{Type: Content, Role: ToolRole, Content: "result", ToolCallID: "orphan_call", ToolName: "read"},
		{Type: Content, Role: Assistant, Content: "ok"},
	}

	out := migrateMessages(msgs)
	assertToolPairingConsistent(t, out)

	for _, m := range out {
		for _, b := range m.Content {
			if b.Type == provider.ContentBlockToolResult && b.ToolCallID == "orphan_call" {
				t.Errorf("orphaned tool result for missing assistant leaked into payload")
			}
		}
	}
}

// TestSummarizeHistoryWithElidedPairs is the /compress:summarize regression:
// a snapshot containing elided pairs must reach the provider with no
// "[elided]" arguments and consistent pairing (the failing request shape
// carried no Tools array, exactly like summarizeHistory).
func TestSummarizeHistoryWithElidedPairs(t *testing.T) {
	p := &gateProbeProvider{
		api:        provider.Api(fmt.Sprintf("summarize-probe-%d", testProviderCounter.Add(1))),
		toolRounds: 0,
	}
	provider.RegisterApiProvider(p)

	agent := newElisionAgent()
	agent.cfg.Model = testModel(p.API())
	agent.cfg.Logger = NewLogger(Error)
	agent.SetHistory(elidedPairHistory())
	agent.compressToolElision(false)

	summary, err := agent.summarizeHistory(context.Background())
	if err != nil {
		t.Fatalf("summarizeHistory failed: %v", err)
	}
	if summary == "" {
		t.Error("summarizeHistory returned an empty summary")
	}

	ctxs := p.recorded()
	if len(ctxs) != 1 {
		t.Fatalf("expected 1 summarize request, got %d", len(ctxs))
	}
	assertToolPairingConsistent(t, ctxs[0].Messages)
	if !strings.Contains(payloadTexts(ctxs[0].Messages), "[earlier call to bash elided]") {
		t.Error("summarize request missing the elision note for elided calls")
	}
}

// --- Provider prefix-cache bust loop:(CM:13) regression tests ---

// appendElisionPair appends one assistant tool call plus its tool result of
// the given size — the session shape that drives elision (a long single turn
// of tool-call rounds).
func appendElisionPair(a *Agent, resultSize int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	id := fmt.Sprintf("c%d", len(a.history))
	a.history = append(a.history,
		Message{Type: Content, Role: Assistant, ToolCalls: []ToolCallInfo{{ID: id, Type: "function", Name: "tool", Arguments: `{"n":1}`}}},
		Message{Type: Content, Role: ToolRole, Content: strings.Repeat("x", resultSize), ToolCallID: id},
	)
}

// historyHash fingerprints message count, contents and tool-call arguments so
// tests can detect any history mutation (in-place elision or message drops) —
// every mutation is a provider prefix-cache bust on the next request.
func historyHash(a *Agent) uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	h := fnv.New64a()
	fmt.Fprintf(h, "%d\x00", len(a.history))
	for i := range a.history {
		m := &a.history[i]
		fmt.Fprintf(h, "%v\x00%s\x00", m.Role, m.Content)
		for _, tc := range m.ToolCalls {
			fmt.Fprintf(h, "%s\x00", tc.Arguments)
		}
	}
	return h.Sum64()
}

// simulateHotCacheRound marks the per-round provider contact the way a
// cache-reporting provider does: every completed request reports cache reads
// (partial hits after a bust still count) and refreshes the activity clock.
func simulateHotCacheRound(a *Agent) {
	a.mu.Lock()
	a.cacheWarmObserved = true
	a.lastRoundActivity = time.Now()
	a.mu.Unlock()
}

// TestMaybeCompress_ToolElision_HotCacheBudgetHysteresis is the core CM:13
// regression: above the 85% deferral ceiling with a hot cache, proactive
// tool_elision used to elide only the ~2 messages that crossed the count
// boundary per round, so usage stayed at the ceiling and the hot prefix
// cache busted EVERY round (13 misses in the session export). The hot-cache
// path must elide by TOKEN BUDGET down to the hysteresis target (hard−20 =
// 75%) in ONE pass, then stay quiet while usage climbs back.
func TestMaybeCompress_ToolElision_HotCacheBudgetHysteresis(t *testing.T) {
	a := NewAgent(Config{
		Model: testModel(provider.ApiOpenAICompletions),
		ContextCompression: ContextCompressionConfig{
			MaxTokens:  10000,
			Strategy:   CompressionToolElision,
			Thresholds: CompressionThresholds{TriggerPercent: 80}, // production shape: ceiling = hard−10 = 85
		},
	})
	a.mu.Lock()
	a.history = append(a.history, Message{Type: Content, Role: System, Content: "sys"})
	a.mu.Unlock()
	for i := 0; i < 19; i++ {
		appendElisionPair(a, 1500) // ~470 est tokens per pair
	}
	stats := a.ContextStats()
	if stats.UsagePercent < 85 || stats.UsagePercent >= 95 {
		t.Fatalf("setup: usage %d%% must be in the [85,95) ceiling band", stats.UsagePercent)
	}
	simulateHotCacheRound(a)

	if err := a.maybeCompress(context.Background()); err != nil {
		t.Fatalf("maybeCompress: %v", err)
	}
	after := a.ContextStats()
	target := a.cfg.ContextCompression.resolveThresholds().elisionTargetPercent()
	if after.UsagePercent > target {
		t.Errorf("one hot-cache pass must elide down to the hysteresis target: usage %d%% > %d%%",
			after.UsagePercent, target)
	}
	// Hysteresis elides only what the budget needs: the oldest results are
	// elided but mid-history payloads survive (no wholesale wipe).
	a.mu.Lock()
	oldest := a.history[2].Content
	mid := a.history[20].Content
	a.mu.Unlock()
	if oldest != elidedToolResultContent {
		t.Errorf("oldest tool result not elided: %q", oldest[:min(30, len(oldest))])
	}
	if mid == elidedToolResultContent {
		t.Errorf("budget overshot: mid-history result elided though the budget was met earlier")
	}
	// No re-fire while usage is back below the trigger: the next per-round
	// gate must leave history untouched (that per-round re-fire IS the bust
	// loop: count-boundary advance rewrote 2 more messages every round).
	before := historyHash(a)
	simulateHotCacheRound(a)
	if err := a.maybeCompress(context.Background()); err != nil {
		t.Fatalf("maybeCompress: %v", err)
	}
	if historyHash(a) != before {
		t.Errorf("proactive gate re-fired below the trigger after hysteresis elision (per-round bust loop)")
	}
}

// TestMaybeCompress_ToolElision_HotCacheEscalatesWhenBudgetUnmet covers the
// payload-poor case: at/above the ceiling with a hot cache but almost no
// elidable tool payload, nibbling every round would bust the cache for
// near-zero gain — the pass must escalate to selective message removal so the
// single bust buys real headroom (the entry's "stop nibbling" alternative).
func TestMaybeCompress_ToolElision_HotCacheEscalatesWhenBudgetUnmet(t *testing.T) {
	a := NewAgent(Config{
		Model: testModel(provider.ApiOpenAICompletions),
		ContextCompression: ContextCompressionConfig{
			MaxTokens:  6200,
			Strategy:   CompressionToolElision,
			Strategies: CompressionLayerStrategies{Hard: CompressionSelective}, // pin hard layer offline
			Thresholds: CompressionThresholds{TriggerPercent: 80},              // production shape: ceiling = 85
		},
	})
	a.mu.Lock()
	a.history = append(a.history, Message{Type: Content, Role: System, Content: "sys"})
	// Payload-poor history: plain user/assistant text only (~91% usage).
	for i := 0; i < 30; i++ {
		a.history = append(a.history,
			Message{Type: Content, Role: User, Content: strings.Repeat("q", 300)},
			Message{Type: Content, Role: Assistant, Content: strings.Repeat("a", 300)},
		)
	}
	a.mu.Unlock()
	stats := a.ContextStats()
	if stats.UsagePercent < 85 || stats.UsagePercent >= 95 {
		t.Fatalf("setup: usage %d%% must be in the [85,95) ceiling band", stats.UsagePercent)
	}
	simulateHotCacheRound(a)

	if err := a.maybeCompress(context.Background()); err != nil {
		t.Fatalf("maybeCompress: %v", err)
	}
	a.mu.Lock()
	n := len(a.history)
	a.mu.Unlock()
	if n >= 61 {
		t.Errorf("budget-unmet hot-cache pass did not escalate to selective: %d messages kept", n)
	}
	after := a.ContextStats()
	target := a.cfg.ContextCompression.resolveThresholds().elisionTargetPercent()
	if after.UsagePercent > target {
		t.Errorf("escalation must buy real headroom: usage %d%% > target %d%%", after.UsagePercent, target)
	}
}

// TestMaybeCompress_ToolElision_CacheBustConvergence replays the CM:13
// session shape — one long turn, hot cache, usage climbing slowly past the
// 85% deferral ceiling — and counts cache busts (rounds where the proactive
// gate mutated history). Pre-fix the count boundary advanced every round and
// busted the cache on EVERY round above the ceiling (~13 misses per export);
// post-fix one budgeted bust buys ~40 rounds of headroom, so a 85-round
// climb busts at most twice (the entry's "≤2 misses" bar).
func TestMaybeCompress_ToolElision_CacheBustConvergence(t *testing.T) {
	a := NewAgent(Config{
		Model: testModel(provider.ApiOpenAICompletions),
		ContextCompression: ContextCompressionConfig{
			MaxTokens:  20000,
			Strategy:   CompressionToolElision,
			Strategies: CompressionLayerStrategies{Hard: CompressionSelective}, // no LLM in tests
			Thresholds: CompressionThresholds{TriggerPercent: 80},              // production shape: ceiling = 85
		},
	})
	a.mu.Lock()
	a.history = append(a.history, Message{Type: Content, Role: System, Content: "sys"})
	a.mu.Unlock()
	// Seed to ~84% with large tool payloads, then climb slowly (small
	// results), mirroring the session's 84% → 95% drift over ~180 rounds.
	for a.ContextStats().UsagePercent < 84 {
		appendElisionPair(a, 1300)
	}

	var bustRounds []int
	usageAfterFirstBust := -1
	for round := 0; round < 85; round++ {
		simulateHotCacheRound(a)
		before := historyHash(a)
		if err := a.maybeCompress(context.Background()); err != nil {
			t.Fatalf("maybeCompress round %d: %v", round, err)
		}
		if historyHash(a) != before {
			bustRounds = append(bustRounds, round)
			if usageAfterFirstBust < 0 {
				usageAfterFirstBust = a.ContextStats().UsagePercent
			}
		}
		appendElisionPair(a, 110) // ~48 est tokens per round of growth
	}

	if len(bustRounds) == 0 {
		t.Fatalf("proactive elision never fired though usage crossed the 85%% ceiling")
	}
	if len(bustRounds) > 2 {
		t.Errorf("cache busted %d times in 85 rounds (rounds %v); budgeted hysteresis must keep it ≤2 "+
			"(pre-fix the count boundary busted EVERY round above the ceiling)", len(bustRounds), bustRounds)
	}
	target := a.cfg.ContextCompression.resolveThresholds().elisionTargetPercent()
	if usageAfterFirstBust > target {
		t.Errorf("usage after the first bust = %d%%, want ≤ hysteresis target %d%%", usageAfterFirstBust, target)
	}
	for i := 1; i < len(bustRounds); i++ {
		if gap := bustRounds[i] - bustRounds[i-1]; gap < 10 {
			t.Errorf("bust gap %d rounds (rounds %v): one bust must buy many rounds of headroom", gap, bustRounds)
		}
	}
}

// --- Cache-warm compaction summarization (CA1) regression tests ---

// prefixStubTool is a minimal tool registered only so the summarize request
// must carry a tools array identical to the conversation request's.
type prefixStubTool struct{ BaseTool }

func (prefixStubTool) Schema() ToolSchema {
	return ToolSchema{
		Name:        "prefix_stub",
		Description: "stub tool for prefix-parity tests",
		Schema:      map[string]any{"type": "object", "properties": map[string]any{}},
	}
}
func (prefixStubTool) Execute(input string) (string, error) { return "ok", nil }
func (prefixStubTool) IsRetryable(err error) bool           { return false }

// TestSummarizeHistoryReusesConversationPrefix is the CA1 regression: the
// summarize request must reuse the warm provider prefix cache, so it must be
// built as the conversation's OWN request prefix — same system prompt, same
// tools, same migrated history — with the compaction instruction appended as
// the final user message. The pre-fix shape swapped in a summarizer system
// prompt and dropped tools, cold-missing the automatic prefix cache (DeepSeek
// context caching) on the largest history of the session.
func TestSummarizeHistoryReusesConversationPrefix(t *testing.T) {
	p := &gateProbeProvider{
		api:        provider.Api(fmt.Sprintf("summarize-prefix-probe-%d", testProviderCounter.Add(1))),
		toolRounds: 0,
	}
	provider.RegisterApiProvider(p)

	agent := NewAgent(Config{
		SystemPrompt: "You are helpful.",
		Tools:        []Tool{prefixStubTool{}},
		ContextCompression: ContextCompressionConfig{
			MaxTokens:           10000,
			ThresholdPercent:    50,
			Strategy:            CompressionToolElision,
			PreserveRecentTurns: 1,
		},
	})
	agent.cfg.Model = testModel(p.API())
	agent.cfg.Logger = NewLogger(Error)
	agent.SetHistory([]Message{
		{Type: Content, Role: System, Content: "You are helpful."},
		{Type: Content, Role: User, Content: "first question"},
		{Type: Content, Role: Assistant, Content: "first answer"},
		{Type: Content, Role: User, Content: "second question"},
		{Type: Content, Role: Assistant, Content: "second answer"},
	})

	summary, err := agent.summarizeHistory(context.Background())
	if err != nil {
		t.Fatalf("summarizeHistory failed: %v", err)
	}
	if summary == "" {
		t.Fatal("summarizeHistory returned an empty summary")
	}

	ctxs := p.recorded()
	if len(ctxs) != 1 {
		t.Fatalf("expected 1 summarize request, got %d", len(ctxs))
	}
	got := ctxs[0]

	// 1. The conversation's own system prompt is the request prefix — not a
	// swapped-in summarizer system prompt.
	if got.SystemPrompt != agent.cfg.SystemPrompt {
		t.Errorf("summarize request system prompt = %q, want the conversation system prompt %q (prefix-cache reuse)",
			got.SystemPrompt, agent.cfg.SystemPrompt)
	}

	// 2. Tool schemas ride the request exactly as they do on conversation
	// turns, keeping the cached prefix (system + tools + history) aligned.
	conversation := agent.buildProviderContext(context.Background())
	if len(got.Tools) != len(conversation.Tools) {
		t.Errorf("summarize request carries %d tool schemas, conversation request carries %d",
			len(got.Tools), len(conversation.Tools))
	}

	// 3. The message list is the conversation history (leading system prompt
	// skipped, since it rides SystemPrompt) plus ONE appended user message
	// carrying the summarize instruction.
	if len(got.Messages) != len(conversation.Messages)+1 {
		t.Fatalf("summarize request holds %d messages, want conversation history (%d) + 1 instruction",
			len(got.Messages), len(conversation.Messages))
	}
	for i, m := range conversation.Messages {
		if got.Messages[i].Role != m.Role || payloadTexts([]provider.Message{got.Messages[i]}) != payloadTexts([]provider.Message{m}) {
			t.Errorf("message %d diverges from conversation prefix: got role=%v text=%q, want role=%v text=%q",
				i, got.Messages[i].Role, payloadTexts([]provider.Message{got.Messages[i]}), m.Role, payloadTexts([]provider.Message{m}))
		}
	}
	last := got.Messages[len(got.Messages)-1]
	if last.Role != provider.RoleUser {
		t.Errorf("instruction message role = %v, want user", last.Role)
	}
	if !strings.Contains(strings.ToLower(payloadTexts([]provider.Message{last})), "summar") {
		t.Error("final message does not carry the summarize instruction")
	}
}

// TestSummarizeHistoryEmptyOnToolOnlyReply guards the tools-bearing summarize
// path: a model that answers the instruction with only a tool call (no text)
// must yield an error instead of wiping the history with an empty summary.
func TestSummarizeHistoryEmptyOnToolOnlyReply(t *testing.T) {
	p := &gateProbeProvider{
		api:        provider.Api(fmt.Sprintf("summarize-toolonly-probe-%d", testProviderCounter.Add(1))),
		toolRounds: 99, // always answer with a tool call, never text
	}
	provider.RegisterApiProvider(p)

	agent := newElisionAgent()
	agent.cfg.Model = testModel(p.API())
	agent.cfg.Logger = NewLogger(Error)
	agent.SetHistory([]Message{
		{Type: Content, Role: User, Content: "q"},
		{Type: Content, Role: Assistant, Content: "a"},
	})

	summary, err := agent.summarizeHistory(context.Background())
	if err == nil {
		t.Errorf("summarizeHistory returned %q with nil error for a text-less reply; want an error so Compact cannot wipe history", summary)
	}
}
