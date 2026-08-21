// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

func (p *panicObserver) OnEvent(event OutputEvent) {
	panic("intentional panic")
}

func TestEmitMessage_TokenStats(t *testing.T) {
	agent := NewAgent(Config{})
	var receivedEvents []OutputEvent
	agent.AddObserver(&testObserver{events: &receivedEvents})

	agent.emitMessage(Message{
		Type:    Content,
		Role:    Assistant,
		Content: "Hello",
		Timings: &TokenTimings{
			PromptN:            10,
			PredictedN:         5,
			PromptMs:           100,
			PredictedMs:        200,
			PredictedPerSecond: 25.0,
		},
	})

	foundStats := false
	for _, e := range receivedEvents {
		if e.Type == EventTokenStats {
			foundStats = true
			if e.Timings == nil {
				t.Fatal("expected Timings in token_stats event")
			}
			if e.Timings.PromptN != 10 {
				t.Errorf("expected PromptN=10, got %d", e.Timings.PromptN)
			}
		}
	}
	if !foundStats {
		t.Error("expected EventTokenStats")
	}
}

func TestEmitMessage_ContentAndStats(t *testing.T) {
	agent := NewAgent(Config{})
	var receivedEvents []OutputEvent
	agent.AddObserver(&testObserver{events: &receivedEvents})

	agent.emitMessage(Message{
		Type:    Content,
		Role:    Assistant,
		Content: "Result: 42",
		Timings: &TokenTimings{PromptN: 10, PredictedN: 5},
	})

	foundContent := false
	foundStats := false
	for _, e := range receivedEvents {
		if e.Type == EventContent && e.Text == "Result: 42" {
			foundContent = true
		}
		if e.Type == EventTokenStats {
			foundStats = true
		}
	}
	if !foundContent {
		t.Error("expected content event")
	}
	if !foundStats {
		t.Error("expected token_stats event")
	}
}

type testObserver struct {
	events *[]OutputEvent
}

func (t *testObserver) OnEvent(event OutputEvent) {
	*t.events = append(*t.events, event)
}

func TestAgent_SetHistory(t *testing.T) {
	agent := NewAgent(Config{
		SystemPrompt: "You are helpful",
		Logger:       NewLogger(Error),
	})

	history := []Message{
		{Type: Content, Role: User, Content: "hello"},
		{Type: Content, Role: Assistant, Content: "hi there"},
	}

	agent.SetHistory(history)

	result := agent.GetHistory()
	if len(result) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(result))
	}
	if result[0].Role != System {
		t.Errorf("expected first to be system, got %v", result[0].Role)
	}
	if result[0].Content != "You are helpful" {
		t.Errorf("expected system prompt, got %q", result[0].Content)
	}
}

func TestAgent_SetHistory_WithExistingSystem(t *testing.T) {
	agent := NewAgent(Config{
		SystemPrompt: "You are helpful",
		Logger:       NewLogger(Error),
	})

	history := []Message{
		{Type: Content, Role: System, Content: "Custom system"},
		{Type: Content, Role: User, Content: "hello"},
	}

	agent.SetHistory(history)

	result := agent.GetHistory()
	if len(result) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result))
	}
	if result[0].Content != "Custom system" {
		t.Errorf("expected custom system prompt, got %q", result[0].Content)
	}
}

func TestAgent_GetHistory_IsCopy(t *testing.T) {
	agent := NewAgent(Config{
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
	})

	agent.SetHistory([]Message{
		{Type: Content, Role: User, Content: "hello"},
	})

	history := agent.GetHistory()
	history[1].Content = "modified"

	result := agent.GetHistory()
	if result[1].Content != "hello" {
		t.Errorf("GetHistory should return a copy, got %q", result[1].Content)
	}
}

func TestAgent_BuildProviderContext_DeduplicatesSystemPrompt(t *testing.T) {
	agent := NewAgent(Config{
		SystemPrompt: "You are helpful",
		Logger:       NewLogger(Error),
	})

	agent.SetHistory([]Message{
		{Type: Content, Role: System, Content: "You are helpful"},
		{Type: Content, Role: User, Content: "hello"},
		{Type: Content, Role: Assistant, Content: "hi"},
	})

	ctx := agent.buildProviderContext(context.Background())

	if ctx.SystemPrompt != "You are helpful" {
		t.Errorf("expected SystemPrompt to be set, got %q", ctx.SystemPrompt)
	}

	systemCount := 0
	for _, m := range ctx.Messages {
		if m.Role == provider.RoleSystem {
			systemCount++
		}
	}
	if systemCount != 0 {
		t.Errorf("expected 0 system messages in provider context, got %d", systemCount)
	}

	userCount := 0
	for _, m := range ctx.Messages {
		if m.Role == provider.RoleUser {
			userCount++
		}
	}
	if userCount != 1 {
		t.Errorf("expected 1 user message, got %d", userCount)
	}
}

