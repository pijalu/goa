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
	"github.com/pijalu/goa/internal/agentic/provider/hooks"
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

// flakyTestProvider simulates a provider that fails a configurable number of
// times with a stream error and then succeeds. Used to verify the agent retry
// path.
type flakyTestProvider struct {
	api           provider.Api
	mu            sync.Mutex
	failures      int
	successEvents []provider.AssistantMessageEvent
}

func (p *flakyTestProvider) API() provider.Api { return p.api }

func (p *flakyTestProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	result := provider.NewAssistantMessageEventStream(64)
	p.mu.Lock()
	shouldFail := p.failures > 0
	if shouldFail {
		p.failures--
	}
	events := p.successEvents
	p.mu.Unlock()

	go func() {
		if shouldFail {
			result.Push(provider.AssistantMessageEvent{
				Type:  provider.EventTextDelta,
				Delta: "Let",
			})
			result.CloseWithError(fmt.Errorf("SSE stream ended prematurely: no finish_reason or [DONE] marker"))
			return
		}
		for _, e := range events {
			result.Push(e)
		}
		result.End(&provider.AssistantMessage{
			Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "Recovered"}},
			StopReason: provider.StopReasonEndTurn,
		})
	}()
	return result, nil
}

func (p *flakyTestProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	base := provider.BuildSimpleOptions(model, opts)
	return p.Stream(model, ctx, base)
}

