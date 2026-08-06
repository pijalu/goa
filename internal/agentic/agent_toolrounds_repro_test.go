// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// thinkingToolProvider reproduces the bugs.md Issue 13 pattern (kimi-code k3):
// EVERY round streams thinking tokens and then a single tool call. Because each
// round carries reasoning, the consecutive-tool-rounds streak must reset every
// round and the forced-answer nudge must never fire — no matter how many rounds
// elapse. After finalRound rounds it emits a plain answer so the turn ends.
type thinkingToolProvider struct {
	api        provider.Api
	calls      atomic.Int32
	finalRound int32
}

func (p *thinkingToolProvider) API() provider.Api { return p.api }

func (p *thinkingToolProvider) Stream(_ provider.Model, _ provider.Context, _ provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	result := provider.NewAssistantMessageEventStream(64)
	round := p.calls.Add(1)
	go func() {
		// Reasoning first (this must reset the streak).
		result.Push(provider.AssistantMessageEvent{Type: provider.EventThinkingDelta, Delta: "reasoning about next step "})
		if round <= p.finalRound {
			// Then exactly one tool call this round.
			result.Push(provider.AssistantMessageEvent{
				Type:         provider.EventToolCallEnd,
				ContentIndex: 0,
				ToolCall: &provider.ContentBlock{
					Type:          provider.ContentBlockToolCall,
					ToolCallID:    "call_" + itoa(int(round)),
					ToolName:      "mock_tool",
					ToolArguments: `{"arg":"value"}`,
				},
			})
			result.End(&provider.AssistantMessage{
				Content:    []provider.ContentBlock{{Type: provider.ContentBlockToolCall, ToolCallID: "call_" + itoa(int(round)), ToolName: "mock_tool", ToolArguments: `{"arg":"value"}`}},
				StopReason: provider.StopReasonToolCall,
			})
			return
		}
		// Final round: answer with text, no tool call.
		result.Push(provider.AssistantMessageEvent{Type: provider.EventTextDelta, Delta: "done"})
		result.End(&provider.AssistantMessage{
			Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "done"}},
			StopReason: provider.StopReasonEndTurn,
		})
	}()
	return result, nil
}

func (p *thinkingToolProvider) StreamSimple(m provider.Model, c provider.Context, o provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(m, c, provider.BuildSimpleOptions(m, o))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// batchThinkingToolProvider mirrors the exact k3 export pattern: each round the
// model streams a LARGE thinking block plus a BATCH of several tool calls
// (StopReasonToolCall), then the tools execute and the next round repeats.
// Every round carries reasoning, so the streak must reset each round.
type batchThinkingToolProvider struct {
	api        provider.Api
	calls      atomic.Int32
	finalRound int32
	batchSize  int
}

func (p *batchThinkingToolProvider) API() provider.Api { return p.api }

func (p *batchThinkingToolProvider) Stream(_ provider.Model, _ provider.Context, _ provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	result := provider.NewAssistantMessageEventStream(64)
	round := p.calls.Add(1)
	go func() {
		// Large reasoning block first.
		for i := 0; i < 5; i++ {
			result.Push(provider.AssistantMessageEvent{Type: provider.EventThinkingDelta, Delta: "reasoning step "})
		}
		if round <= p.finalRound {
			var blocks []provider.ContentBlock
			for i := 0; i < p.batchSize; i++ {
				id := "call_" + itoa(int(round)) + "_" + itoa(i)
				result.Push(provider.AssistantMessageEvent{
					Type:         provider.EventToolCallEnd,
					ContentIndex: i,
					ToolCall: &provider.ContentBlock{
						Type:          provider.ContentBlockToolCall,
						ToolCallID:    id,
						ToolName:      "mock_tool",
						ToolArguments: `{"arg":"value"}`,
					},
				})
				blocks = append(blocks, provider.ContentBlock{Type: provider.ContentBlockToolCall, ToolCallID: id, ToolName: "mock_tool", ToolArguments: `{"arg":"value"}`})
			}
			result.End(&provider.AssistantMessage{Content: blocks, StopReason: provider.StopReasonToolCall})
			return
		}
		result.Push(provider.AssistantMessageEvent{Type: provider.EventTextDelta, Delta: "done"})
		result.End(&provider.AssistantMessage{
			Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "done"}},
			StopReason: provider.StopReasonEndTurn,
		})
	}()
	return result, nil
}