func TestAgent_BuildProviderContext_KeepsSystemWhenNoSeparatePrompt(t *testing.T) {
	agent := NewAgent(Config{
		Logger: NewLogger(Error),
	})

	agent.SetHistory([]Message{
		{Type: Content, Role: System, Content: "You are helpful"},
		{Type: Content, Role: User, Content: "hello"},
	})

	ctx := agent.buildProviderContext(context.Background())

	if ctx.SystemPrompt != "" {
		t.Errorf("expected empty SystemPrompt, got %q", ctx.SystemPrompt)
	}

	systemCount := 0
	for _, m := range ctx.Messages {
		if m.Role == provider.RoleSystem {
			systemCount++
		}
	}
	if systemCount != 1 {
		t.Errorf("expected 1 system message when no separate prompt, got %d", systemCount)
	}
}

func TestAgent_MigrateMessage_AssistantWithToolCalls(t *testing.T) {
	msg := Message{
		Type:    Content,
		Role:    Assistant,
		Content: "",
		ToolCalls: []ToolCallInfo{{
			ID:        "call_1",
			Type:      "function",
			Name:      "read",
			Arguments: `{"path":"README.md"}`,
		}},
	}

	pm := migrateMessage(msg)
	if pm.Role != provider.RoleAssistant {
		t.Fatalf("expected role assistant, got %v", pm.Role)
	}
	if len(pm.Content) != 2 {
		t.Fatalf("expected 2 content blocks (tool_call + text), got %d", len(pm.Content))
	}
	if pm.Content[0].Type != provider.ContentBlockToolCall {
		t.Errorf("expected first block to be tool_call, got %v", pm.Content[0].Type)
	}
	if pm.Content[0].ToolCallID != "call_1" {
		t.Errorf("expected tool_call_id call_1, got %q", pm.Content[0].ToolCallID)
	}
	if pm.Content[0].ToolName != "read" {
		t.Errorf("expected tool_name read, got %q", pm.Content[0].ToolName)
	}
	if pm.Content[1].Type != provider.ContentBlockText {
		t.Errorf("expected second block to be text, got %v", pm.Content[1].Type)
	}
}

func TestAgent_BuildProviderContext_IncludesToolCalls(t *testing.T) {
	agent := NewAgent(Config{
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
	})

	agent.SetHistory([]Message{
		{Type: Content, Role: User, Content: "summarize"},
		{Type: Content, Role: Assistant, Content: "", ToolCalls: []ToolCallInfo{{
			ID: "call_1", Type: "function", Name: "read", Arguments: `{"path":"PLAN.md"}`,
		}}},
		{Type: Content, Role: ToolRole, Content: "file contents", ToolName: "read", ToolCallID: "call_1"},
	})

	ctx := agent.buildProviderContext(context.Background())
	if len(ctx.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(ctx.Messages))
	}

	assistant := ctx.Messages[1]
	if assistant.Role != provider.RoleAssistant {
		t.Fatalf("expected assistant message, got %v", assistant.Role)
	}
	toolCallFound := false
	for _, b := range assistant.Content {
		if b.Type == provider.ContentBlockToolCall && b.ToolCallID == "call_1" {
			toolCallFound = true
		}
	}
	if !toolCallFound {
		t.Errorf("expected assistant message to contain tool_call block with id call_1")
	}

	toolResult := ctx.Messages[2]
	if toolResult.Role != provider.RoleToolResult {
		t.Fatalf("expected tool_result message, got %v", toolResult.Role)
	}
	toolResultFound := false
	for _, b := range toolResult.Content {
		if b.Type == provider.ContentBlockToolResult && b.ToolCallID == "call_1" {
			toolResultFound = true
		}
	}
	if !toolResultFound {
		t.Errorf("expected tool_result message to contain tool_result block with id call_1")
	}
}

func TestAgent_ToolBudget_DifferentCallsNotBlocked(t *testing.T) {
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
	if realResults != 5 {
		t.Errorf("expected 5 real executions for 5 different calls, got %d (guards=%d)", realResults, guardResults)
	}
	if guardResults != 0 {
		t.Errorf("expected 0 guard results for different calls, got %d", guardResults)
	}
}