func registerFlakyTestProvider(failures int, successEvents []provider.AssistantMessageEvent) *flakyTestProvider {
	uniqueID := testProviderCounter.Add(1)
	p := &flakyTestProvider{
		api:           provider.Api(fmt.Sprintf("test-flaky-%d", uniqueID)),
		failures:      failures,
		successEvents: successEvents,
	}
	provider.RegisterApiProvider(p)
	return p
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
	if realResults != 1 {
		t.Errorf("expected 1 real execution, got %d (soft=%d hard=%d)", realResults, softResults, hardResults)
	}
	if softResults != 2 {
		t.Errorf("expected 2 soft-repeat results, got %d", softResults)
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
	// With MaxToolRepeatConsecutive=2 and identical calls:
	// call 1: executed
	// call 2: soft repeat (2nd consecutive call)
	// call 3: hard loop (3rd consecutive call)
	// call 4: hard loop
	if realResults != 1 {
		t.Errorf("expected 1 real execution, got %d (soft=%d hard=%d)", realResults, softResults, hardResults)
	}
	if softResults != 1 {
		t.Errorf("expected 1 soft-repeat result with limit=2, got %d", softResults)
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

	// Under the duplicate-window semantics, only the first identical call is
	// executed; the remaining three are guardrail hints.
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
	if realResults != 1 {
		t.Errorf("expected 1 real result, got %d (guards=%d)", realResults, guardResults)
	}
	if guardResults != 3 {
		t.Errorf("expected 3 guard results, got %d", guardResults)
	}
}

// TestAgent_DisableToolBudget_AllowsUnlimitedCalls verifies that setting
// DisableToolBudget to true prevents duplicate-window and consecutive-repeat
// guardrail messages.
func TestAgent_DisableToolBudget_AllowsUnlimitedCalls(t *testing.T) {
	totalCalls := 4
	maxCalls := 2 // Low limit, but DisableToolBudget should override

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
		MaxToolRepeatConsecutive: 0,
		MaxToolCalls:             maxCalls,
		DisableToolBudget:        true,
	})

	obs := runAgentCollectingEvents(t, agent, "call tools")

	history := copyAgentHistory(agent)

	assertAllToolResultsPresent(t, history, totalCalls)

	// Verify NO guardrail messages appeared in tool results.
	for _, msg := range history {
		if msg.Role == ToolRole && (strings.Contains(msg.Content, "Loop guardrail") || strings.Contains(msg.Content, "already executed") || strings.Contains(msg.Content, "budget exceeded")) {
			t.Errorf("unexpected guardrail tool result with DisableToolBudget=true: %q", msg.Content)
		}
	}

	var realResults int
	for _, e := range obs.Events() {
		if e.Type == EventToolResult && !strings.Contains(e.Text, "Loop guardrail") && !strings.Contains(e.Text, "already executed") && !strings.Contains(e.Text, "budget exceeded") {
			realResults++
		}
	}
	if realResults != totalCalls {
		t.Errorf("expected %d real executions with DisableToolBudget=true, got %d", totalCalls, realResults)
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

// sequenceToolProvider emits a configurable sequence of tool-call arguments
// on its first stream, then a plain text response on subsequent streams.
// Used to verify duplicate detection when the same call is spaced out by
// different calls.
type sequenceToolProvider struct {
	api      provider.Api
	args     []string
	attempts int
}

func (p *sequenceToolProvider) API() provider.Api { return p.api }

func (p *sequenceToolProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	p.attempts++
	result := provider.NewAssistantMessageEventStream(64)
	go func() {
		if p.attempts == 1 {
			for i, arg := range p.args {
				result.Push(provider.AssistantMessageEvent{
					Type:         provider.EventToolCallEnd,
					ContentIndex: i,
					ToolCall: &provider.ContentBlock{
						Type:          provider.ContentBlockToolCall,
						ToolCallID:    fmt.Sprintf("call_%d", i+1),
						ToolName:      "mock_tool",
						ToolArguments: fmt.Sprintf(`{"arg":%q}`, arg),
					},
				})
			}
			result.Push(provider.AssistantMessageEvent{
				Type: provider.EventTextStart, ContentIndex: len(p.args),
			})
			result.Push(provider.AssistantMessageEvent{
				Type: provider.EventTextDelta, ContentIndex: len(p.args), Delta: "final summary",
			})
			result.Push(provider.AssistantMessageEvent{
				Type: provider.EventTextEnd, ContentIndex: len(p.args),
			})
		} else {
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

func (p *sequenceToolProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(model, ctx, provider.StreamOptions{})
}

func registerSequenceToolProvider(args []string) *sequenceToolProvider {
	p := &sequenceToolProvider{
		api:  provider.Api(fmt.Sprintf("test-sequence-%d", testProviderCounter.Add(1))),
		args: args,
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
		MaxToolRepeatTotal:       0,
		MaxToolRepeatConsecutive: 0,
		MaxToolCalls:             maxCalls,
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

func assertToolGuardResult(t *testing.T, history []Message) {
	for _, msg := range history {
		if msg.Role == ToolRole && (strings.Contains(msg.Content, "Loop guardrail") || strings.Contains(msg.Content, "already executed")) {
			return
		}
	}
	t.Errorf("expected a tool result with a loop-guard or repeat hint in history")
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
	// With MaxToolCalls=3 and identical calls:
	// call 1: executed
	// call 2: soft-repeat (already executed)
	// call 3: soft-repeat (still within limit)
	// call 4: hard loop (4th duplicate in window)
	if realResults != 1 {
		t.Errorf("expected 1 real execution, got %d (repeat=%d loop=%d)", realResults, repeatResults, loopResults)
	}
	if repeatResults != 2 {
		t.Errorf("expected 2 soft-repeat results, got %d", repeatResults)
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
	if realResults != 1 {
		t.Errorf("expected 1 real execution before loop guardrail, got %d (repeat=%d loop=%d)", realResults, repeatResults, loopResults)
	}
	if repeatResults != 2 {
		t.Errorf("expected 2 soft-repeat results at 2nd and 3rd consecutive calls, got %d", repeatResults)
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

func TestAgent_RetriesStreamError(t *testing.T) {
	p := registerFlakyTestProvider(1, []provider.AssistantMessageEvent{
		{Type: provider.EventTextDelta, Delta: "Recovered"},
	})
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
	if err := agent.Run(ctx, "prompt"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var contents []string
	var endWithError bool
	for _, e := range obs.Events() {
		if e.Type == EventContent && e.Role == Assistant {
			contents = append(contents, e.Text)
		}
		if e.Type == EventEnd && e.Text != "" {
			endWithError = true
		}
	}
	if endWithError {
		t.Error("expected retry to succeed, but EventEnd carried an error")
	}
	if !containsContent(contents, "Recovered") {
		t.Errorf("expected recovered assistant content, got %q", contents)
	}
}

func TestAgent_RetriesStreamError_EmitsSystemNotification(t *testing.T) {
	p := registerFlakyTestProvider(1, []provider.AssistantMessageEvent{
		{Type: provider.EventTextDelta, Delta: "Recovered"},
	})
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
	if err := agent.Run(ctx, "prompt"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var notifications []OutputEvent
	for _, e := range obs.Events() {
		if e.Type == EventContent && e.Role == System && e.Metadata != nil && e.Metadata["category"] == "system-notification" {
			notifications = append(notifications, e)
		}
	}
	if len(notifications) != 1 {
		t.Fatalf("expected 1 system notification, got %d", len(notifications))
	}
	if !strings.Contains(notifications[0].Text, "retrying") {
		t.Errorf("expected notification to mention retrying, got %q", notifications[0].Text)
	}
}

// testResponseError is a test double for provider.HTTPResponseError.
type testResponseError struct {
	status int
	body   string
}

func (e *testResponseError) Error() string        { return fmt.Sprintf("test error %d", e.status) }
func (e *testResponseError) StatusCode() int      { return e.status }
func (e *testResponseError) ResponseBody() string { return e.body }

func TestFormatRetryMessage(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "openai-style error body",
			err: &testResponseError{
				status: 503,
				body:   `{"error":{"message":"Inference is temporarily unavailable","type":"server_error","code":"failover_exhausted"}}`,
			},
			want: "Error: 503 - Inference is temporarily unavailable (failover_exhausted) - retrying",
		},
		{
			name: "plain http error",
			err:  &testResponseError{status: 500, body: "internal server error"},
			want: "Error: 500 - internal server error - retrying",
		},
		{
			name: "provider error from hooks",
			err: (&hooks.ErrorContext{
				StatusCode: 503,
				Body:       `{"error":{"message":"Inference is temporarily unavailable","code":"failover_exhausted"}}`,
			}).ToError(),
			want: "Error: 503 - Inference is temporarily unavailable (failover_exhausted) - retrying",
		},
		{
			name: "generic error",
			err:  fmt.Errorf("SSE stream ended prematurely"),
			want: "Error: SSE stream ended prematurely - retrying",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatRetryMessage(tc.err)
			if got != tc.want {
				t.Errorf("formatRetryMessage() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAgent_StreamErrorRetriesExhausted(t *testing.T) {
	p := registerFlakyTestProvider(3, []provider.AssistantMessageEvent{
		{Type: provider.EventTextDelta, Delta: "Never reached"},
	})
	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
	})

	go func() {
		for range agent.Output {
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := agent.Run(ctx, "prompt")
	if err == nil {
		t.Fatal("expected error after retries exhausted, got nil")
	}
	if !strings.Contains(err.Error(), "LLM connection lost after retries") {
		t.Errorf("expected retries-exhausted error, got %v", err)
	}
}

func containsContent(contents []string, text string) bool {
	for _, c := range contents {
		if strings.Contains(c, text) {
			return true
		}
	}
	return false
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