func (p *batchThinkingToolProvider) StreamSimple(m provider.Model, c provider.Context, o provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(m, c, provider.BuildSimpleOptions(m, o))
}

// TestConsecutiveToolRounds_BatchThinking_NeverNudges reproduces the k3 export:
// thinking + a batch of tool calls each round. The streak must reset each round.
func TestConsecutiveToolRounds_BatchThinking_NeverNudges(t *testing.T) {
	p := &batchThinkingToolProvider{api: provider.Api("repro-batch-thinking"), finalRound: 20, batchSize: 5}
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
		MaxToolRepeatConsecutive: 0,
		MaxToolCalls:             200,
		MaxConsecutiveToolRounds: 15,
	})

	obs := &mockEventObserver{}
	agent.AddObserver(obs)
	go func() {
		for range agent.Output {
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := agent.Run(ctx, "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	n := 0
	for _, m := range copyAgentHistory(agent) {
		if m.Metadata[metaEphemeral] == "true" {
			n++
		}
	}
	if n != 0 {
		t.Fatalf("forced-answer nudge fired %d times, want 0 (each round had thinking+batched tools)", n)
	}
}

// contentToolProvider reproduces the UI evidence in bugs.md Issue 13: each round
// the model emits a short "Let me ..." content message AND a tool call. There is
// never a run of message-less tool calls, so the streak must stay reset.
type contentToolProvider struct {
	api        provider.Api
	calls      atomic.Int32
	finalRound int32
}

func (p *contentToolProvider) API() provider.Api { return p.api }

func (p *contentToolProvider) Stream(_ provider.Model, _ provider.Context, _ provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	result := provider.NewAssistantMessageEventStream(64)
	round := p.calls.Add(1)
	go func() {
		if round <= p.finalRound {
			id := "call_" + itoa(int(round))
			// Visible message first, then the tool call — the exact UI pattern.
			result.Push(provider.AssistantMessageEvent{Type: provider.EventTextDelta, Delta: "Let me check the code. "})
			result.Push(provider.AssistantMessageEvent{
				Type:         provider.EventToolCallEnd,
				ContentIndex: 0,
				ToolCall: &provider.ContentBlock{
					Type:          provider.ContentBlockToolCall,
					ToolCallID:    id,
					ToolName:      "mock_tool",
					ToolArguments: `{"arg":"value"}`,
				},
			})
			result.End(&provider.AssistantMessage{
				Content: []provider.ContentBlock{
					{Type: provider.ContentBlockText, Text: "Let me check the code. "},
					{Type: provider.ContentBlockToolCall, ToolCallID: id, ToolName: "mock_tool", ToolArguments: `{"arg":"value"}`},
				},
				StopReason: provider.StopReasonToolCall,
			})
			return
		}
		result.Push(provider.AssistantMessageEvent{Type: provider.EventTextDelta, Delta: "done"})
		result.End(&provider.AssistantMessage{
			Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "done"}},
			StopReason: provider.StopReasonEndTurn,
		})
	}()
	return result, nil
}

func (p *contentToolProvider) StreamSimple(m provider.Model, c provider.Context, o provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(m, c, provider.BuildSimpleOptions(m, o))
}

