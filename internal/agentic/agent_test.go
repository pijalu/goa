// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// mockEventObserver captures all events for assertions.
type mockEventObserver struct {
	mu     sync.Mutex
	events []OutputEvent
}

func (m *mockEventObserver) OnEvent(event OutputEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, event)
}

func (m *mockEventObserver) Events() []OutputEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]OutputEvent, len(m.events))
	copy(result, m.events)
	return result
}

func (m *mockEventObserver) HasEventType(et EventType) bool {
	for _, e := range m.Events() {
		if e.Type == et {
			return true
		}
	}
	return false
}

// textEventProvider returns predetermined text content delta events.
func textEventProvider(text string) *testAPIProvider {
	return registerTestProvider("text-events", []provider.AssistantMessageEvent{
		{Type: provider.EventTextStart, ContentIndex: 0},
		{Type: provider.EventTextDelta, ContentIndex: 0, Delta: text},
		{Type: provider.EventTextEnd, ContentIndex: 0},
	})
}

func TestAgent_AddRemoveObserver(t *testing.T) {
	agent := NewAgent(Config{
		Model:        testModel("test-observer-api"),
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
	})

	obs := &mockEventObserver{}
	agent.AddObserver(obs)
	if len(agent.observers) != 1 {
		t.Errorf("expected 1 observer, got %d", len(agent.observers))
	}

	agent.RemoveObserver(obs)
	if len(agent.observers) != 0 {
		t.Errorf("expected 0 observers, got %d", len(agent.observers))
	}
}

func TestAgent_EmitsSystemAndUserEvents(t *testing.T) {
	p := textEventProvider("Hello")
	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "You are helpful",
		Logger:       NewLogger(Error),
	})

	obs := &mockEventObserver{}
	agent.AddObserver(obs)

	runAgentToDone(t, agent, "Hi")

	if !obs.HasEventType(EventContent) {
		t.Error("expected EventContent events")
	}
	if !obs.HasEventType(EventEnd) {
		t.Error("expected EventEnd")
	}
	assertEventObserved(t, obs.Events(), EventContent, System, "helpful")
	assertEventObserved(t, obs.Events(), EventContent, User, "Hi")
}

func runAgentToDone(t *testing.T, agent *Agent, prompt string) {
	t.Helper()
	ctx := context.Background()
	done := make(chan struct{})
	go func() {
		_ = agent.Run(ctx, prompt)
		close(done)
	}()
	go func() {
		for range agent.Output {
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for Run")
	}
	agent.Stop()
}

func assertEventObserved(t *testing.T, events []OutputEvent, wantType EventType, wantRole Role, wantText string) {
	t.Helper()
	for _, e := range events {
		if e.Type == wantType && e.Role == wantRole && strings.Contains(e.Text, wantText) {
			return
		}
	}
	t.Errorf("expected event type=%s role=%s containing %q", wantType, wantRole, wantText)
}

func TestAgent_ConversationContinuation(t *testing.T) {
	p := textEventProvider("Response")
	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "You are helpful",
		Logger:       NewLogger(Error),
	})

	go func() {
		for range agent.Output {
		}
	}()

	ctx := context.Background()
	if err := agent.Run(ctx, "First input"); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	if err := agent.Run(ctx, "Second input"); err != nil {
		t.Fatalf("second Run: %v", err)
	}

	agent.Stop()

	agent.mu.Lock()
	historyLen := len(agent.history)
	agent.mu.Unlock()

	if historyLen < 4 {
		t.Errorf("expected at least 4 history messages, got %d", historyLen)
	}
}

func TestAgent_QueueInputsWhileProcessing(t *testing.T) {
	p := textEventProvider("Response")
	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
	})

	go func() {
		for range agent.Output {
		}
	}()

	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Run(context.Background(), "input1")
	}()

	time.Sleep(50 * time.Millisecond)

	err := agent.Run(context.Background(), "input2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Wait for processing to complete
	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for first Run")
	}

	agent.mu.Lock()
	queueLen := len(agent.queue)
	agent.mu.Unlock()
	if queueLen != 0 {
		t.Errorf("expected queue to be empty, got %d", queueLen)
	}
	agent.Stop()
}