// TestAgent_ToolBudget_RollingWindowDuplicate verifies that repeating the same
// tool call within the rolling window triggers the duplicate guard after the
// configured limit, and that the LLM receives a clear hint in the tool result.
func TestAgent_ToolBudget_RollingWindowDuplicate(t *testing.T) {
	totalCalls := 4
	maxCalls := 3

	p := registerBatchToolProvider(totalCalls)
	agent := newAgentWithMockTool(p.API(), maxCalls)
	obs := runAgentCollectingEvents(t, agent, "call tools")

	var realResults, softResults, hardResults int
	for _, e := range obs.Events() {
		if e.Type != EventToolResult {
			continue
		}
		switch {
		case strings.Contains(e.Text, "already executed"):
			softResults++
		case strings.Contains(e.Text, "Loop guardrail"):
			hardResults++
		default:
			realResults++
		}
	}
	// With MaxToolCalls=3 (rolling-window limit) and 4 identical calls (fixed
	// semantics): calls 1,2,3 execute (window count ≤ 3); call 4 is a hard-loop
	// guard (window count 4 > limit 3). The old over-sensitive soft-skip at the
	// 2nd call is gone.
	if realResults != 3 {
		t.Errorf("expected 3 real executions, got %d (soft=%d hard=%d)", realResults, softResults, hardResults)
	}
	if softResults != 0 {
		t.Errorf("expected 0 soft-repeat results (no more 2nd-call soft-skip), got %d", softResults)
	}
	if hardResults != 1 {
		t.Errorf("expected 1 hard-loop result (4th duplicate), got %d", hardResults)
	}

	history := copyAgentHistory(agent)
	assertSingleAssistantWithTools(t, history, totalCalls)
	assertAllToolResultsPresent(t, history, totalCalls)
	assertToolEventCounts(t, obs.Events(), totalCalls)
	assertToolGuardResult(t, history)
}

// TestAgent_ToolBudget_ConsecutiveDuplicate verifies that the consecutive-repeat
// guard fires independently of the rolling-window guard.
func TestAgent_ToolBudget_ConsecutiveDuplicate(t *testing.T) {
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
		MaxToolRepeatTotal:       0,
		MaxToolRepeatConsecutive: 2,
		MaxToolCalls:             0, // disable rolling-window guard
	})
	obs := runAgentCollectingEvents(t, agent, "call tools")

	var realResults, softResults, hardResults int
	for _, e := range obs.Events() {
		if e.Type != EventToolResult {
			continue
		}
		switch {
		case strings.Contains(e.Text, "already executed"):
			softResults++
		case strings.Contains(e.Text, "Loop guardrail"):
			hardResults++
		default:
			realResults++
		}
	}
	// With MaxToolRepeatConsecutive=2 and identical calls (fixed semantics):
	// call 1: executed
	// call 2: executed (a 2nd consecutive repeat is legitimate — NOT skipped)
	// call 3: hard loop (3rd consecutive call, exceeds limit 2)
	// call 4: hard loop
	if realResults != 2 {
		t.Errorf("expected 2 real executions, got %d (soft=%d hard=%d)", realResults, softResults, hardResults)
	}
	if softResults != 0 {
		t.Errorf("expected 0 soft-repeat results (2nd call now executes), got %d", softResults)
	}
	if hardResults != 2 {
		t.Errorf("expected 2 hard-loop results, got %d", hardResults)
	}
}

// TestAgent_ToolBudget_NonConsecutiveDuplicateNotFlagged verifies that when
// the same tool call is spaced out by a different call (A, B, A), the second
// A is not treated as a soft duplicate. Only truly consecutive duplicates
// should trigger the soft-repeat hint; the rolling window is reserved for the
// hard-loop guard at the configured limit.
func TestAgent_ToolBudget_NonConsecutiveDuplicateNotFlagged(t *testing.T) {
	args := []string{"A", "B", "A"}
	p := registerSequenceToolProvider(args)
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
	if realResults != 3 {
		t.Errorf("expected 3 real executions for A,B,A, got %d (guards=%d)", realResults, guardResults)
	}
	if guardResults != 0 {
		t.Errorf("expected 0 guard results for A,B,A, got %d", guardResults)
	}
}