// TestConsecutiveToolRounds_ContentMessageEachRound is the UI-evidence repro:
// a content message + tool call each round must never trigger the nudge.
func TestConsecutiveToolRounds_ContentMessageEachRound(t *testing.T) {
	p := &contentToolProvider{api: provider.Api("repro-content-tools"), finalRound: 20}
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
		MaxToolRepeatConsecutive: 0,
		MaxToolCalls:             100,
		MaxConsecutiveToolRounds: 15,
	})

	obs := &mockEventObserver{}
	agent.AddObserver(obs)
	go func() {
		for range agent.Output {
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := agent.Run(ctx, "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	n := 0
	for _, m := range copyAgentHistory(agent) {
		if m.Metadata[metaEphemeral] == "true" {
			n++
		}
	}
	if n != 0 {
		t.Fatalf("forced-answer nudge fired %d times, want 0 (each round had a content message)", n)
	}
}

// toolThenThinkProvider emits the tool call FIRST, then thinking, each round —
// the reverse order. This probes whether the streak check (which runs after the
// round completes) still sees the thinking regardless of intra-round ordering.
type toolThenThinkProvider struct {
	api        provider.Api
	calls      atomic.Int32
	finalRound int32
}

func (p *toolThenThinkProvider) API() provider.Api { return p.api }

func (p *toolThenThinkProvider) Stream(_ provider.Model, _ provider.Context, _ provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	result := provider.NewAssistantMessageEventStream(64)
	round := p.calls.Add(1)
	go func() {
		if round <= p.finalRound {
			id := "call_" + itoa(int(round))
			result.Push(provider.AssistantMessageEvent{
				Type:         provider.EventToolCallEnd,
				ContentIndex: 0,
				ToolCall: &provider.ContentBlock{
					Type:          provider.ContentBlockToolCall,
					ToolCallID:    id,
					ToolName:      "mock_tool",
					ToolArguments: `{"arg":"value"}`,
				},
			})
			// Thinking AFTER the tool call, same round.
			result.Push(provider.AssistantMessageEvent{Type: provider.EventThinkingDelta, Delta: "post-hoc reasoning "})
			result.End(&provider.AssistantMessage{
				Content:    []provider.ContentBlock{{Type: provider.ContentBlockToolCall, ToolCallID: id, ToolName: "mock_tool", ToolArguments: `{"arg":"value"}`}},
				StopReason: provider.StopReasonToolCall,
			})
			return
		}
		result.Push(provider.AssistantMessageEvent{Type: provider.EventTextDelta, Delta: "done"})
		result.End(&provider.AssistantMessage{
			Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "done"}},
			StopReason: provider.StopReasonEndTurn,
		})
	}()
	return result, nil
}

func (p *toolThenThinkProvider) StreamSimple(m provider.Model, c provider.Context, o provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(m, c, provider.BuildSimpleOptions(m, o))
}

// TestConsecutiveToolRounds_ToolThenThinking verifies ordering independence: a
// round with a tool call followed by thinking must still reset the streak.
func TestConsecutiveToolRounds_ToolThenThinking(t *testing.T) {
	p := &toolThenThinkProvider{api: provider.Api("repro-tool-then-think"), finalRound: 20}
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
		MaxToolRepeatConsecutive: 0,
		MaxToolCalls:             100,
		MaxConsecutiveToolRounds: 15,
	})

	obs := &mockEventObserver{}
	agent.AddObserver(obs)
	go func() {
		for range agent.Output {
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := agent.Run(ctx, "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	n := 0
	for _, m := range copyAgentHistory(agent) {
		if m.Metadata[metaEphemeral] == "true" {
			n++
		}
	}
	if n != 0 {
		t.Fatalf("forced-answer nudge fired %d times, want 0 (thinking present each round, order-independent)", n)
	}
}

// TestConsecutiveToolRounds_ThinkingEachRound_NeverNudges is the regression test
// for Issue 13: when every tool round is preceded by thinking tokens, the
// consecutive-tool-rounds streak must reset each round, so even 20 rounds (> the
// 15 default) must NOT trigger the forced-answer nudge.
func TestConsecutiveToolRounds_ThinkingEachRound_NeverNudges(t *testing.T) {
	p := &thinkingToolProvider{api: provider.Api("repro-thinking-tools"), finalRound: 20}
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
		MaxToolRepeatConsecutive: 0,
		MaxToolCalls:             100,
		MaxConsecutiveToolRounds: 15,
	})

	obs := &mockEventObserver{}
	agent.AddObserver(obs)
	go func() {
		for range agent.Output {
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := agent.Run(ctx, "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The nudge injects an ephemeral system message; count them in history.
	n := 0
	for _, m := range copyAgentHistory(agent) {
		if m.Metadata[metaEphemeral] == "true" {
			n++
		}
	}
	if n != 0 {
		t.Fatalf("forced-answer nudge fired %d times, want 0 (every round had thinking, so the streak must reset)", n)
	}
	if got := p.calls.Load(); got < 20 {
		t.Fatalf("provider streams = %d, want >= 20 (turn ended early — possible round cap)", got)
	}
}
