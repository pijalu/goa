// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/agentic/provider/hooks"
)

// overflowThenSuccessProvider fails the first N summarize streams with a
// context-overflow ProviderError, then succeeds with a fixed summary. It
// records the message count of every summarize request so tests can assert
// whether micro compaction shrank the input between attempts.
type overflowThenSuccessProvider struct {
	api provider.Api

	mu        sync.Mutex
	failures  int
	received  []int // messages per summarize attempt
	summaries int   // successful summarizes
}

func (p *overflowThenSuccessProvider) API() provider.Api { return p.api }

func (p *overflowThenSuccessProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	p.mu.Lock()
	p.received = append(p.received, len(ctx.Messages))
	shouldFail := p.failures > 0
	if shouldFail {
		p.failures--
	}
	p.mu.Unlock()

	if shouldFail {
		return nil, &hooks.ProviderError{
			Err:               fmt.Errorf("context_length_exceeded: too many tokens"),
			IsContextOverflow: true,
			IsRetryable:       true,
		}
	}

	p.mu.Lock()
	p.summaries++
	p.mu.Unlock()

	result := provider.NewAssistantMessageEventStream(64)
	go func() {
		result.Push(provider.AssistantMessageEvent{Type: provider.EventTextStart, ContentIndex: 0})
		result.Push(provider.AssistantMessageEvent{Type: provider.EventTextDelta, ContentIndex: 0, Delta: "Summary."})
		result.Push(provider.AssistantMessageEvent{Type: provider.EventTextEnd, ContentIndex: 0})
		result.End(&provider.AssistantMessage{
			Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "Summary."}},
			StopReason: provider.StopReasonEndTurn,
		})
	}()
	return result, nil
}

func (p *overflowThenSuccessProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(model, ctx, provider.BuildSimpleOptions(model, opts))
}

func registerOverflowProvider(name string, failures int) *overflowThenSuccessProvider {
	uniqueID := testProviderCounter.Add(1)
	p := &overflowThenSuccessProvider{
		api:      provider.Api(fmt.Sprintf("test-%s-%d", name, uniqueID)),
		failures: failures,
	}
	provider.RegisterApiProvider(p)
	return p
}

// microToolHistory builds a history with several large tool results (micro-
// compactable) interleaved with user/assistant turns, sized so its estimated
// tokens approach the given window. All tool results exceed MinContentTokens.
func microToolHistory(toolPayload int, pairs int) []Message {
	hist := []Message{{Type: Content, Role: User, Content: "start"}}
	for i := 0; i < pairs; i++ {
		hist = append(hist,
			Message{Type: Content, Role: Assistant, Content: "calling tool", ToolCalls: []ToolCallInfo{{ID: fmt.Sprintf("c%d", i), Name: "bash", Arguments: "{}"}}},
			Message{Type: Content, Role: ToolRole, ToolCallID: fmt.Sprintf("c%d", i), Content: strings.Repeat("r", toolPayload)},
			Message{Type: Content, Role: User, Content: "next"},
		)
	}
	return hist
}

// TestCompact_MicroDisabledByDefault_UsesSummarize verifies the contract's
// default: with micro compaction NOT opted in, Compact on a full window goes
// straight to summarize and never truncates tool results in place.
func TestCompact_MicroDisabledByDefault_UsesSummarize(t *testing.T) {
	p := registerOverflowProvider("micro-disabled", 0)
	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "sys",
		Logger:       NewLogger(Error),
		ContextCompression: ContextCompressionConfig{
			MaxTokens: 4000,
			Strategy:  CompressionSummarize,
			// MicroCompaction zero → Enabled=false (the default).
		},
	})
	hist := microToolHistory(2000, 6)
	origTool := hist[2].Content
	agent.mu.Lock()
	agent.history = hist
	agent.mu.Unlock()

	if err := agent.Compact(context.Background()); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	p.mu.Lock()
	summaries := p.summaries
	p.mu.Unlock()
	if summaries != 1 {
		t.Fatalf("expected exactly 1 summarize call, got %d", summaries)
	}
	// Result must be the compact pair produced by summarize (NOT an in-place
	// micro truncation, which would have left the tool messages in history).
	agent.mu.Lock()
	h := append([]Message(nil), agent.history...)
	agent.mu.Unlock()
	if len(h) != 2 || h[0].Role != User || h[1].Role != Assistant {
		t.Fatalf("expected summarize compact pair, got %+v", h)
	}
	for _, m := range h {
		if m.Content == origTool {
			t.Fatal("micro truncation ran despite micro being disabled (default)")
		}
	}
}

