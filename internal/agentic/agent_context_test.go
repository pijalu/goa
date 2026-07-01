// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"errors"
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

// contextToolCallProvider emits a single tool call.
type contextToolCallProvider struct {
	api provider.Api
}

func (p *contextToolCallProvider) API() provider.Api { return p.api }

func (p *contextToolCallProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	result := provider.NewAssistantMessageEventStream(64)
	go func() {
		result.Push(provider.AssistantMessageEvent{
			Type:         provider.EventToolCallEnd,
			ContentIndex: 0,
			ToolCall: &provider.ContentBlock{
				Type:          provider.ContentBlockToolCall,
				ToolCallID:    "call_ctx_1",
				ToolName:      "blocker",
				ToolArguments: `{}`,
			},
		})
		result.End(&provider.AssistantMessage{
			Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "calling tool"}},
			StopReason: provider.StopReasonEndTurn,
		})
	}()
	return result, nil
}

func (p *contextToolCallProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(model, ctx, provider.StreamOptions{})
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
		res, _ := agent.executeToolWithResult(ctx, "blocker", "{}")
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

	res, err := agent.executeToolWithResult(context.Background(), "plain", "{}")
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