// TestAgent_ToolBudget_LLMReceivesHintAndContinues verifies that when a
// duplicate guard fires, the model receives the hint as a tool result and the
// turn continues (the second provider stream returns text).
func TestAgent_ToolBudget_LLMReceivesHintAndContinues(t *testing.T) {
	totalCalls := 3
	maxCalls := 2

	p := registerBatchToolProvider(totalCalls)
	agent := newAgentWithMockTool(p.API(), maxCalls)
	runAgentCollectingEvents(t, agent, "call tools")

	history := copyAgentHistory(agent)
	assertToolGuardResult(t, history)

	// The turn should have continued after the guard: there must be at least
	// one text-only assistant response following the tool results.
	var textResponses int
	for _, msg := range history {
		if msg.Role == Assistant && len(msg.ToolCalls) == 0 && msg.Content != "" {
			textResponses++
		}
	}
	if textResponses == 0 {
		t.Errorf("expected the turn to continue with a text response after the guard")
	}
}

// batchToolProvider emits N tool calls in a single stream on its FIRST
// invocation only. Subsequent streams return a plain text response so the
// agent can finish after budget-exceeded re-streams.
type batchToolProvider struct {
	api      provider.Api
	nCalls   int
	attempts int
}

func (p *batchToolProvider) API() provider.Api { return p.api }

func (p *batchToolProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	p.attempts++
	result := provider.NewAssistantMessageEventStream(64)
	go func() {
		// First stream: emit tool calls.
		if p.attempts == 1 {
			for i := 0; i < p.nCalls; i++ {
				callID := fmt.Sprintf("call_%d", i+1)
				result.Push(provider.AssistantMessageEvent{
					Type:         provider.EventToolCallEnd,
					ContentIndex: i,
					ToolCall: &provider.ContentBlock{
						Type:          provider.ContentBlockToolCall,
						ToolCallID:    callID,
						ToolName:      "mock_tool",
						ToolArguments: `{"arg":"value"}`,
					},
				})
			}
			result.Push(provider.AssistantMessageEvent{
				Type: provider.EventTextStart, ContentIndex: p.nCalls,
			})
			result.Push(provider.AssistantMessageEvent{
				Type: provider.EventTextDelta, ContentIndex: p.nCalls, Delta: "final summary",
			})
			result.Push(provider.AssistantMessageEvent{
				Type: provider.EventTextEnd, ContentIndex: p.nCalls,
			})
		} else {
			// Subsequent streams: text only so the agent can finish.
			result.Push(provider.AssistantMessageEvent{
				Type: provider.EventTextStart, ContentIndex: 0,
			})
			result.Push(provider.AssistantMessageEvent{
				Type: provider.EventTextDelta, ContentIndex: 0, Delta: "ok finished",
			})
			result.Push(provider.AssistantMessageEvent{
				Type: provider.EventTextEnd, ContentIndex: 0,
			})
		}
		result.End(&provider.AssistantMessage{
			Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "done"}},
			StopReason: provider.StopReasonEndTurn,
		})
	}()
	return result, nil
}

func (p *batchToolProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(model, ctx, provider.StreamOptions{})
}

func TestAgent_MaxToolCalls_MidBatch_LeavesSingleAssistantMessage(t *testing.T) {
	totalCalls := 4
	maxCalls := 3

	p := registerBatchToolProvider(totalCalls)
	agent := newAgentWithMockTool(p.API(), maxCalls)
	obs := runAgentCollectingEvents(t, agent, "call tools")

	history := copyAgentHistory(agent)
	assertSingleAssistantWithTools(t, history, totalCalls)
	assertAllToolResultsPresent(t, history, totalCalls)
	assertToolGuardResult(t, history)
	assertToolEventCounts(t, obs.Events(), totalCalls)

	// Under the fixed duplicate-window semantics, the first three identical
	// calls execute (window count ≤ MaxToolCalls=3); the 4th is a guardrail hint.
	var realResults, guardResults int
	for _, msg := range history {
		if msg.Role != ToolRole {
			continue
		}
		if strings.Contains(msg.Content, "Loop guardrail") || strings.Contains(msg.Content, "already executed") {
			guardResults++
		} else {
			realResults++
		}
	}
	if realResults != 3 {
		t.Errorf("expected 3 real results, got %d (guards=%d)", realResults, guardResults)
	}
	if guardResults != 1 {
		t.Errorf("expected 1 guard result, got %d", guardResults)
	}
}

// TestAgent_DisableToolBudget_AllowsUnlimitedCalls verifies that setting
// DisableToolBudget to true prevents duplicate-window and consecutive-repeat
// guardrail messages.
