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

// gateProbeProvider emits one tool call per stream for the first toolRounds
// streams, then a plain text reply, recording every provider context it
// receives so tests can assert what each round's request actually carried.
type gateProbeProvider struct {
	api        provider.Api
	toolRounds int

	mu   sync.Mutex
	ctxs []provider.Context
}

func (p *gateProbeProvider) API() provider.Api { return p.api }

func (p *gateProbeProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	p.mu.Lock()
	call := len(p.ctxs)
	p.ctxs = append(p.ctxs, ctx)
	p.mu.Unlock()

	result := provider.NewAssistantMessageEventStream(64)
	go func() {
		if call < p.toolRounds {
			result.Push(provider.AssistantMessageEvent{
				Type:         provider.EventToolCallEnd,
				ContentIndex: 0,
				ToolCall: &provider.ContentBlock{
					Type:          provider.ContentBlockToolCall,
					ToolCallID:    fmt.Sprintf("call_%d", call+1),
					ToolName:      "huge_tool",
					ToolArguments: `{"n":1}`,
				},
			})
		} else {
			delta := fmt.Sprintf("All %d tool results processed; the task is complete and verified.", call)
			result.Push(provider.AssistantMessageEvent{Type: provider.EventTextStart, ContentIndex: 0})
			result.Push(provider.AssistantMessageEvent{Type: provider.EventTextDelta, ContentIndex: 0, Delta: delta})
			result.Push(provider.AssistantMessageEvent{Type: provider.EventTextEnd, ContentIndex: 0})
		}
		result.End(&provider.AssistantMessage{
			Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: fmt.Sprintf("done %d", call)}},
			StopReason: provider.StopReasonEndTurn,
		})
	}()
	return result, nil
}

func (p *gateProbeProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(model, ctx, provider.BuildSimpleOptions(model, opts))
}

func (p *gateProbeProvider) recorded() []provider.Context {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]provider.Context(nil), p.ctxs...)
}

// contextText flattens a provider context's message contents for substring
// assertions. Tool results are ContentBlockToolResult blocks whose Text holds
// the result body, so every block's Text is included regardless of type.
func contextText(ctx provider.Context) string {
	var b strings.Builder
	for _, m := range ctx.Messages {
		for _, c := range m.Content {
			b.WriteString(c.Text)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// TestAgent_CompressionGateBetweenRounds guards the per-round compression
// gate (compression entry: a long tool-call turn climbed past 100%
// unchecked because compression ran only at turn start, ending in a provider
// 401). Tool results push usage past the trigger mid-turn; the stream AFTER
// the per-round gate must go out compressed — with no gate, the raw payload
// would be re-sent and this test fails.
func TestAgent_CompressionGateBetweenRounds(t *testing.T) {
	p := &gateProbeProvider{
		api:        provider.Api(fmt.Sprintf("gate-probe-%d", testProviderCounter.Add(1))),
		toolRounds: 8, // the budget truncator caps each result (~180 tok), so crossing the trigger needs several rounds — exactly the TC:436 incident shape
	}
	provider.RegisterApiProvider(p)

	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
		Tools: []Tool{hugeResultTool{
			name:   "huge_tool",
			schema: ToolSchema{Name: "huge_tool", Description: "test"},
			size:   2400,
		}},
		ContextCompression: ContextCompressionConfig{
			MaxTokens: 2000,
			Strategy:  CompressionToolElision,
			// Trigger at 50%: two 600-token results (~60%) already cross it,
			// so the round-2/3 gates must run the elision pass.
			Thresholds: CompressionThresholds{TriggerPercent: 50},
		},
	})

	obs := &mockEventObserver{}
	agent.AddObserver(obs)
	go func() {
		for range agent.Output {
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := agent.Run(ctx, "prompt"); err != nil {
		t.Fatalf("turn failed: %v", err)
	}

	ctxs := p.recorded()
	if len(ctxs) != 9 {
		t.Fatalf("expected 9 stream rounds, got %d", len(ctxs))
	}
	// Usage crosses the 50% trigger around round 5-6; the per-round gate must
	// elide the oldest tool results, so the final request carries the elision
	// evidence and FEWER payload chars than the peak pre-gate request. Elided
	// call/result pairs are now serialized as a plain-text assistant note
	// ("[elided]" placeholder imitation + summarize 400), so the
	// observable marker is the note, not the dropped "[tool result elided]".
	last := contextText(ctxs[len(ctxs)-1])
	if !strings.Contains(last, "[earlier call to huge_tool elided]") {
		t.Errorf("final request missing the elision note; " +
			"the per-round compression gate did not run before re-streaming")
	}
	if strings.Contains(last, "[elided]") {
		t.Errorf("final request still carries the raw [elided] placeholder; " +
			"elided tool calls must serialize as text notes")
	}
	xAt := func(i int) int {
		return strings.Count(contextText(ctxs[i]), "x")
	}
	// Without the gate the final request would carry all 8 results
	// (8 × 724 = 5792 chars); the gate holds the total near the preserve
	// window (~3620 here). Assert the cap held — the exact per-interval
	// delta alternates in steady state as usage hovers at the trigger.
	if end, fullGrowth := xAt(len(ctxs)-1), 8*724; end >= fullGrowth {
		t.Errorf("payload grew unchecked through the gate (final %d, un-gated %d)", end, fullGrowth)
	}
}
