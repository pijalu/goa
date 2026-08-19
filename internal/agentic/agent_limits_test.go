// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

func TestAgent_ToolCallLimitResetsOnUniqueCall(t *testing.T) {
	p := registerUniqueArgToolProvider(5)
	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
		Tools: []Tool{mockTool{
			name:   "mock_tool",
			schema: ToolSchema{Name: "mock_tool", Description: "test"},
		}},
		MaxToolRepeatTotal:       0,
		MaxToolRepeatConsecutive: 0,
		MaxToolCalls:             3,
		ToolCallLimitResetWindow: 10,
	})

	obs := runAgentCollectingEvents(t, agent, "call tools")

	var realResults, guardResults int
	for _, e := range obs.Events() {
		if e.Type != EventToolResult {
			continue
		}
		if strings.Contains(e.Text, "Loop guardrail") || strings.Contains(e.Text, "already executed") {
			guardResults++
		} else {
			realResults++
		}
	}
	// With MaxToolCalls=3 and 5 unique calls, no call repeats within the
	// window, so all calls execute and no guard fires.
	if realResults != 5 {
		t.Errorf("expected 5 real executions for unique calls, got %d (guards=%d)", realResults, guardResults)
	}
	if guardResults != 0 {
		t.Errorf("expected 0 guard results for unique calls, got %d", guardResults)
	}
}

// TestAgent_SingleEventEndAcrossToolCallTurn is the regression test for the
// "spinner disappears after the first tool call" bug.
//
// EventEnd marks the end of a whole conversation turn. A turn that performs
// tool calls and then produces a final answer streams multiple rounds, but it
// is still a single turn, so it must emit exactly one EventEnd — at the very
// end. Previously completeStreamTurn emitted an EventEnd after every tool
// batch; UI consumers (the status spinner) treated that as a session end and
// armed a guard that silently dropped every subsequent Show(), so the spinner
// vanished after the first tool call and never came back.
//
// Flow exercised: round 1 = tool call A, round 2 = tool call B, round 3 =
// final text answer. Expected: exactly one EventEnd, positioned after the
// final assistant content and with no EventEnd between the tool results and
// the final answer.
func TestAgent_SingleEventEndAcrossToolCallTurn(t *testing.T) {
	p := registerUniqueArgToolProvider(2)
	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
		Tools: []Tool{mockTool{
			name:   "mock_tool",
			schema: ToolSchema{Name: "mock_tool", Description: "test"},
		}},
		MaxToolCalls: 10,
	})

	obs := runAgentCollectingEvents(t, agent, "call tools")
	events := obs.Events()

	var endCount int
	var lastEndIdx, lastContentIdx int = -1, -1
	for i, e := range events {
		if e.Type == EventEnd {
			endCount++
			lastEndIdx = i
		}
		if e.Type == EventContent && e.Role == Assistant {
			lastContentIdx = i
		}
	}
	if endCount != 1 {
		var seq []string
		for _, e := range events {
			seq = append(seq, string(e.Type))
		}
		t.Fatalf("expected exactly 1 EventEnd for a multi-round tool-call turn, got %d. Event sequence: %v", endCount, seq)
	}
	if lastContentIdx < 0 {
		t.Fatal("expected at least one assistant content event (the final answer)")
	}
	// The single EventEnd must come after the final assistant content: it
	// terminates the turn, so nothing turn-related should follow it.
	if lastEndIdx < lastContentIdx {
		t.Fatalf("EventEnd (idx %d) came before final assistant content (idx %d); it must terminate the turn", lastEndIdx, lastContentIdx)
	}
}

func TestAgent_ToolCallLimitEnforcedOnRepeatedCall(t *testing.T) {
	totalCalls := 4
	maxCalls := 3

	p := registerBatchToolProvider(totalCalls)
	agent := newAgentWithMockTool(p.API(), maxCalls)
	obs := runAgentCollectingEvents(t, agent, "call tools")

	var realResults, repeatResults, loopResults int
	for _, e := range obs.Events() {
		if e.Type != EventToolResult {
			continue
		}
		switch {
		case strings.Contains(e.Text, "already executed"):
			repeatResults++
		case strings.Contains(e.Text, "Loop guardrail"):
			loopResults++
		default:
			realResults++
		}
	}
	// With MaxToolCalls=3 and identical calls (fixed semantics):
	// calls 1,2,3: executed (window count ≤ limit 3)
	// call 4: hard loop (4th duplicate in window)
	if realResults != 3 {
		t.Errorf("expected 3 real executions, got %d (repeat=%d loop=%d)", realResults, repeatResults, loopResults)
	}
	if repeatResults != 0 {
		t.Errorf("expected 0 soft-repeat results (2nd call now executes), got %d", repeatResults)
	}
	if loopResults != 1 {
		t.Errorf("expected 1 hard-loop result, got %d", loopResults)
	}
}

