// SPDX-License-Identifier: GPL-3.0-or-later

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
