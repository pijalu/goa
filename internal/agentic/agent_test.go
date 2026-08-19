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

// flakyStartProvider simulates a provider whose initial HTTP request fails a
// configurable number of times before opening a stream. Unlike flakyTestProvider,
// the error is returned directly from Stream() rather than pushed inside the
// event stream. This exercises the retry path for connection errors such as
// HTTP 408 that happen before any SSE events are received.
type flakyStartProvider struct {
	api           provider.Api
	mu            sync.Mutex
	failures      int
	err           error
	successEvents []provider.AssistantMessageEvent
}

func (p *flakyStartProvider) API() provider.Api { return p.api }

func (p *flakyStartProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	p.mu.Lock()
	shouldFail := p.failures > 0
	if shouldFail {
		p.failures--
	}
	events := p.successEvents
	p.mu.Unlock()

	if shouldFail {
		return nil, p.err
	}

	result := provider.NewAssistantMessageEventStream(64)
	go func() {
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

func (p *flakyStartProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
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

func registerFlakyStartProvider(failures int, err error, successEvents []provider.AssistantMessageEvent) *flakyStartProvider {
	uniqueID := testProviderCounter.Add(1)
	p := &flakyStartProvider{
		api:           provider.Api(fmt.Sprintf("test-flaky-start-%d", uniqueID)),
		failures:      failures,
		err:           err,
		successEvents: successEvents,
	}
	provider.RegisterApiProvider(p)
	return p
}

// scriptedStreamProvider plays back a fixed script of per-call outcomes so
// tests can exercise multi-episode retry scenarios (fail → recover → fail
// again) with distinct behavior on each Stream call. Calls beyond the script
// repeat the last step.
type scriptedStreamProvider struct {
	api   provider.Api
	mu    sync.Mutex
	steps []scriptedStreamStep
	calls int
}

type scriptedStreamStep struct {
	err    error                            // when non-nil, Stream fails with this
	events []provider.AssistantMessageEvent // else a successful stream with these
}

func (p *scriptedStreamProvider) API() provider.Api { return p.api }

func (p *scriptedStreamProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	p.mu.Lock()
	i := p.calls
	p.calls++
	p.mu.Unlock()
	if i >= len(p.steps) {
		i = len(p.steps) - 1
	}
	step := p.steps[i]
	if step.err != nil {
		return nil, step.err
	}
	result := provider.NewAssistantMessageEventStream(64)
	go func() {
		for _, e := range step.events {
			result.Push(e)
		}
		result.End(&provider.AssistantMessage{
			Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "done"}},
			StopReason: provider.StopReasonEndTurn,
		})
	}()
	return result, nil
}

func (p *scriptedStreamProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	base := provider.BuildSimpleOptions(model, opts)
	return p.Stream(model, ctx, base)
}

// Calls returns how many times Stream was invoked.
func (p *scriptedStreamProvider) Calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
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
	// The structured payload carries the strategy label and before/after
	// usage; the summary text lives in Compaction.Detail so every surface can
	// render the pass without parsing free text.
	if compactEvent.Compaction == nil {
		t.Fatalf("expected structured Compaction payload, got Text=%q", compactEvent.Text)
	}
	if compactEvent.Compaction.Strategy != "summarize" {
		t.Errorf("Compaction.Strategy = %q, want summarize", compactEvent.Compaction.Strategy)
	}
	if !strings.Contains(compactEvent.Compaction.Detail, "Summary") {
		t.Errorf("expected summary in Compaction.Detail, got %q", compactEvent.Compaction.Detail)
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
	// Compaction must produce a valid, provider-friendly role sequence: the
	// system prompt is carried via Context.SystemPrompt (not duplicated in
	// history), and the summary is an assistant reply to a user turn so the
	// history is not assistant-first and the next user input alternates.
	if history[0].Role != User {
		t.Errorf("expected first to be user (no system duplication), got %v", history[0].Role)
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