func TestAgent_ToolCallLimit_WindowCustom(t *testing.T) {
	p := registerUniqueArgToolProvider(5)
	agent := NewAgent(Config{
		Model:                    testModel(p.API()),
		SystemPrompt:             "test",
		Logger:                   NewLogger(Error),
		Tools:                    []Tool{mockTool{name: "mock_tool", schema: ToolSchema{Name: "mock_tool", Description: "test"}}},
		MaxToolRepeatTotal:       0,
		MaxToolRepeatConsecutive: 0,
		MaxToolCalls:             10,
		ToolCallLimitResetWindow: 5,
	})

	// The custom window is honored: all unique calls execute, no duplicates.
	obs := runAgentCollectingEvents(t, agent, "call tools")

	var realResults, guardResults int
	for _, e := range obs.Events() {
		if e.Type != EventToolResult {
			continue
		}
		if strings.Contains(e.Text, "Loop guardrail") || strings.Contains(e.Text, "already executed") {
			guardResults++
		} else {
			realResults++
		}
	}
	if realResults != 5 {
		t.Errorf("expected 5 real executions, got %d (guards=%d)", realResults, guardResults)
	}
	if guardResults != 0 {
		t.Errorf("expected 0 guard results for unique calls, got %d", guardResults)
	}
}

func TestAgent_ToolBudget_GuardResultReturnedToLLM(t *testing.T) {
	totalCalls := 3
	maxCalls := 2

	p := registerBatchToolProvider(totalCalls)
	agent := newAgentWithMockTool(p.API(), maxCalls)
	runAgentCollectingEvents(t, agent, "call tools")

	history := copyAgentHistory(agent)
	assertToolGuardResult(t, history)

	// Ensure the guard result is a ToolRole message, not an error returned by Run.
	var found bool
	for _, msg := range history {
		if msg.Role == ToolRole && (strings.Contains(msg.Content, "Loop guardrail") || strings.Contains(msg.Content, "already executed")) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a ToolRole message containing a guardrail hint in history")
	}
}

func TestAgent_TurnStatsBeforeEnd(t *testing.T) {
	p := textEventProvider("Hello")
	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
	})

	obs := &mockEventObserver{}
	agent.AddObserver(obs)

	go func() {
		for range agent.Output {
		}
	}()
	if err := agent.Run(context.Background(), "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	agent.Stop()

	var endIdx, statsIdx int
	foundEnd, foundStats := false, false
	for i, e := range obs.Events() {
		if e.Type == EventTokenStats {
			statsIdx = i
			foundStats = true
		}
		if e.Type == EventEnd {
			endIdx = i
			foundEnd = true
			break // EventEnd is the last relevant event
		}
	}
	if !foundStats {
		t.Error("expected EventTokenStats before EventEnd")
	}
	if !foundEnd {
		t.Fatal("expected EventEnd")
	}
	if statsIdx > endIdx {
		t.Errorf("EventTokenStats (idx %d) should come before EventEnd (idx %d)", statsIdx, endIdx)
	}
}

