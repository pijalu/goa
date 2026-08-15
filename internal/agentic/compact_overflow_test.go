// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// summaryCapturingProvider records the number of messages it received in the
// summarization request and returns a fixed summary.
type summaryCapturingProvider struct {
	api      provider.Api
	mu       sync.Mutex
	received int
	called   bool
}

func (p *summaryCapturingProvider) API() provider.Api { return p.api }

func (p *summaryCapturingProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	p.mu.Lock()
	p.called = true
	p.received = len(ctx.Messages)
	p.mu.Unlock()

	result := provider.NewAssistantMessageEventStream(64)
	go func() {
		result.Push(provider.AssistantMessageEvent{Type: provider.EventTextStart, ContentIndex: 0})
		result.Push(provider.AssistantMessageEvent{Type: provider.EventTextDelta, ContentIndex: 0, Delta: "Summary of conversation."})
		result.Push(provider.AssistantMessageEvent{Type: provider.EventTextEnd, ContentIndex: 0})
		result.End(&provider.AssistantMessage{
			Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "Summary of conversation."}},
			StopReason: provider.StopReasonEndTurn,
		})
	}()
	return result, nil
}

func (p *summaryCapturingProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(model, ctx, provider.BuildSimpleOptions(model, opts))
}

// TestCompact_SummarizesOriginalHistory verifies the current contract: Compact
// no longer pre-shrinks (selective) before summarizing — summarize runs on the
// ORIGINAL history so the provider prefix cache is preserved. Self-overflow is
// now handled by the micro-on-overflow fallback (see compact_micro_optional_test.go),
// not by an unconditional pre-flight shrink.
func TestCompact_SummarizesOriginalHistory(t *testing.T) {
	p := &summaryCapturingProvider{api: provider.Api("test-compact-orig-1")}
	provider.RegisterApiProvider(p)

	const turns = 30
	hist := make([]Message, 0, turns*2+1)
	hist = append(hist, Message{Type: Content, Role: User, Content: strings.Repeat("x", 200)})
	for i := 0; i < turns; i++ {
		hist = append(hist, Message{Type: Content, Role: Assistant, Content: strings.Repeat("y", 200)})
		hist = append(hist, Message{Type: Content, Role: User, Content: strings.Repeat("z", 200)})
	}

	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "You are helpful",
		Logger:       NewLogger(Error),
		ContextCompression: ContextCompressionConfig{
			MaxTokens:           800,
			Strategy:            CompressionSummarize,
			PreserveRecentTurns: 2,
		},
	})
	agent.mu.Lock()
	agent.history = hist
	origLen := len(agent.history)
	agent.mu.Unlock()

	if err := agent.Compact(context.Background()); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	p.mu.Lock()
	received := p.received
	called := p.called
	p.mu.Unlock()

	if !called {
		t.Fatal("summarize was not called")
	}
	// No pre-shrink: the summarizer receives the FULL original (non-system)
	// history — there is no system message stored in history here. The +1 is
	// the summarize instruction appended as the final user message after the
	// replayed prefix (cache-warm summarization, CA1).
	if received != origLen+1 {
		t.Fatalf("summarize should run on original history + instruction (%d msgs), got %d", origLen+1, received)
	}

	// And the result is the valid [user, assistant] compact pair.
	agent.mu.Lock()
	h := append([]Message(nil), agent.history...)
	agent.mu.Unlock()
	if len(h) != 2 || h[0].Role != User || h[1].Role != Assistant {
		t.Fatalf("expected [user, assistant] compact pair, got %+v", h)
	}
}

// TestCompact_NoSystemDuplication verifies the compacted history does not store
// the system prompt (which is sent via Context.SystemPrompt), so it is not
// double-sent on the next turn.
func TestCompact_NoSystemDuplication(t *testing.T) {
	p := textEventProvider("Summary.")
	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "You are helpful",
		Logger:       NewLogger(Error),
	})
	agent.mu.Lock()
	agent.history = []Message{
		{Type: Content, Role: User, Content: "hello"},
		{Type: Content, Role: Assistant, Content: "hi"},
	}
	agent.mu.Unlock()

	if err := agent.Compact(context.Background()); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	agent.mu.Lock()
	defer agent.mu.Unlock()
	for i, m := range agent.history {
		if m.Role == System {
			t.Fatalf("compacted history must not contain a system message (duplicates Context.SystemPrompt), found System at index %d: %q", i, m.Content)
		}
	}
}