// TestCompact_MicroEnabled_DryRunDoesNotMutate verifies the dry-run contract:
// even with micro enabled and able to meet the shrink, the dry-run must NOT
// fire as a first pass — summarize still runs on the ORIGINAL history.
func TestCompact_MicroEnabled_DryRunDoesNotMutate(t *testing.T) {
	p := registerOverflowProvider("micro-dryrun", 0)
	micro := DefaultMicroCompactionConfig
	micro.Enabled = true
	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "sys",
		Logger:       NewLogger(Error),
		ContextCompression: ContextCompressionConfig{
			MaxTokens:       4000,
			Strategy:        CompressionSummarize,
			MicroCompaction: micro,
		},
	})
	hist := microToolHistory(2000, 6)
	agent.mu.Lock()
	agent.history = hist
	beforeLen := len(agent.history)
	beforeTool := agent.history[2].Content
	agent.mu.Unlock()

	if err := agent.Compact(context.Background()); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	p.mu.Lock()
	received := p.received
	summaries := p.summaries
	p.mu.Unlock()

	// Micro must not have fired first: exactly one summarize, on the FULL
	// original history (the dry-run validated but did not truncate).
	if summaries != 1 {
		t.Fatalf("expected 1 summarize, got %d", summaries)
	}
	if len(received) != 1 || received[0] != beforeLen { // history has no system message; summarize gets all of it
		t.Fatalf("summarize did not run on original history: received %v (history len %d)", received, beforeLen)
	}
	// The summarized result replaced history; micro never truncated in place.
	agent.mu.Lock()
	h := append([]Message(nil), agent.history...)
	agent.mu.Unlock()
	if len(h) != 2 {
		t.Fatalf("expected compact pair after summarize, got %d messages", len(h))
	}
	for _, m := range h {
		if m.Content == beforeTool {
			t.Fatal("micro first-pass truncation mutated history (must be summarize-first)")
		}
	}
}

