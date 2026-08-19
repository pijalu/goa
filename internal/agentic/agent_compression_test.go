// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

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
			MaxTokens:       100000,
			Thresholds:      CompressionThresholds{SoftPercent: 80},
			Strategies:      CompressionLayerStrategies{Soft: CompressionMicro},
			MicroCompaction: DefaultMicroCompactionConfig,
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
			MaxTokens: 500,
			// Soft/hard model: no trigger layer. Drive the SOFT layer with a
			// low soft threshold + selective strategy (selective actually
			// removes messages). A fresh agent has no hot-cache evidence, so
			// the soft gate fires rather than deferring.
			Thresholds:          CompressionThresholds{SoftPercent: 10},
			Strategies:          CompressionLayerStrategies{Soft: CompressionSelective, Hard: CompressionSelective},
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
			// Soft/hard model: no trigger layer. Drive the SOFT layer with a
			// low soft threshold + selective strategy (selective removes
			// messages); fresh agent = cold cache, so the soft gate fires.
			Thresholds:          CompressionThresholds{SoftPercent: 10},
			Strategies:          CompressionLayerStrategies{Soft: CompressionSelective, Hard: CompressionSelective},
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