// TestAgent_OutputSpeedFallbackForLocalProvider verifies that when a provider
// reports token usage WITHOUT any timing fields (as LM Studio, llama.cpp, and
// Ollama do), the agent still derives a non-zero output tok/s from wall-clock
// generation time rather than reporting speed=0.0.
func TestAgent_OutputSpeedFallbackForLocalProvider(t *testing.T) {
	p := textEventProvider("hello world")
	// Simulate LM Studio usage: token counts only, no PredictedMs/PerSecond
	// (provider.Usage has no timing fields, matching local servers).
	p.usage = &provider.Usage{InputTokens: 12, OutputTokens: 3}

	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
	})
	obs := &mockEventObserver{}
	agent.AddObserver(obs)
	go func() {
		for range agent.Output {
		}
	}()
	if err := agent.Run(context.Background(), "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	agent.Stop()

	var stats *TokenTimings
	for _, e := range obs.Events() {
		if e.Type == EventTokenStats && e.Timings != nil {
			stats = e.Timings
			break
		}
	}
	if stats == nil {
		t.Fatal("expected EventTokenStats")
	}
	if stats.PromptN != 12 {
		t.Errorf("PromptN = %d, want 12 (provider usage)", stats.PromptN)
	}
	if stats.PredictedN != 3 {
		t.Errorf("PredictedN = %d, want 3 (provider usage)", stats.PredictedN)
	}
	if stats.PredictedPerSecond <= 0 {
		t.Errorf("PredictedPerSecond = %.2f, want > 0 (wall-clock fallback for timing-less providers)", stats.PredictedPerSecond)
	}
}

// TestAgent_CacheStatsSurfacedWhenReported verifies that when a provider
// reports cache tokens (e.g. llama.cpp tokens_cached, or Anthropic/OpenAI
// cached_tokens), they are surfaced in the token-stats timings so the footer
// can display them. Providers that omit cache (LM Studio) simply leave these 0.
func TestAgent_CacheStatsSurfacedWhenReported(t *testing.T) {
	p := textEventProvider("ok")
	p.usage = &provider.Usage{InputTokens: 10, OutputTokens: 2, CacheReadTokens: 8, CacheCreationTokens: 1}

	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
	})
	obs := &mockEventObserver{}
	agent.AddObserver(obs)
	go func() {
		for range agent.Output {
		}
	}()
	if err := agent.Run(context.Background(), "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	agent.Stop()

	for _, e := range obs.Events() {
		if e.Type == EventTokenStats && e.Timings != nil {
			if e.Timings.CacheReadTokens != 8 {
				t.Errorf("CacheReadTokens = %d, want 8", e.Timings.CacheReadTokens)
			}
			if e.Timings.CacheWriteTokens != 1 {
				t.Errorf("CacheWriteTokens = %d, want 1", e.Timings.CacheWriteTokens)
			}
			return
		}
	}
	t.Fatal("expected EventTokenStats with cache fields")
}

func TestAgent_ContextStats_AutoMaxFromModel(t *testing.T) {
	agent := NewAgent(Config{
		Model: provider.Model{
			Api:           provider.ApiOpenAICompletions,
			ID:            "test-model",
			ContextWindow: 128000,
		},
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
	})

	agent.history = []Message{
		{Type: Content, Role: System, Content: "test"},
		{Type: Content, Role: User, Content: "hello"},
	}

	stats := agent.ContextStats()
	if stats.MaxTokens != 128000 {
		t.Errorf("expected MaxTokens=128000 from model context window, got %d", stats.MaxTokens)
	}
	if !stats.AutoMax {
		t.Error("expected AutoMax=true when using model context window")
	}
	if stats.EstimatedTokens == 0 {
		t.Error("expected non-zero estimated tokens")
	}
}

func TestAgent_ToolResultAsUserOverride(t *testing.T) {
	agent := NewAgent(Config{
		Model:        testModel("test-tool-result-as-user-api"),
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
	})

	model := testModel("test-tool-result-as-user-api")
	modified := agent.withToolResultAsUser(model, true)
	compat, ok := modified.Compat.(provider.OpenAICompletionsCompat)
	if !ok {
		t.Fatal("expected OpenAICompletionsCompat")
	}
	if !provider.ToBool(compat.ToolResultAsUser, false) {
		t.Error("expected ToolResultAsUser=true")
	}

	modified = agent.withToolResultAsUser(model, false)
	compat = modified.Compat.(provider.OpenAICompletionsCompat)
	if provider.ToBool(compat.ToolResultAsUser, true) {
		t.Error("expected ToolResultAsUser=false")
	}
}

