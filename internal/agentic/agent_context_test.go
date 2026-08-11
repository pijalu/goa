// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// blockingContextTool blocks until ctx is cancelled, then returns the
// ctx error. Implements ContextTool so the agent forwards the turn ctx.
type blockingContextTool struct {
	name   string
	schema ToolSchema
}

func (m blockingContextTool) Schema() ToolSchema { return m.schema }
func (m blockingContextTool) Execute(input string) (string, error) {
	return "", errors.New("Execute must not be called on a ContextTool")
}
func (m blockingContextTool) IsRetryable(err error) bool { return false }
func (m blockingContextTool) ExecuteContext(ctx context.Context, input string) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

// TestExecuteToolWithResult_ContextToolForwardsCtx verifies that when a tool
// implements ContextTool, executeToolWithResult forwards the caller ctx and
// returns ctx.Err() promptly when it is cancelled (instead of hanging).
func TestExecuteToolWithResult_ContextToolForwardsCtx(t *testing.T) {
	agent := NewAgent(Config{
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
		Tools:        []Tool{blockingContextTool{name: "blocker", schema: ToolSchema{Name: "blocker"}}},
	})

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan ToolResult, 1)
	go func() {
		res, _ := agent.executeToolWithResult(ctx, "blocker", "{}", "call_1")
		done <- res
	}()

	// Give the tool a moment to start blocking.
	select {
	case <-done:
		t.Fatal("tool returned before ctx cancel — it did not block on ctx.Done()")
	case <-time.After(50 * time.Millisecond):
	}

	cancel()

	select {
	case res := <-done:
		if !errors.Is(res.Error, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", res.Error)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("tool did not return within 100ms of ctx cancel (B1 regression)")
	}
}

// TestExecuteToolWithResult_FallsBackToExecute verifies tools that do NOT
// implement ContextTool still execute via the plain Execute path.
func TestExecuteToolWithResult_FallsBackToExecute(t *testing.T) {
	agent := NewAgent(Config{
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
		Tools: []Tool{mockTool{
			name:   "plain",
			schema: ToolSchema{Name: "plain"},
		}},
	})

	res, err := agent.executeToolWithResult(context.Background(), "plain", "{}", "call_1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Output != "mock result" {
		t.Errorf("expected 'mock result', got %q", res.Output)
	}
}

// TestScheduleAndRunToolCalls_CancelTurnCtx exercises the full scheduler path:
// when the turn ctx is cancelled mid-execution, the buffered tool call returns
// within the deadline (previously this hung forever).
func TestScheduleAndRunToolCalls_CancelTurnCtx(t *testing.T) {
	agent := NewAgent(Config{
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
		Tools:        []Tool{blockingContextTool{name: "blocker", schema: ToolSchema{Name: "blocker"}}},
	})

	ctx, cancel := context.WithCancel(context.Background())

	tcs := []provider.ContentBlock{{
		Type:          provider.ContentBlockToolCall,
		ToolCallID:    "call_ctx_1",
		ToolName:      "blocker",
		ToolArguments: `{}`,
	}}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = agent.scheduleAndRunToolCalls(ctx, tcs)
	}()

	select {
	case <-done:
		t.Fatal("scheduleAndRunToolCalls returned before ctx cancel")
	case <-time.After(50 * time.Millisecond):
	}

	cancel()

	select {
	case <-done:
		// success: cancelled tool unblocked Collect()
	case <-time.After(200 * time.Millisecond):
		t.Fatal("scheduleAndRunToolCalls hung after turn ctx cancel (B1 regression)")
	}
}

// TestContextTool_Interface asserts the ContextTool interface composes Tool
// and ExecuteContext, guaranteeing the type assertion in executeToolWithResult.
func TestContextTool_Interface(t *testing.T) {
	var _ Tool = blockingContextTool{}
	var _ ContextTool = blockingContextTool{}

	// Sanity: mockTool does NOT satisfy ContextTool (no ExecuteContext).
	if _, ok := any(mockTool{}).(ContextTool); ok {
		t.Fatal("mockTool must not satisfy ContextTool")
	}
}

func TestEffectiveMaxTokens_UsesModelContextWindow(t *testing.T) {
	a := &Agent{
		cfg: Config{
			Model: provider.Model{ContextWindow: 1000000},
		},
	}
	if got := a.effectiveMaxTokens(); got != 1000000 {
		t.Errorf("effectiveMaxTokens() = %d, want 1000000", got)
	}
}