func TestAgent_ClearResetsHistory(t *testing.T) {
	agent := NewAgent(Config{
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
	})

	obs := &mockEventObserver{}
	agent.AddObserver(obs)

	agent.history = []Message{
		{Type: Content, Role: User, Content: "hello"},
	}

	agent.Clear()

	agent.mu.Lock()
	historyLen := len(agent.history)
	agent.mu.Unlock()

	if historyLen != 0 {
		t.Errorf("expected empty history after Clear, got %d", historyLen)
	}

	if !obs.HasEventType(EventClear) {
		t.Error("expected EventClear to be emitted")
	}
}

func TestAgent_ClearCancelsProcessing(t *testing.T) {
	p := textEventProvider("slow response")
	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
	})

	go func() {
		for range agent.Output {
		}
	}()
	go agent.Run(context.Background(), "hello")
	time.Sleep(50 * time.Millisecond)

	agent.Clear()

	agent.mu.Lock()
	processing := agent.processing
	agent.mu.Unlock()
	if processing {
		t.Error("expected processing to be false after Clear")
	}
}

func TestAgent_ClearEmitsClearEvent(t *testing.T) {
	agent := NewAgent(Config{
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
	})

	obs := &mockEventObserver{}
	agent.AddObserver(obs)

	agent.Clear()

	events := obs.Events()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != EventClear {
		t.Errorf("expected EventClear, got %s", events[0].Type)
	}
}

func TestAgent_StopCancelsProcessing(t *testing.T) {
	agent := NewAgent(Config{
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
	})

	agent.mu.Lock()
	agent.processing = true
	agent.mu.Unlock()

	agent.Stop()

	agent.mu.Lock()
	if agent.processing {
		t.Error("expected processing to be false after Stop")
	}
	agent.mu.Unlock()
}

func TestAgent_CompactEmitsCompactEvent(t *testing.T) {
	p := textEventProvider("Summary: user greeted assistant")
	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
	})

	agent.history = []Message{
		{Type: Content, Role: System, Content: "test"},
		{Type: Content, Role: User, Content: "hello"},
		{Type: Content, Role: Assistant, Content: "hi there"},
	}

	obs := &mockEventObserver{}
	agent.AddObserver(obs)

	ctx := context.Background()
	err := agent.Compact(ctx)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}

	if !obs.HasEventType(EventCompact) {
		t.Error("expected EventCompact to be emitted")
	}

	events := obs.Events()
	var compactEvent *OutputEvent
	for i := range events {
		if events[i].Type == EventCompact {
			compactEvent = &events[i]
			break
		}
	}
	if compactEvent == nil {
		t.Fatal("expected compact event")
	}
	if !strings.Contains(compactEvent.Text, "Summary") {
		t.Errorf("expected summary text, got %q", compactEvent.Text)
	}
}

func TestAgent_CompactReplacesHistory(t *testing.T) {
	p := textEventProvider("Summary of conversation")
	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "You are helpful",
		Logger:       NewLogger(Error),
	})

	agent.history = []Message{
		{Type: Content, Role: System, Content: "You are helpful"},
		{Type: Content, Role: User, Content: "hello"},
		{Type: Content, Role: Assistant, Content: "hi there"},
		{Type: Content, Role: User, Content: "how are you"},
	}

	ctx := context.Background()
	err := agent.Compact(ctx)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}

	agent.mu.Lock()
	history := make([]Message, len(agent.history))
	copy(history, agent.history)
	agent.mu.Unlock()

	if len(history) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(history))
	}
	if history[0].Role != System {
		t.Errorf("expected first to be system, got %v", history[0].Role)
	}
	if history[1].Role != Assistant {
		t.Errorf("expected second to be assistant, got %v", history[1].Role)
	}
	if !strings.Contains(history[1].Content, "Summary") {
		t.Errorf("expected summary, got %q", history[1].Content)
	}
}

func TestAgent_CompactEmptyHistory(t *testing.T) {
	agent := NewAgent(Config{
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
	})

	ctx := context.Background()
	err := agent.Compact(ctx)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}

	agent.mu.Lock()
	if len(agent.history) != 0 {
		t.Errorf("expected empty history, got %d", len(agent.history))
	}
	agent.mu.Unlock()
}