func TestAgent_SetTools_UpdatesRegistry(t *testing.T) {
	p := textEventProvider("hi")
	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
	})
	go func() {
		for range agent.Output {
		}
	}()
	if err := agent.Run(context.Background(), "hello"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	newTool := &fakeTool{name: "new_tool"}
	agent.SetTools([]Tool{newTool})

	if _, ok := agent.reg.Get("new_tool"); !ok {
		t.Error("new_tool should be in agent registry after SetTools")
	}
}

func TestAgent_InjectSystemMessage_IncludesLaterSystemMessages(t *testing.T) {
	p := textEventProvider("hi")
	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "initial",
		Logger:       NewLogger(Error),
	})
	go func() {
		for range agent.Output {
		}
	}()
	if err := agent.Run(context.Background(), "hello"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	agent.InjectSystemMessage("additional system info")
	assertHistoryContains(t, agent.GetHistory(), "additional system info")
	pCtx := agent.buildProviderContext(context.Background())
	assertProviderContextContains(t, pCtx, "additional system info")
	// The injected note must reach the provider as a user-role message: a
	// non-leading system role breaks strict Jinja chat templates (HTTP 400
	// "System message must be at the beginning", 2026-08-04 LM Studio export).
	for _, m := range pCtx.Messages {
		if m.Role == provider.RoleSystem {
			t.Error("injected system message must not keep system role in provider context")
		}
	}
}

func assertHistoryContains(t *testing.T, hist []Message, want string) {
	t.Helper()
	for _, m := range hist {
		if m.Role == System && m.Content == want {
			return
		}
	}
	t.Errorf("injected system message not found in history: %+v", hist)
}

// assertProviderContextContains reports the injected note is forwarded to
// the provider at any role. Non-leading system messages are downgraded to
// user-role notes at the provider boundary (strict Jinja templates reject
// them), so presence is asserted role-agnostically.
func assertProviderContextContains(t *testing.T, pCtx provider.Context, want string) {
	t.Helper()
	for _, m := range pCtx.Messages {
		for _, b := range m.Content {
			if b.Type == provider.ContentBlockText && b.Text == want {
				return
			}
		}
	}
	t.Error("injected system message should be included in provider context")
}

type fakeTool struct{ name string }

func (f *fakeTool) Schema() ToolSchema             { return ToolSchema{Name: f.name, Description: "fake"} }
func (f *fakeTool) Execute(string) (string, error) { return "ok", nil }
func (f *fakeTool) IsRetryable(error) bool         { return false }

// multiRoundToolProvider emits a single tool call for the first `toolRounds`
// streams and a plain text response on the next stream. Used to verify that
// the agent continues re-streaming after tool calls for more than the old
// hard-coded 3-attempt limit.
type multiRoundToolProvider struct {
	api        provider.Api
	toolRounds int
	seen       int
}

func (p *multiRoundToolProvider) API() provider.Api { return p.api }

func (p *multiRoundToolProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	p.seen++
	result := provider.NewAssistantMessageEventStream(64)
	go func() {
		if p.seen <= p.toolRounds {
			result.Push(provider.AssistantMessageEvent{
				Type:         provider.EventToolCallEnd,
				ContentIndex: 0,
				ToolCall: &provider.ContentBlock{
					Type:          provider.ContentBlockToolCall,
					ToolCallID:    fmt.Sprintf("call_%d", p.seen),
					ToolName:      "mock_tool",
					ToolArguments: `{"arg":"value"}`,
				},
			})
		} else {
			result.Push(provider.AssistantMessageEvent{
				Type: provider.EventTextStart, ContentIndex: 0,
			})
			result.Push(provider.AssistantMessageEvent{
				Type: provider.EventTextDelta, ContentIndex: 0, Delta: "done",
			})
			result.Push(provider.AssistantMessageEvent{
				Type: provider.EventTextEnd, ContentIndex: 0,
			})
		}
		result.End(&provider.AssistantMessage{
			Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "mock done"}},
			StopReason: provider.StopReasonEndTurn,
		})
	}()
	return result, nil
}

func (p *multiRoundToolProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(model, ctx, provider.StreamOptions{})
}