func TestEffectiveMaxTokens_PrefersCompressionConfig(t *testing.T) {
	a := &Agent{
		cfg: Config{
			Model:              provider.Model{ContextWindow: 1000000},
			ContextCompression: ContextCompressionConfig{MaxTokens: 8192},
		},
	}
	if got := a.effectiveMaxTokens(); got != 8192 {
		t.Errorf("effectiveMaxTokens() = %d, want 8192", got)
	}
}

func TestSetContextWindow_UpdatesEffectiveMaxTokens(t *testing.T) {
	a := NewAgent(Config{
		Model: provider.Model{ContextWindow: 262144},
	})
	a.SetContextWindow(32768)
	if got := a.effectiveMaxTokens(); got != 32768 {
		t.Errorf("effectiveMaxTokens() after SetContextWindow = %d, want 32768", got)
	}
}

func TestSetContextWindow_UpdatesContextStats(t *testing.T) {
	a := NewAgent(Config{
		Model:        provider.Model{ContextWindow: 131072},
		SystemPrompt: "You are helpful.",
	})
	a.SetContextWindow(32768)
	stats := a.ContextStats()
	if stats.MaxTokens != 32768 {
		t.Errorf("ContextStats().MaxTokens after SetContextWindow = %d, want 32768", stats.MaxTokens)
	}
	if !stats.AutoMax {
		t.Error("AutoMax should be true after runtime context-window refresh")
	}
}

func TestEffectiveMaxTokens_CapsCompressionMaxByModelWindow(t *testing.T) {
	a := NewAgent(Config{
		Model:              provider.Model{ContextWindow: 8192},
		ContextCompression: ContextCompressionConfig{MaxTokens: 100_000},
	})
	if got := a.effectiveMaxTokens(); got != 8192 {
		t.Errorf("effectiveMaxTokens() = %d, want 8192 (model window caps explicit max)", got)
	}
}

func TestCheckContextLimit_AllowsLargeHistoryWithinModelWindow(t *testing.T) {
	a := &Agent{
		cfg: Config{
			Model: provider.Model{ContextWindow: 1000000},
		},
		history: []Message{
			{Type: Content, Role: System, Content: strings.Repeat("a", 10000)},
			{Type: Content, Role: User, Content: strings.Repeat("b", 100000)},
		},
	}
	if err := a.checkContextLimit(); err != nil {
		t.Errorf("checkContextLimit() = %v, want nil for history well under 1M window", err)
	}
}

// TestEnforceContextCeiling_KeepsSystemAndFittingTail verifies the O(n)
// rewrite retains the system prompt plus the most-recent contiguous tail that
// fits under the hard ceiling, dropping oldest non-system messages first.
func TestEnforceContextCeiling_KeepsSystemAndFittingTail(t *testing.T) {
	// estimateTokens counts ascii chars as asciiCount/4, so 200 chars => 50 tokens.
	mk := func(role Role, n int) Message {
		return Message{Type: Content, Role: role, Content: strings.Repeat("x", n)}
	}
	a := &Agent{
		cfg: Config{Model: provider.Model{ContextWindow: 100}}, // hardCeiling = 95 tokens
		history: []Message{
			mk(System, 4), // ~1 token, index 0 — must always be retained
			mk(User, 200), // ~50 tokens (oldest non-system; dropped first)
			mk(User, 200), // ~50 tokens
			mk(User, 200), // ~50 tokens (newest; retained)
		},
	}
	a.enforceContextCeiling()

	if len(a.history) != 2 {
		t.Fatalf("expected system + 1 retained tail message, got %d messages", len(a.history))
	}
	if a.history[0].Role != System {
		t.Errorf("index 0 must remain the system prompt, got role %s", a.history[0].Role)
	}
	if a.history[1].Role != User || len(a.history[1].Content) != 200 {
		t.Errorf("retained tail must be the newest user message, got role=%s len=%d",
			a.history[1].Role, len(a.history[1].Content))
	}
}