func TestAgent_ObserverPanicRecovered(t *testing.T) {
	p := textEventProvider("Hi")
	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
	})

	panicker := &panicObserver{}
	normal := &mockEventObserver{}

	agent.AddObserver(panicker)
	agent.AddObserver(normal)

	go func() {
		for range agent.Output {
		}
	}()
	go agent.Run(context.Background(), "hello")
	time.Sleep(200 * time.Millisecond)
	agent.Stop()

	if len(normal.Events()) == 0 {
		t.Error("normal observer should have received events despite panicker")
	}
}

type panicObserver struct{}

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

// toolCountingProvider emits a tool call on the first N streams and then
// a text response, allowing tests to exercise the per-turn tool-call budget.
type toolCountingProvider struct {
	api     provider.Api
	mu      sync.Mutex
	streams int
}

func (p *toolCountingProvider) API() provider.Api { return p.api }

func (p *toolCountingProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	p.mu.Lock()
	p.streams++
	callID := fmt.Sprintf("call_%d", p.streams)
	p.mu.Unlock()

	result := provider.NewAssistantMessageEventStream(64)
	go func() {
		result.Push(provider.AssistantMessageEvent{
			Type:         provider.EventToolCallEnd,
			ContentIndex: 0,
			ToolCall: &provider.ContentBlock{
				Type:          provider.ContentBlockToolCall,
				ToolCallID:    callID,
				ToolName:      "mock_tool",
				ToolArguments: `{"arg":"value"}`,
			},
		})
		result.End(&provider.AssistantMessage{
			Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "done"}},
			StopReason: provider.StopReasonEndTurn,
		})
	}()
	return result, nil
}

func (p *toolCountingProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(model, ctx, provider.StreamOptions{})
}

