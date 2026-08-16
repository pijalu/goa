// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package multiagent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/provider"
)

func TestForegroundOrchestrator_New(t *testing.T) {
	pool := NewAgentPool(testModel("test-model"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)
	if orch == nil {
		t.Fatal("NewForegroundOrchestrator returned nil")
	}
	if orch.pool != pool {
		t.Error("expected pool reference to be stored")
	}
}

func TestForegroundOrchestrator_SetMode_GetMode(t *testing.T) {
	pool := NewAgentPool(testModel("test-model"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)

	if orch.Mode() != WorkflowInactive {
		t.Errorf("expected initial mode WorkflowInactive, got %v", orch.Mode())
	}

	orch.SetMode(WorkflowCompanionMinor)
	if orch.Mode() != WorkflowCompanionMinor {
		t.Errorf("expected WorkflowCompanionMinor, got %v", orch.Mode())
	}
}

func TestForegroundOrchestrator_SetMainAgent(t *testing.T) {
	pool := NewAgentPool(testModel("test-model"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)

	// Just verify SetMainAgent doesn't panic
	orch.SetMainAgent(nil)
}

func TestForegroundOrchestrator_Events_ChannelCreated(t *testing.T) {
	pool := NewAgentPool(testModel("test-model"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)

	ch := orch.Events()
	if ch == nil {
		t.Fatal("Events() returned nil channel")
	}
}

func TestForegroundOrchestrator_InjectSteering(t *testing.T) {
	pool := NewAgentPool(testModel("test-model"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)

	// Inject steering and verify it's available via checkSteering
	orch.InjectSteering("skip this task")
	text, ok := orch.checkSteering()
	if !ok {
		t.Fatal("expected steering to be available")
	}
	if text != "skip this task" {
		t.Errorf("expected 'skip this task', got %q", text)
	}
}

func TestForegroundOrchestrator_InjectSteering_Multiple(t *testing.T) {
	pool := NewAgentPool(testModel("test-model"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)

	// With shared queue semantics, multiple steering messages are buffered
	// and merged when consumed.
	orch.InjectSteering("first")
	orch.InjectSteering("second")

	text1, ok := orch.checkSteering()
	if !ok {
		t.Fatal("expected steering to be available")
	}
	want := "first\n\nsecond"
	if text1 != want {
		t.Errorf("expected %q, got %q", want, text1)
	}

	// Queue should be empty after draining.
	_, ok = orch.checkSteering()
	if ok {
		t.Error("expected no more steering messages after draining")
	}
}

func TestForegroundOrchestrator_Stop_Stopped(t *testing.T) {
	pool := NewAgentPool(testModel("test-model"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)

	if orch.Stopped() {
		t.Error("expected not stopped initially")
	}

	orch.Stop()
	if !orch.Stopped() {
		t.Error("expected stopped after Stop()")
	}
}

func TestForegroundOrchestrator_Stop_Idempotent(t *testing.T) {
	pool := NewAgentPool(testModel("test-model"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)

	orch.Stop()
	orch.Stop() // should not panic
	if !orch.Stopped() {
		t.Error("expected stopped after multiple Stop() calls")
	}
}

func TestForegroundOrchestrator_Emit_NonBlocking(t *testing.T) {
	pool := NewAgentPool(testModel("test-model"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)

	// Fill the events buffer
	for i := 0; i < 100; i++ {
		orch.emit("system", "user", "test message")
	}
	// 101st should not block (select + default)
	orch.emit("system", "user", "overflow message")
}

func TestForegroundOrchestrator_CollectLastMessage_NoOutput(t *testing.T) {
	pool := NewAgentPool(testModel("test-model"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)

	msg := orch.CollectLastMessage("companion")
	if msg != "" {
		t.Errorf("expected empty string for no output, got %q", msg)
	}
}

func TestForegroundOrchestrator_CollectLastMessage_AfterRecord(t *testing.T) {
	pool := NewAgentPool(testModel("test-model"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)

	orch.RecordOutput("companion", "companion output text")
	msg := orch.CollectLastMessage("companion")
	if msg != "companion output text" {
		t.Errorf("expected 'companion output text', got %q", msg)
	}
}

func TestForegroundOrchestrator_RecordOutput_Overwrites(t *testing.T) {
	pool := NewAgentPool(testModel("test-model"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)

	orch.RecordOutput("companion", "first output")
	orch.RecordOutput("companion", "updated output")

	msg := orch.CollectLastMessage("companion")
	if msg != "updated output" {
		t.Errorf("expected 'updated output', got %q", msg)
	}
}

func TestForegroundOrchestrator_AfterMainTurn_NotCompanionMinor(t *testing.T) {
	pool := NewAgentPool(testModel("test-model"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)

	// Without companion-minor mode, AfterMainTurn should return nil immediately
	err := orch.AfterMainTurn(context.TODO(), "some output")
	if err != nil {
		t.Errorf("expected nil error for non-companion mode, got %v", err)
	}
}

func TestAfterMainTurn_FeedsBackToMainAgent(t *testing.T) {
	pool := NewAgentPool(testModel("test-model"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)
	orch.SetMode(WorkflowCompanionMinor)

	bus := agentic.NewAgentBus()
	mainInbox, err := bus.Register("main")
	if err != nil {
		t.Fatalf("register main: %v", err)
	}
	orch.SetAgentBus(bus)

	ctx := context.Background()
	if err := orch.AfterMainTurn(ctx, "func main() {}"); err != nil {
		t.Fatalf("AfterMainTurn error: %v", err)
	}

	select {
	case msg := <-mainInbox:
		if msg.From != "companion" {
			t.Errorf("expected from companion, got %q", msg.From)
		}
		if msg.To != "main" {
			t.Errorf("expected to main, got %q", msg.To)
		}
		if !strings.Contains(msg.Content, "Test response from mock provider") {
			t.Errorf("expected mock review text, got %q", msg.Content)
		}
		if !strings.Contains(msg.Content, "Message from companion:") {
			t.Errorf("expected labeled companion message, got %q", msg.Content)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for bus message")
	}
}

func TestAfterMainTurn_EndDelimiterStopsLoop(t *testing.T) {
	pool := NewAgentPool(modelWithResponse(" Looks good. "+DefaultCompanionEndDelimiter), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)
	orch.SetMode(WorkflowCompanionMinor)

	bus := agentic.NewAgentBus()
	mainInbox, err := bus.Register("main")
	if err != nil {
		t.Fatalf("register main: %v", err)
	}
	orch.SetAgentBus(bus)

	ctx := context.Background()
	if err := orch.AfterMainTurn(ctx, "func main() {}"); err != nil {
		t.Fatalf("AfterMainTurn error: %v", err)
	}

	select {
	case <-mainInbox:
		t.Fatal("expected no bus message when delimiter is present")
	case <-ctx.Done():
		t.Fatal("timeout")
	default:
	}
}

func TestAfterMainTurn_MaxCyclesStopsLoop(t *testing.T) {
	pool := NewAgentPool(testModel("test-model"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)
	orch.SetMode(WorkflowCompanionMinor)
	orch.SetCompanionMaxMessages(1)

	bus := agentic.NewAgentBus()
	mainInbox, err := bus.Register("main")
	if err != nil {
		t.Fatalf("register main: %v", err)
	}
	orch.SetAgentBus(bus)

	ctx := context.Background()
	if err := orch.AfterMainTurn(ctx, "first"); err != nil {
		t.Fatalf("AfterMainTurn error: %v", err)
	}

	// First call sends a message.
	select {
	case <-mainInbox:
	case <-ctx.Done():
		t.Fatal("timeout")
	}

	// Second call should hit the cycle cap and not send.
	if err := orch.AfterMainTurn(ctx, "second"); err != nil {
		t.Fatalf("AfterMainTurn error: %v", err)
	}
	select {
	case <-mainInbox:
		t.Fatal("expected no bus message after max cycles")
	case <-ctx.Done():
		t.Fatal("timeout")
	default:
	}
}

func TestAfterMainTurn_EmitsCompanionCalled(t *testing.T) {
	pool := NewAgentPool(testModel("test-model"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)
	orch.SetMode(WorkflowCompanionMinor)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	// Without a bus, the call should fail after running the companion but still
	// emit "Companion called" synchronously.
	go orch.AfterMainTurn(ctx, "func main() {}")

	select {
	case msg := <-orch.Events():
		if msg.From != "system" || msg.To != "companion" {
			t.Errorf("expected system/companion message, got %s/%s", msg.From, msg.To)
		}
		if msg.Content != "Companion called" {
			t.Errorf("expected 'Companion called', got %q", msg.Content)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for companion called message")
	}
}

func TestForegroundOrchestrator_CompanionCount(t *testing.T) {
	pool := NewAgentPool(testModel("test-model"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)

	cur, max := orch.CompanionCount()
	if cur != 0 {
		t.Errorf("expected current count 0, got %d", cur)
	}
	if max != defaultCompanionMaxMessages {
		t.Errorf("expected default max %d, got %d", defaultCompanionMaxMessages, max)
	}

	orch.SetCompanionMaxMessages(5)
	_, max = orch.CompanionCount()
	if max != 5 {
		t.Errorf("expected max 5, got %d", max)
	}

	orch.ResetCompanionCount()
	cur, _ = orch.CompanionCount()
	if cur != 0 {
		t.Errorf("expected current count 0 after reset, got %d", cur)
	}
}

func TestAfterMainTurn_EmptyReviewSkipped(t *testing.T) {
	pool := NewAgentPool(modelWithResponse(""), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)
	orch.SetMode(WorkflowCompanionMinor)

	bus := agentic.NewAgentBus()
	mainInbox, err := bus.Register("main")
	if err != nil {
		t.Fatalf("register main: %v", err)
	}
	orch.SetAgentBus(bus)

	ctx := context.Background()
	if err := orch.AfterMainTurn(ctx, "func main() {}"); err != nil {
		t.Fatalf("AfterMainTurn error: %v", err)
	}

	select {
	case <-mainInbox:
		t.Fatal("expected no bus message for empty review")
	case <-ctx.Done():
		t.Fatal("timeout")
	default:
	}
}

// failingProvider is a test-only provider whose Stream always fails (e.g. a
// slow-LLM context deadline), so the companion run aborts before EventEnd.
type failingProvider struct {
	api provider.Api
	err error
}

func (p *failingProvider) API() provider.Api { return p.api }

func (p *failingProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	return nil, p.err
}

func (p *failingProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return nil, p.err
}

func failingModel(text string) provider.Model {
	api := provider.Api("test-failing-api-" + text)
	provider.RegisterApiProvider(&failingProvider{api: api, err: context.DeadlineExceeded})
	return provider.Model{
		ID:         "failing-model",
		Name:       "failing-model",
		Api:        api,
		Provider:   provider.ProviderCustom,
		InputTypes: []string{"text"},
		BaseURL:    "http://localhost:9999/v1/chat/completions",
	}
}

// TestAfterMainTurn_FailureEmitsCompanionStreamEnd is the regression test for
// the bugs.md entry "team: run breaks on slow LLM — stuck screen, incorrect
// statusbar". When the companion run fails mid-stream (a slow-LLM deadline),
// no EventEnd fires so no stream_end is emitted — leaving the footer stuck on
// "reviewing"/busy and the transcript section open. AfterMainTurn must emit a
// companion stream_end on failure so the UI always returns to idle.
func TestAfterMainTurn_FailureEmitsCompanionStreamEnd(t *testing.T) {
	// A non-eligible retry policy (empty Codes) so the failing companion
	// surfaces immediately instead of burning the real 5-retry backoff
	// schedule (~31s) — keeps the test fast while still exercising the
	// failure path.
	pool := NewAgentPool(failingModel("boom"), provider.StreamOptions{
		RetryPolicy: &provider.RetryPolicy{Mode: provider.RetryModeNormal, MaxRetries: 1, Codes: []string{}},
	}, nil)
	orch := NewForegroundOrchestrator(pool)
	orch.SetMode(WorkflowCompanionMinor)

	ctx := context.Background()
	err := orch.AfterMainTurn(ctx, "func main() {}")
	if err == nil {
		t.Fatal("expected companion run error from failing provider")
	}

	// Drain events, looking for the companion cleanup stream_end.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case msg := <-orch.Events():
			if msg.From == "companion" && msg.To == "stream_end" && msg.Kind == "content" {
				return // success: cleanup stream_end emitted
			}
		case <-deadline:
			t.Fatal("no companion stream_end emitted after companion failure — UI would stay stuck busy")
		}
	}
}

// responseProvider is a test-only provider that returns a fixed text response.
type responseProvider struct {
	api  provider.Api
	text string
}

func (p *responseProvider) API() provider.Api { return p.api }

func (p *responseProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	result := provider.NewAssistantMessageEventStream(64)
	go func() {
		if p.text != "" {
			result.Push(provider.AssistantMessageEvent{
				Type:         provider.EventTextStart,
				ContentIndex: 0,
			})
			result.Push(provider.AssistantMessageEvent{
				Type:         provider.EventTextDelta,
				ContentIndex: 0,
				Delta:        p.text,
			})
			result.Push(provider.AssistantMessageEvent{
				Type:         provider.EventTextEnd,
				ContentIndex: 0,
			})
		}
		result.End(&provider.AssistantMessage{
			Content: []provider.ContentBlock{
				{Type: provider.ContentBlockText, Text: p.text},
			},
		})
	}()
	return result, nil
}

func (p *responseProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	base := provider.BuildSimpleOptions(model, opts)
	return p.Stream(model, ctx, base)
}

func modelWithResponse(text string) provider.Model {
	api := provider.Api("test-response-api-" + text)
	provider.RegisterApiProvider(&responseProvider{api: api, text: text})
	return provider.Model{
		ID:         "response-model",
		Name:       "response-model",
		Api:        api,
		Provider:   provider.ProviderCustom,
		InputTypes: []string{"text"},
		BaseURL:    "http://localhost:9999/v1/chat/completions",
	}
}

// NewTestAgent creates a minimal agent for testing history injection.
func NewTestAgent() *agentic.Agent {
	return agentic.NewAgent(agentic.Config{
		SystemPrompt: "test",
		Logger:       agentic.NewLogger(agentic.Error),
	})
}