// TestEnforceContextCeiling_NoopWhenUnderCeiling verifies it leaves history
// untouched when usage is already within budget.
func TestEnforceContextCeiling_NoopWhenUnderCeiling(t *testing.T) {
	before := []Message{
		{Type: Content, Role: System, Content: "sys"},
		{Type: Content, Role: User, Content: "hi"},
		{Type: Content, Role: Assistant, Content: "hello"},
	}
	a := &Agent{
		cfg:     Config{Model: provider.Model{ContextWindow: 1_000_000}},
		history: before,
	}
	a.enforceContextCeiling()
	if len(a.history) != len(before) {
		t.Fatalf("history must be unchanged when under ceiling: got %d want %d", len(a.history), len(before))
	}
	for i := range before {
		if a.history[i].Role != before[i].Role || a.history[i].Content != before[i].Content {
			t.Errorf("history mutated at index %d", i)
		}
	}
}

// firstOrphanedToolResult returns a human-readable description of the first
// tool-role message whose owning assistant(tool_calls) is absent from the
// history — the exact state strict providers reject with HTTP 400
// "Messages with role 'tool' must be a response to a preceding message with
// 'tool_calls'". An empty string means every tool result is paired.
func firstOrphanedToolResult(hist []Message) string {
	seen := make(map[string]bool)
	for i := range hist {
		m := hist[i]
		if m.Role == Assistant {
			for _, tc := range m.ToolCalls {
				if tc.ID != "" {
					seen[tc.ID] = true
				}
			}
		}
		if m.Role == ToolRole && !seen[m.ToolCallID] {
			return fmt.Sprintf("tool result at index %d (call_id=%q) has no preceding assistant tool_calls", i, m.ToolCallID)
		}
	}
	return ""
}

func roleNames(hist []Message) []string {
	out := make([]string, len(hist))
	for i, m := range hist {
		out[i] = string(m.Role)
	}
	return out
}