func TestAgent_MaxToolCalls_BlocksAdditionalCalls(t *testing.T) {
	p := &toolCountingProvider{api: provider.Api(fmt.Sprintf("test-max-tool-calls-%d", testProviderCounter.Add(1)))}
	provider.RegisterApiProvider(p)

	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
		Tools: []Tool{mockTool{
			name:   "mock_tool",
			schema: ToolSchema{Name: "mock_tool", Description: "test"},
		}},
		MaxToolRepeatTotal:       0, // allow repeated identical calls
		MaxToolRepeatConsecutive: 0,
		MaxToolCalls:             2,
	})

	obs := &mockEventObserver{}
	agent.AddObserver(obs)
	go func() {
		for range agent.Output {
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := agent.Run(ctx, "call tools"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var toolResults []OutputEvent
	for _, e := range obs.Events() {
		if e.Type == EventToolResult {
			toolResults = append(toolResults, e)
		}
	}

	if len(toolResults) < 3 {
		t.Fatalf("expected at least 3 tool results (1 executed + 1 repeat + 1 budget exceeded), got %d", len(toolResults))
	}

	// First result is a real execution.
	if toolResults[0].Text != "mock result" {
		t.Errorf("expected first result to be real execution, got %q", toolResults[0].Text)
	}

	// Second result should be the repeat message (2nd consecutive same call).
	if !strings.Contains(toolResults[1].Text, "already executed") {
		t.Errorf("expected second result to mention 'already executed', got %q", toolResults[1].Text)
	}

	// Third result should be the budget-exceeded message.
	if !strings.Contains(toolResults[2].Text, "budget exceeded") {
		t.Errorf("expected third result to mention budget exceeded, got %q", toolResults[2].Text)
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
	assertBudgetToolResult(t, history)
	assertToolEventCounts(t, obs.Events(), totalCalls)
}

// TestAgent_DisableToolBudget_AllowsUnlimitedCalls verifies that setting
// DisableToolBudget to true prevents budget-exceeded messages even when
// the number of tool calls exceeds MaxToolCalls.
func TestAgent_DisableToolBudget_AllowsUnlimitedCalls(t *testing.T) {
	totalCalls := 4
	maxCalls := 2 // Low limit, but DisableToolBudget should override

	p := registerBatchToolProvider(totalCalls)
	agent := NewAgent(Config{
		Model:             testModel(p.API()),
		SystemPrompt:      "test",
		Logger:            NewLogger(Error),
		Tools: []Tool{mockTool{
			name:   "mock_tool",
			schema: ToolSchema{Name: "mock_tool", Description: "test"},
		}},
		MaxToolRepeatTotal:       0,
		MaxToolRepeatConsecutive: 0,
		MaxToolCalls:             maxCalls,
		DisableToolBudget:        true,
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

	history := copyAgentHistory(agent)

	// All tool results should be present (none should be budget-exceeded).
	assertAllToolResultsPresent(t, history, totalCalls)

	// Verify NO budget-exceeded messages appeared in tool results.
	for _, msg := range history {
		if msg.Role == ToolRole && strings.Contains(msg.Content, "budget exceeded") {
			t.Errorf("unexpected budget-exceeded tool result with DisableToolBudget=true: %q", msg.Content)
		}
	}
}

func registerBatchToolProvider(totalCalls int) *batchToolProvider {
	p := &batchToolProvider{
		api:    provider.Api(fmt.Sprintf("test-mid-batch-budget-%d", testProviderCounter.Add(1))),
		nCalls: totalCalls,
	}
	provider.RegisterApiProvider(p)
	return p
}

func newAgentWithMockTool(api provider.Api, maxCalls int) *Agent {
	return NewAgent(Config{
		Model:        testModel(api),
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
		Tools: []Tool{mockTool{
			name:   "mock_tool",
			schema: ToolSchema{Name: "mock_tool", Description: "test"},
		}},
		MaxToolRepeatTotal: 0,
		MaxToolRepeatConsecutive: 0,
		MaxToolCalls:  maxCalls,
	})
}

func runAgentCollectingEvents(t *testing.T, agent *Agent, prompt string) *mockEventObserver {
	obs := &mockEventObserver{}
	agent.AddObserver(obs)
	go func() {
		for range agent.Output {
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := agent.Run(ctx, prompt); err != nil {
		t.Fatalf("Run: %v", err)
	}
	return obs
}

func copyAgentHistory(agent *Agent) []Message {
	agent.mu.Lock()
	defer agent.mu.Unlock()
	history := make([]Message, len(agent.history))
	copy(history, agent.history)
	return history
}

func assertSingleAssistantWithTools(t *testing.T, history []Message, totalCalls int) {
	var assistantWithTools int
	var toolsAssistant *Message
	for i := range history {
		if history[i].Role == Assistant {
			if len(history[i].ToolCalls) > 0 {
				assistantWithTools++
				toolsAssistant = &history[i]
			}
		}
	}
	if assistantWithTools != 1 {
		t.Errorf("expected exactly 1 assistant with tool_calls, got %d", assistantWithTools)
	}
	if toolsAssistant == nil {
		t.Fatal("no assistant message with tool_calls found")
	}
	if len(toolsAssistant.ToolCalls) != totalCalls {
		t.Errorf("expected assistant message to have %d tool_calls, got %d", totalCalls, len(toolsAssistant.ToolCalls))
	}
}

func assertAllToolResultsPresent(t *testing.T, history []Message, totalCalls int) {
	var toolResultCount int
	for _, msg := range history {
		if msg.Role == ToolRole {
			toolResultCount++
		}
	}
	if toolResultCount != totalCalls {
		t.Errorf("expected %d tool results in history, got %d. Messages:\n", totalCalls, toolResultCount)
		for i, m := range history {
			t.Logf("  [%d] %s: %s (tool_calls=%d)", i, m.Role, m.Content[:min(len(m.Content), 60)], len(m.ToolCalls))
		}
	}
}

func assertBudgetToolResult(t *testing.T, history []Message) {
	for _, msg := range history {
		if msg.Role == ToolRole && strings.Contains(msg.Content, "budget exceeded") {
			return
		}
	}
	t.Errorf("expected a tool result with budget message in history")
	for i, m := range history {
		t.Logf("  [%d] %s: %.60s (tool_calls=%d)", i, m.Role, m.Content, len(m.ToolCalls))
	}
}

func assertToolEventCounts(t *testing.T, events []OutputEvent, totalCalls int) {
	var tcCount, trCount int
	for _, e := range events {
		switch e.Type {
		case EventToolCall:
			tcCount++
		case EventToolResult:
			trCount++
		}
	}
	if tcCount != totalCalls {
		t.Errorf("expected %d EventToolCall events, got %d", totalCalls, tcCount)
	}
	if trCount != totalCalls {
		t.Errorf("expected %d EventToolResult events, got %d", totalCalls, trCount)
	}
}

// uniqueArgToolProvider emits a tool call with a unique argument on its
// first N streams, then a plain text response so the agent can finish.
type uniqueArgToolProvider struct {
	api        provider.Api
	mu         sync.Mutex
	streams    int
	totalCalls int
}

func (p *uniqueArgToolProvider) API() provider.Api { return p.api }

func (p *uniqueArgToolProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	p.mu.Lock()
	p.streams++
	stream := p.streams
	p.mu.Unlock()

	result := provider.NewAssistantMessageEventStream(64)
	go func() {
		if stream <= p.totalCalls {
			result.Push(provider.AssistantMessageEvent{
				Type:         provider.EventToolCallEnd,
				ContentIndex: 0,
				ToolCall: &provider.ContentBlock{
					Type:          provider.ContentBlockToolCall,
					ToolCallID:    fmt.Sprintf("call_%d", stream),
					ToolName:      "mock_tool",
					ToolArguments: fmt.Sprintf(`{"arg":"%d"}`, stream),
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

func (p *uniqueArgToolProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(model, ctx, provider.StreamOptions{})
}

func registerUniqueArgToolProvider(totalCalls int) *uniqueArgToolProvider {
	p := &uniqueArgToolProvider{
		api:        provider.Api(fmt.Sprintf("test-unique-arg-%d", testProviderCounter.Add(1))),
		totalCalls: totalCalls,
	}
	provider.RegisterApiProvider(p)
	return p
}

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
		MaxToolRepeatTotal: 0,
		MaxToolRepeatConsecutive: 0,
		MaxToolCalls:  3,
	})

	obs := runAgentCollectingEvents(t, agent, "call tools")

	var realResults int
	var budgetResults int
	for _, e := range obs.Events() {
		if e.Type != EventToolResult {
			continue
		}
		if strings.Contains(e.Text, "budget exceeded") {
			budgetResults++
		} else {
			realResults++
		}
	}
	// With MaxToolCalls=3 and 5 unique calls: calls 1-3 execute (global
	// budget), call 4 is budget-exceeded and ends the turn.
	if realResults != 3 {
		t.Errorf("expected 3 real executions (budget=3), got %d (budget=%d)", realResults, budgetResults)
	}
	if budgetResults != 1 {
		t.Errorf("expected 1 budget result, got %d", budgetResults)
	}
}

func TestAgent_ToolCallLimitEnforcedOnRepeatedCall(t *testing.T) {
	p := &toolCountingProvider{
		api:     provider.Api(fmt.Sprintf("test-repeated-budget-%d", testProviderCounter.Add(1))),
		streams: 0,
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
		MaxToolRepeatTotal: 0,
		MaxToolRepeatConsecutive: 0,
		MaxToolCalls:  3,
	})

	obs := runAgentCollectingEvents(t, agent, "call tools")

	var realResults, repeatResults, loopResults, budgetResults int
	for _, e := range obs.Events() {
		if e.Type != EventToolResult {
			continue
		}
		switch {
		case strings.Contains(e.Text, "budget exceeded"):
			budgetResults++
		case strings.Contains(e.Text, "already executed"):
			repeatResults++
		case strings.Contains(e.Text, "Loop guardrail"):
			loopResults++
		default:
			realResults++
		}
	}
	// With MaxToolCalls=3 and identical calls:
	// call 1: executed (repeatCount=1)
	// call 2: soft-repeat (toolRepeatedMessage)
	// call 3: hard loop (repeatCount=3, toolLoopMessage)
	// call 4: budget exceeded (totalCalls=4 > MaxToolCalls=3), ends turn
	if realResults != 1 {
		t.Errorf("expected 1 real execution, got %d (repeat=%d loop=%d budget=%d)", realResults, repeatResults, loopResults, budgetResults)
	}
	if budgetResults != 1 {
		t.Errorf("expected 1 budget result, got %d", budgetResults)
	}
}

func TestAgent_ToolCallLimit_WindowCustom(t *testing.T) {
	p := registerUniqueArgToolProvider(5)
	agent := NewAgent(Config{
		Model:                    testModel(p.API()),
		SystemPrompt:             "test",
		Logger:                   NewLogger(Error),
		Tools:                    []Tool{mockTool{name: "mock_tool", schema: ToolSchema{Name: "mock_tool", Description: "test"}}},
		MaxToolRepeatTotal:            0,
		MaxToolRepeatConsecutive:            0,
		MaxToolCalls:             10,
		ToolCallLimitResetWindow: 5,
	})

	// The custom window is honored: all unique calls execute.
	obs := runAgentCollectingEvents(t, agent, "call tools")

	var realResults int
	var budgetResults int
	for _, e := range obs.Events() {
		if e.Type != EventToolResult {
			continue
		}
		if strings.Contains(e.Text, "budget exceeded") {
			budgetResults++
		} else {
			realResults++
		}
	}
	if realResults != 5 {
		t.Errorf("expected 5 real executions, got %d (budget=%d)", realResults, budgetResults)
	}
	if budgetResults != 0 {
		t.Errorf("expected 0 budget results, got %d", budgetResults)
	}
}

func TestAgent_ToolCallLimit_BudgetMessageReturnedToLLM(t *testing.T) {
	p := &toolCountingProvider{
		api:     provider.Api(fmt.Sprintf("test-budget-history-%d", testProviderCounter.Add(1))),
		streams: 0,
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
		MaxToolRepeatTotal: 0,
		MaxToolRepeatConsecutive: 0,
		MaxToolCalls:  2,
	})

	runAgentCollectingEvents(t, agent, "call tools")

	history := copyAgentHistory(agent)
	assertBudgetToolResult(t, history)

	// Ensure the budget result is a ToolRole message, not an error returned
	// by Run.
	var found bool
	for _, msg := range history {
		if msg.Role == ToolRole && strings.Contains(msg.Content, ToolBudgetResultPrefix) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a ToolRole message containing %q in history", ToolBudgetResultPrefix)
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
	assertProviderContextContains(t, agent.buildProviderContext(context.Background()), "additional system info")
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

func assertProviderContextContains(t *testing.T, pCtx provider.Context, want string) {
	t.Helper()
	for _, m := range pCtx.Messages {
		if m.Role != provider.RoleSystem {
			continue
		}
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
		MaxToolRepeatTotal: 0,
		MaxToolRepeatConsecutive: 0, // allow repeated identical calls
		MaxToolCalls:  0, // no per-turn budget
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
// guardrail. With MaxToolRepeatConsecutive=3, the third consecutive identical
// call is rejected with a loop hint while preserving the assistant message
// structure.
func TestAgent_ExactToolRepeatGuard_5Percent(t *testing.T) {
	p := &toolCountingProvider{
		api:     provider.Api(fmt.Sprintf("test-exact-repeat-%d", testProviderCounter.Add(1))),
		streams: 0,
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
		MaxToolRepeatTotal: 0, // disable total-repeat guardrail
		MaxToolRepeatConsecutive: 3, // stop after 3 consecutive identical calls
		MaxToolCalls:  20,
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
		case strings.Contains(e.Text, "budget exceeded"):
			// budget guard is separate
		default:
			realResults++
		}
	}
	if realResults != 1 {
		t.Errorf("expected 1 real execution before loop guardrail, got %d (repeat=%d loop=%d)", realResults, repeatResults, loopResults)
	}
	if loopResults < 1 {
		t.Errorf("expected at least 1 loop-guard tool result, got %d", loopResults)
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
func TestAgent_AssistantRepeat_WarnsThenStops(t *testing.T) {
	p := &repeatTextProvider{
		api:     provider.Api(fmt.Sprintf("test-assistant-repeat-%d", testProviderCounter.Add(1))),
		content: "I am stuck.",
	}
	provider.RegisterApiProvider(p)

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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// First run: assistant responds "I am stuck."
	if err := agent.Run(ctx, "help"); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	// Second run: identical response should inject a warning hint.
	if err := agent.Run(ctx, "continue"); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	// Third run: identical response again should stop the session.
	err := agent.Run(ctx, "continue")
	if err == nil {
		t.Fatal("expected runaway loop error, got nil")
	}
	if !strings.Contains(err.Error(), "runaway loop detected") {
		t.Errorf("expected runaway loop error, got %v", err)
	}

	history := copyAgentHistory(agent)
	var warnings int
	for _, m := range history {
		if m.Role == System && strings.Contains(m.Content, "Progress has stalled") {
			warnings++
		}
	}
	if warnings != 1 {
		t.Errorf("expected exactly 1 stall warning in history, got %d", warnings)
	}
}

// TestAgent_EmptyAssistantRepeat_Stops verifies that consecutive empty
// assistant responses (no content, no tool calls) are detected as a stall and
// stop the session before the context explodes.
func TestAgent_EmptyAssistantRepeat_Stops(t *testing.T) {
	p := &repeatTextProvider{
		api:     provider.Api(fmt.Sprintf("test-empty-repeat-%d", testProviderCounter.Add(1))),
		content: "",
	}
	provider.RegisterApiProvider(p)

	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
	})

	go func() {
		for range agent.Output {
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := agent.Run(ctx, "help"); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if err := agent.Run(ctx, "continue"); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	err := agent.Run(ctx, "continue")
	if err == nil {
		t.Fatal("expected runaway loop error for empty repeats, got nil")
	}
	if !strings.Contains(err.Error(), "runaway loop detected") {
		t.Errorf("expected runaway loop error, got %v", err)
	}
}

// TestAgent_ToolResultTooLarge_TruncatesWithNotice verifies that an oversized
// successful tool result is truncated with a clear notice so the LLM can adapt
// and the turn can finish without an opaque error.
func TestAgent_UndoLastAssistantMessage_KeepsPreviousTurn(t *testing.T) {
	agent := NewAgent(Config{SystemPrompt: "test", Logger: NewLogger(Error)})
	agent.history = []Message{
		{Type: Content, Role: System, Content: "test"},
		{Type: Content, Role: User, Content: "first question"},
		{Type: Content, Role: Assistant, Content: "first answer"},
		{Type: Content, Role: User, Content: "second question"},
	}

	agent.undoLastAssistantMessage()

	history := agent.GetHistory()
	if len(history) != 4 {
		t.Fatalf("expected history length 4, got %d", len(history))
	}
	if history[2].Content != "first answer" {
		t.Errorf("expected previous assistant message to be preserved, got %q", history[2].Content)
	}
}

func TestAgent_UndoLastAssistantMessage_RemovesCurrentTurnAssistant(t *testing.T) {
	agent := NewAgent(Config{SystemPrompt: "test", Logger: NewLogger(Error)})
	agent.history = []Message{
		{Type: Content, Role: System, Content: "test"},
		{Type: Content, Role: User, Content: "first question"},
		{Type: Content, Role: Assistant, Content: "first answer"},
		{Type: Content, Role: User, Content: "second question"},
		{Type: Content, Role: Assistant, Content: "partial second answer"},
	}

	agent.undoLastAssistantMessage()

	history := agent.GetHistory()
	if len(history) != 4 {
		t.Fatalf("expected history length 4, got %d", len(history))
	}
	if history[len(history)-1].Role != User {
		t.Errorf("expected last message to be user after undo, got %v", history[len(history)-1].Role)
	}
}

func TestAgent_ToolResultTooLarge_TruncatesWithNotice(t *testing.T) {
	p := registerTestProvider("huge-result", []provider.AssistantMessageEvent{
		{Type: provider.EventToolCallEnd, ContentIndex: 0, ToolCall: &provider.ContentBlock{
			Type: provider.ContentBlockToolCall, ToolCallID: "call_1",
			ToolName: "huge_tool", ToolArguments: `{}`,
		}},
	})

	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
		Tools: []Tool{hugeResultTool{
			name:   "huge_tool",
			schema: ToolSchema{Name: "huge_tool", Description: "test"},
			size:   20000, // well above the 2048-char limit when MaxTokens=8192
		}},
		ContextCompression: ContextCompressionConfig{MaxTokens: 8192},
	})

	obs := &mockEventObserver{}
	agent.AddObserver(obs)
	go func() {
		for range agent.Output {
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := agent.Run(ctx, "call huge tool"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var toolResults []OutputEvent
	for _, e := range obs.Events() {
		if e.Type == EventToolResult {
			toolResults = append(toolResults, e)
		}
	}
	if len(toolResults) == 0 {
		t.Fatal("expected a tool result event")
	}
	result := toolResults[0].Text
	if strings.HasPrefix(result, "Error:") {
		t.Errorf("expected truncated result, got error: %q", result)
	}
	if !strings.Contains(result, "[goa-system] Tool result was truncated") {
		t.Errorf("expected truncation notice, got %q", result)
	}
	if !strings.Contains(result, "original 20000 bytes") {
		t.Errorf("expected original size in notice, got %q", result)
	}
	if len(result) <= 100 {
		t.Errorf("expected non-trivial truncated content, got %q", result)
	}
}