func TestAgent_ToolCallRounds_NotLimitedToThree(t *testing.T) {
	toolRounds := 5
	p := &multiRoundToolProvider{
		api:        provider.Api(fmt.Sprintf("test-multi-round-%d", testProviderCounter.Add(1))),
		toolRounds: toolRounds,
	}
	provider.RegisterApiProvider(p)

	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
		Tools: []Tool{mockTool{
			name:   "mock_tool",
			schema: ToolSchema{Name: "mock_tool", Description: "test"},
		}},
		MaxToolRepeatTotal:       0,
		MaxToolRepeatConsecutive: 0, // allow repeated identical calls
		MaxToolCalls:             0, // no per-turn budget
	})

	obs := &mockEventObserver{}
	agent.AddObserver(obs)
	go func() {
		for range agent.Output {
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := agent.Run(ctx, "call tools"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var toolResults int
	var assistantContents []string
	for _, e := range obs.Events() {
		switch e.Type {
		case EventToolResult:
			toolResults++
		case EventContent:
			if e.Role == Assistant && e.Text != "" {
				assistantContents = append(assistantContents, e.Text)
			}
		}
	}

	if toolResults != toolRounds {
		t.Errorf("expected %d tool results, got %d", toolRounds, toolResults)
	}
	if len(assistantContents) == 0 || assistantContents[len(assistantContents)-1] != "done" {
		t.Errorf("expected final assistant content 'done', got %v", assistantContents)
	}
}

// TestAgent_ExactToolRepeatGuard_5Percent triggers the consecutive-repeat
// guardrail. With MaxToolRepeatConsecutive=3, the fourth consecutive identical
// call is rejected with a loop hint while preserving the assistant message
// structure.
func TestAgent_ExactToolRepeatGuard_5Percent(t *testing.T) {
	totalCalls := 4
	p := registerBatchToolProvider(totalCalls)

	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
		Tools: []Tool{mockTool{
			name:   "mock_tool",
			schema: ToolSchema{Name: "mock_tool", Description: "test"},
		}},
		MaxToolRepeatTotal:       0, // disable total-repeat guardrail
		MaxToolRepeatConsecutive: 3, // allow up to 3 consecutive identical calls
		MaxToolCalls:             0, // disable rolling-window guardrail
	})

	obs := runAgentCollectingEvents(t, agent, "call tools")

	var realResults, loopResults, repeatResults int
	for _, e := range obs.Events() {
		if e.Type != EventToolResult {
			continue
		}
		switch {
		case strings.Contains(e.Text, "Loop guardrail"):
			loopResults++
		case strings.Contains(e.Text, "already executed"):
			repeatResults++
		default:
			realResults++
		}
	}
	// With MaxToolRepeatConsecutive=3 and 4 identical calls (fixed semantics):
	// calls 1,2,3: executed (consecutive count ≤ limit 3)
	// call 4: hard loop (4th consecutive call, exceeds limit 3)
	if realResults != 3 {
		t.Errorf("expected 3 real executions before loop guardrail, got %d (repeat=%d loop=%d)", realResults, repeatResults, loopResults)
	}
	if repeatResults != 0 {
		t.Errorf("expected 0 soft-repeat results (no more 2nd/3rd-call soft-skip), got %d", repeatResults)
	}
	if loopResults != 1 {
		t.Errorf("expected 1 hard-loop result at 4th consecutive call, got %d", loopResults)
	}
}

// repeatTextProvider always returns the same text response, used to test
// assistant-message loop detection.
type repeatTextProvider struct {
	api     provider.Api
	content string
}

func (p *repeatTextProvider) API() provider.Api { return p.api }

func (p *repeatTextProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	result := provider.NewAssistantMessageEventStream(64)
	go func() {
		result.Push(provider.AssistantMessageEvent{Type: provider.EventTextStart, ContentIndex: 0})
		result.Push(provider.AssistantMessageEvent{Type: provider.EventTextDelta, ContentIndex: 0, Delta: p.content})
		result.Push(provider.AssistantMessageEvent{Type: provider.EventTextEnd, ContentIndex: 0})
		result.End(&provider.AssistantMessage{
			Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: p.content}},
			StopReason: provider.StopReasonEndTurn,
		})
	}()
	return result, nil
}

func (p *repeatTextProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(model, ctx, provider.StreamOptions{})
}

// TestAgent_AssistantRepeat_WarnsThenStops verifies that two consecutive
// identical assistant text responses first inject a warning hint and then
// stop the session with a clear error.