// TestEnforceContextCeiling_NeverOrphansToolResults is the regression test for
// the export-20260805-180955 bug: enforceContextCeiling dropped the
// assistant(tool_calls) message while keeping its tool result, producing a
// history that the provider rejected with HTTP 400 "Messages with role 'tool'
// must be a response to a preceding message with 'tool_calls'".
//
// Each case sets a token budget that forces the cut to land exactly at a tool
// result message. After enforceContextCeiling the result must contain zero
// orphaned tool results.
func TestEnforceContextCeiling_NeverOrphansToolResults(t *testing.T) {
	// 260 ascii chars ≈ 78 content tokens; with the 4-token message overhead
	// a "big" message is ~82 tokens — large enough that the ceiling enforcer
	// must drop it to fit under ContextWindow=100 (hard ceiling 95 tokens).
	bigContent := strings.Repeat("x", 260)

	tests := []struct {
		name    string
		history []Message
	}{
		{
			name: "single orphaned tool result at cut",
			history: []Message{
				{Type: Content, Role: System, Content: "sys"}, // idx 0 — always kept
				{Type: Content, Role: User, Content: bigContent},
				{Type: Content, Role: User, Content: bigContent},
				{Type: Content, Role: Assistant, Content: bigContent, ToolCalls: []ToolCallInfo{
					{ID: "tc1", Type: "function", Name: "read", Arguments: "{}"},
				}},
				{Type: Content, Role: ToolRole, Content: "result", ToolCallID: "tc1", ToolName: "read"},
				{Type: Content, Role: User, Content: "ok"},
			},
		},
		{
			name: "multiple orphaned tool results at cut",
			history: []Message{
				{Type: Content, Role: System, Content: "sys"},
				{Type: Content, Role: User, Content: bigContent},
				{Type: Content, Role: Assistant, Content: bigContent, ToolCalls: []ToolCallInfo{
					{ID: "tc1", Type: "function", Name: "read", Arguments: "{}"},
					{ID: "tc2", Type: "function", Name: "search", Arguments: "{}"},
				}},
				{Type: Content, Role: ToolRole, Content: "r1", ToolCallID: "tc1", ToolName: "read"},
				{Type: Content, Role: ToolRole, Content: "r2", ToolCallID: "tc2", ToolName: "search"},
				{Type: Content, Role: User, Content: "ok"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &Agent{
				cfg:     Config{Model: provider.Model{ContextWindow: 100}},
				history: append([]Message(nil), tt.history...),
			}
			a.enforceContextCeiling()

			if orphan := firstOrphanedToolResult(a.history); orphan != "" {
				t.Fatalf("%s\nroles after enforceContextCeiling: %v",
					orphan, roleNames(a.history))
			}
			// Sanity: system prompt must always survive.
			if len(a.history) == 0 || a.history[0].Role != System {
				t.Fatalf("system prompt must survive ceiling enforcement, got: %v",
					roleNames(a.history))
			}
		})
	}
}

// TestEnforceContextCeiling_ReactiveCutFreesHalfWindow is the CM:13 regression
// test (bugs.md must-fix #1): the reactive ceiling enforcer must NOT nibble —
// dropping just enough to dip under the 95% ceiling re-busts the provider prefix
// cache on the very next tool result (the CM:13 session: 13 busts / 58 drops).
// Instead, a reactive cut must free ≥ReactiveSavingsPercent of the window in one
// pass so one cache miss buys many rounds of headroom (design rule 4).
//
// We build a history pinned near the 95% ceiling, run the enforcer, and assert
// the retained history occupies at most the reactive target (≈45% of the window
// at the default 95% ceiling), i.e. ≥50% savings.
func TestEnforceContextCeiling_ReactiveCutFreesHalfWindow(t *testing.T) {
	const window = 10000
	// estimateTokens counts ascii chars as asciiCount/4, so N chars => N/4 tokens.
	// Build ~38 messages of ~100 tokens each (~3800 tokens ≈ 38% of window) plus a
	// system prompt, totaling just over the 95% ceiling so the enforcer fires.
	mk := func(role Role, chars int) Message {
		return Message{Type: Content, Role: role, Content: strings.Repeat("x", chars)}
	}
	hist := []Message{mk(System, 400)} // ~100 tokens
	// Fill to ~96% of the window: 95 ceiling, so the enforcer must cut.
	for i := 0; i < 95; i++ {
		hist = append(hist, mk(User, 400)) // ~100 tokens each
	}
	a := &Agent{cfg: Config{Model: provider.Model{ContextWindow: window}}, history: hist}
	a.enforceContextCeiling()

	retained := 0
	for i := range a.history {
		retained += messageTokenCount(&a.history[i])
	}
	// Reactive target at default hard=95: 95 - ReactiveSavingsPercent(50) = 45%.
	// Fixed cost is ~0 here (no tool schemas), so retained must be ≤ 45% of window.
	target := window * 45 / 100
	if retained > target {
		t.Errorf("reactive cut did not free ≥50%% of the window: retained %d tokens (%.1f%%), target %d tokens (45%%) — "+
			"a nibble that only dips under the 95%% ceiling re-busts the prefix cache every round (CM:13)",
			retained, float64(retained)/float64(window)*100, target)
	}
	// The cut must have actually happened (history shrank substantially).
	if len(a.history) >= len(hist) {
		t.Errorf("history was not cut: %d messages before, %d after", len(hist), len(a.history))
	}
}

// TestEnforceContextCeiling_ReactiveCutNoImmediateRebust verifies the second
// half of the CM:13 fix: after a reactive cut to the target, a single small
// tool-result addition must NOT push history back over the ceiling (which would
// force another destructive cut and re-bust). This is the "one cache bust buys
// many rounds" guarantee (design rule 4 rationale).
func TestEnforceContextCeiling_ReactiveCutNoImmediateRebust(t *testing.T) {
	const window = 10000
	mk := func(role Role, chars int) Message {
		return Message{Type: Content, Role: role, Content: strings.Repeat("x", chars)}
	}
	hist := []Message{mk(System, 400)}
	for i := 0; i < 95; i++ {
		hist = append(hist, mk(User, 400))
	}
	a := &Agent{cfg: Config{Model: provider.Model{ContextWindow: window}}, history: hist}
	a.enforceContextCeiling()

	// Simulate the next round: a small assistant + tool result (~200 tokens).
	a.history = append(a.history, mk(Assistant, 400), mk(ToolRole, 400))
	a.enforceContextCeiling()

	// After the small addition + a second enforce, history must STILL be under
	// the hard ceiling (95%) — the first cut left enough headroom that a single
	// round did not re-trigger a destructive cut. If the first cut had only
	// nibbled to the ceiling, this second enforce would cut again.
	retained := 0
	for i := range a.history {
		retained += messageTokenCount(&a.history[i])
	}
	hardCeiling := window * 95 / 100
	if retained > hardCeiling {
		t.Errorf("after reactive cut + one small round, history %d tokens exceeds 95%% ceiling %d — "+
			"the first cut did not buy headroom (re-bust every round = the CM:13 loop)", retained, hardCeiling)
	}
}