// TestCompact_MicroEnabled_AppliedOnlyOnSummarizeOverflow is the core contract:
// micro is applied ONLY when summarize itself fails with a context-overflow
// error — to create enough room — then summarize retries on the shrunk input.
func TestCompact_MicroEnabled_AppliedOnlyOnSummarizeOverflow(t *testing.T) {
	// Fail the FIRST summarize with a context overflow, then succeed.
	p := registerOverflowProvider("micro-overflow", 1)
	micro := DefaultMicroCompactionConfig
	micro.Enabled = true
	micro.KeepRecentMessages = 2 // keep few so truncation has room to act
	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "sys",
		Logger:       NewLogger(Error),
		ContextCompression: ContextCompressionConfig{
			MaxTokens:       4000,
			Strategy:        CompressionSummarize,
			MicroCompaction: micro,
		},
	})
	obs := &mockEventObserver{}
	agent.AddObserver(obs)
	hist := microToolHistory(2000, 6)
	origLen := len(hist)
	agent.mu.Lock()
	agent.history = hist
	agent.mu.Unlock()

	if err := agent.Compact(context.Background()); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	p.mu.Lock()
	received := p.received
	summaries := p.summaries
	p.mu.Unlock()

	// Two summarize attempts: the first (overflowed) on the original history,
	// the retry after micro truncation on fewer tokens. received[0] is the
	// original non-system message count; the retry must see the SAME message
	// count (micro truncates in place, it does not drop) but the history in
	// between was micro-truncated.
	if summaries != 1 {
		t.Fatalf("expected 1 successful summarize, got %d", summaries)
	}
	if len(received) != 2 {
		t.Fatalf("expected 2 summarize attempts (overflow + retry), got %v", received)
	}
	if received[0] != origLen {
		t.Fatalf("first summarize should see original history (%d msgs), got %d", origLen, received[0])
	}

	// Micro compaction MUST have been applied for real between the attempts:
	// exactly one EventCompact with Strategy="micro" (the in-place truncation
	// that made room for the summarize retry), tagged as the overflow fallback.
	var microEvents []OutputEvent
	for _, e := range compactEvents(obs) {
		if e.Compaction != nil && e.Compaction.Strategy == "micro" {
			microEvents = append(microEvents, e)
		}
	}
	if len(microEvents) != 1 {
		t.Fatalf("expected exactly 1 micro EventCompact on summarize-overflow fallback, got %d (%v)", len(microEvents), compactEvents(obs))
	}
	if microEvents[0].Compaction.Detail == "" {
		t.Error("micro overflow-fallback event should carry a detail note")
	}

	agent.mu.Lock()
	h := append([]Message(nil), agent.history...)
	agent.mu.Unlock()
	if len(h) != 2 || h[1].Role != Assistant {
		t.Fatalf("expected summarize compact pair after overflow retry, got %+v", h)
	}
}

// TestCompact_MicroEnabled_OverflowWithoutMicro_Fails verifies that when micro
// is DISABLED, a summarize that overflows propagates the error (no micro
// rescue) — micro is strictly opt-in.
func TestCompact_MicroDisabled_OverflowPropagatesError(t *testing.T) {
	p := registerOverflowProvider("micro-disabled-overflow", 1)
	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "sys",
		Logger:       NewLogger(Error),
		ContextCompression: ContextCompressionConfig{
			MaxTokens: 4000,
			Strategy:  CompressionSummarize,
			// Micro disabled (default).
		},
	})
	agent.mu.Lock()
	agent.history = microToolHistory(2000, 6)
	agent.mu.Unlock()

	err := agent.Compact(context.Background())
	if err == nil {
		t.Fatal("expected summarize overflow error to propagate when micro is disabled")
	}
	if !isContextLengthError(err) {
		t.Fatalf("expected context-length error, got %v", err)
	}
}

// TestMicroCompactionDryRun_NoMutation pins the dry-run estimator: it must
// report what micro WOULD free without touching history.
func TestMicroCompactionDryRun_NoMutation(t *testing.T) {
	micro := DefaultMicroCompactionConfig
	micro.Enabled = true
	micro.KeepRecentMessages = 2
	agent := NewAgent(Config{
		Model:        testModel(provider.Api("dryrun-model")),
		SystemPrompt: "sys",
		Logger:       NewLogger(Error),
	})
	agent.mu.Lock()
	agent.history = microToolHistory(2000, 5)
	snapshot := append([]Message(nil), agent.history...)
	changed, freed := agent.microCompactionDryRun(micro, true)
	after := append([]Message(nil), agent.history...)
	agent.mu.Unlock()

	if changed == 0 {
		t.Fatal("dry-run should report truncatable tool results")
	}
	if freed <= 0 {
		t.Fatalf("dry-run should estimate freed tokens > 0, got %d", freed)
	}
	if len(after) != len(snapshot) {
		t.Fatalf("dry-run mutated history length: %d → %d", len(snapshot), len(after))
	}
	for i := range snapshot {
		if after[i].Content != snapshot[i].Content {
			t.Fatalf("dry-run mutated message %d content", i)
		}
	}
}
