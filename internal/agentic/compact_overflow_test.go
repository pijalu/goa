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

// TestCompact_SummarizeOverflowShrinksInputUntilFits pins the bug-2 input-side
// guarantee: when the summarize request overflows AND micro truncation cannot
// free enough (a chat-heavy history with no elidable tool payload), the retry
// input must be cut down so the retried summarize fits the window. Before the
// fix the retry re-overflowed and Compact failed, surfacing as the "hard
// fallback" drop with the "summarize did not fit the window" label.
func TestCompact_SummarizeOverflowShrinksInputUntilFits(t *testing.T) {
	// Overflow once, then succeed. The provider records messages per attempt.
	p := registerOverflowProvider("shrink-retry", 1)
	a := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "sys",
		Logger:       NewLogger(Error),
	})
	// A plain-chat history (no tool payloads for micro to free) large enough
	// that the summarize request overflows the window.
	big := strings.Repeat("u", 4000)
	a.history = []Message{{Type: Content, Role: System, Content: "sys"}}
	for i := 0; i < 40; i++ {
		a.history = append(a.history,
			Message{Type: Content, Role: User, Content: big},
			Message{Type: Content, Role: Assistant, Content: big},
		)
	}
	// Tiny window: the full history (~100K tokens) vastly exceeds it.
	a.cfg.Model.ContextWindow = 4000

	if err := a.Compact(context.Background()); err != nil {
		t.Fatalf("Compact must succeed after shrinking the summarize input: %v", err)
	}
	p.mu.Lock()
	received := append([]int(nil), p.received...)
	p.mu.Unlock()
	if len(received) != 2 {
		t.Fatalf("summarize attempts = %d, want 2 (overflow + shrunk retry)", len(received))
	}
	if received[1] >= received[0] {
		t.Errorf("retry input was not shrunk: attempt sizes %v (want retry < first)", received)
	}
	// The compacted result is the [user, assistant] summary pair.
	if len(a.history) != 2 {
		t.Errorf("after Compact history = %d messages, want the summary pair", len(a.history))
	}
}

// TestCompact_OversizedSummaryIsCappedToFit pins the bug-2 output-side
// guarantee: a verbose model returning an oversized summary must not land a
// compacted history that still exceeds the ceiling (which would re-fire
// compression — or the destructive fallback — on the very next turn). The
// summary is capped to the window budget with a truncation note.
func TestCompact_OversizedSummaryIsCappedToFit(t *testing.T) {
	// Provider returns a summary far larger than the window budget allows.
	big := strings.Repeat("S", 200000) // ~60K tokens, window is 4000
	p := textEventProvider(big)
	a := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "sys",
		Logger:       NewLogger(Error),
	})
	a.cfg.Model.ContextWindow = 4000
	a.history = []Message{
		{Type: Content, Role: System, Content: "sys"},
		{Type: Content, Role: User, Content: strings.Repeat("u", 12000)},
		{Type: Content, Role: Assistant, Content: strings.Repeat("a", 12000)},
	}

	if err := a.Compact(context.Background()); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(a.history) != 2 {
		t.Fatalf("expected summary pair, got %d", len(a.history))
	}
	summary := a.history[1].Content
	if !strings.Contains(summary, "truncated to fit the context window") {
		t.Errorf("oversized summary must carry the truncation note; got len=%d", len(summary))
	}
	// The landed pair must fit under the hard ceiling of the window.
	a.mu.Lock()
	after := a.estimateContextTokensLocked()
	a.mu.Unlock()
	hardCeiling := 4000 * 95 / 100
	if after > hardCeiling {
		t.Errorf("compacted history %d tokens still exceeds hard ceiling %d — summary was not capped", after, hardCeiling)
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
