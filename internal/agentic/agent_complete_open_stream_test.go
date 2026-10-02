// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// completeThenSilentProvider reproduces the live z.ai / opencode-go failure
// (docs/research/zai-connection-review-20260930.md §2): the provider streams a
// COMPLETE answer — text deltas for the whole reply — and then holds the
// connection open without ever emitting a terminal event (no EventDone, no
// stream.End, no [DONE]).
//
// This is NOT the silent-from-the-start hang the event-stall watchdog exists
// for (Issue 21, stream_stall_repro_test.go): the model already finished its
// turn and the answer is fully buffered. Today goa cannot tell the two apart,
// so it waits the full stall window, discards the completed answer via
// undoLastAssistantMessage, and replays the entire turn.
type completeThenSilentProvider struct {
	api provider.Api
	// calls counts Stream() invocations — 2+ proves the completed answer was
	// thrown away and the turn replayed.
	calls atomic.Int32
	// openStreams counts how many times the provider opened a stream that it
	// then abandoned without terminating (the held-open socket).
	openStreams atomic.Int32
}

func (p *completeThenSilentProvider) API() provider.Api { return p.api }

func (p *completeThenSilentProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	p.calls.Add(1)
	result := provider.NewAssistantMessageEventStream(64)
	p.openStreams.Add(1)
	go func() {
		// The whole answer, in order — exactly what the real providers sent
		// before going quiet (see export B: "Evidence is complete. Writing the
		// review document.").
		for _, word := range []string{"Evidence", " is", " complete", "."} {
			result.Push(provider.AssistantMessageEvent{
				Type:  provider.EventTextDelta,
				Delta: word,
			})
		}
		// No EventDone. No End(). The socket stays open — the provider is
		// simply never going to send another byte.
	}()
	return result, nil
}

func (p *completeThenSilentProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	base := provider.BuildSimpleOptions(model, opts)
	return p.Stream(model, ctx, base)
}

// TestAgent_CompleteAnswerHeldOpenStream_FinalizesWithoutReplay is the
// regression for the CRITICAL stall defect: a provider that delivered the full
// answer and then held the connection open must NOT cost the user the answer
// and a full turn replay.
//
// Expected after the fix:
//   - the turn completes successfully (err == nil),
//   - the received text survives in the transcript,
//   - the provider was called exactly once (no replay).
func TestAgent_CompleteAnswerHeldOpenStream_FinalizesWithoutReplay(t *testing.T) {
	p := &completeThenSilentProvider{api: provider.Api(fmt.Sprintf("test-complete-open-%d", testProviderCounter.Add(1)))}
	provider.RegisterApiProvider(p)

	agent := NewAgent(Config{
		Model: provider.Model{
			ID:         "complete-open-test",
			Api:        p.API(),
			Provider:   provider.ProviderCustom,
			InputTypes: []string{"text"},
		},
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
		StreamOptions: provider.StreamOptions{
			MaxRetries:  3,
			IdleTimeout: 400 * time.Millisecond, // shrink the stall watchdog for the test
		},
		// This provider reproduces z.ai / opencode-go, which never send a
		// terminator even on a complete answer. Without the capability the
		// watchdog must (correctly) treat the held-open socket as a stall, so
		// the flag is what makes "complete answer, no terminator" decidable.
		ProviderOmitsStreamTerminator: true,
	})

	var transcript []string
	agent.AddObserver(OutputObserverFunc(func(e OutputEvent) {
		if e.Type == EventContent && e.Role == Assistant {
			transcript = append(transcript, e.Text)
		}
	}))

	go func() {
		for range agent.Output {
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- agent.Run(ctx, "prompt") }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("turn should complete from the delivered answer, got error: %v (provider called %d times, %d held-open streams — a replay wastes the whole turn)",
				err, p.calls.Load(), p.openStreams.Load())
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("turn did not end within 15s; provider calls=%d", p.calls.Load())
	}

	if got := p.calls.Load(); got != 1 {
		t.Errorf("completed answer was discarded and the turn replayed: provider called %d times, want 1", got)
	}

	full := joinTexts(transcript)
	if full == "" {
		t.Fatal("delivered answer was lost: assistant transcript is empty")
	}
	for _, want := range []string{"Evidence", "complete"} {
		if !containsText(full, want) {
			t.Errorf("delivered answer lost %q; transcript=%q", want, full)
		}
	}
}

// TestAgent_ProductiveSilence_StillStalls guards the other side of the fix:
// silence BEFORE any answer is delivered is a genuine stall and must keep the
// existing watchdog + retry behaviour. If the "complete answer" relaxation
// were too broad, this test would hang or finalize an empty turn.
func TestAgent_ProductiveSilence_StillStalls(t *testing.T) {
	p := &silentStreamProvider{api: provider.Api(fmt.Sprintf("test-productive-silence-%d", testProviderCounter.Add(1)))}
	provider.RegisterApiProvider(p)

	agent := NewAgent(Config{
		Model: provider.Model{
			ID:         "productive-silence-test",
			Api:        p.API(),
			Provider:   provider.ProviderCustom,
			InputTypes: []string{"text"},
		},
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
		StreamOptions: provider.StreamOptions{
			MaxRetries:  1,
			IdleTimeout: 300 * time.Millisecond,
		},
	})

	go func() {
		for range agent.Output {
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- agent.Run(ctx, "prompt") }()

	select {
	case <-done:
		// Stall → retry → eventual terminal error is the expected outcome; the
		// point is only that the turn ENDS (never finalize-silently on an
		// empty turn).
	case <-time.After(10 * time.Second):
		t.Fatalf("genuinely silent stream must still stall and end, not hang forever")
	}
}

func joinTexts(in []string) string {
	out := ""
	for _, s := range in {
		out += s
	}
	return out
}

func containsText(haystack, needle string) bool {
	return len(needle) == 0 || indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

// TestRoundDeliveredCompleteAnswer_Boundaries pins the conservative contract
// of the helper that decides "finished answer vs genuine stall". A false
// positive here would silently finalize a truncated answer, so every negative
// case matters as much as the positive one.
func TestRoundDeliveredCompleteAnswer_Boundaries(t *testing.T) {
	for _, tc := range []struct {
		name string
		// terminator simulates the provider's own end-of-generation marker
		// (finish_reason / [DONE] / message_stop) having arrived.
		terminator bool
		// omitsTerminator marks a provider the catalog declares as never
		// sending one (z.ai, opencode-go).
		omitsTerminator bool
		content         string
		thinking        string
		tools           []provider.ContentBlock
		streaming       int
		want            bool
	}{
		// Tier 1: the protocol terminator is authoritative and
		// language-agnostic — a Chinese or Arabic answer counts identically,
		// because nothing about the text is consulted.
		{name: "terminator wins over any text shape", terminator: true, content: "证据已完整。", want: true},
		{name: "terminator with no text at all", terminator: true, want: true},
		{name: "terminator is blocked by a pending tool call", terminator: true, content: "Calling.", tools: []provider.ContentBlock{{Type: provider.ContentBlockToolCall, ToolName: "read"}}, want: false},
		{name: "terminator is blocked by a streaming tool call", terminator: true, content: "Calling.", streaming: 1, want: false},

		// Tier 2: no terminator, but the provider is declared to omit one, so
		// the answer is judged by structure (Latin punctuation only).
		{name: "no terminator, omits one: finished sentence", omitsTerminator: true, content: "Evidence is complete.", want: true},
		{name: "no terminator, omits one: finished question", omitsTerminator: true, content: "Should I edit the file?", want: true},
		{name: "no terminator, omits one: closed code fence", omitsTerminator: true, content: "Done:\n```go\nfmt.Println()\n```", want: true},
		{name: "no terminator, omits one: trailing whitespace trimmed", omitsTerminator: true, content: "All good.   ", want: true},
		{name: "no terminator, omits one: truncated mid-word", omitsTerminator: true, content: "Evidence is comp", want: false},
		{name: "no terminator, omits one: truncated mid-thought", omitsTerminator: true, content: "The next step is to", want: false},
		{name: "no terminator, omits one: unclosed bracket", omitsTerminator: true, content: "call foo(", want: false},
		{name: "no terminator, omits one: empty content", omitsTerminator: true, content: "", want: false},
		{name: "no terminator, omits one: whitespace only", omitsTerminator: true, content: "   \n ", want: false},
		{name: "no terminator, omits one: thinking is not an answer", omitsTerminator: true, thinking: "reasoning...", want: false},

		// A provider that DOES send a terminator must never be second-guessed
		// on text shape: a hung stream there is a stall, not a finished answer.
		// This is the case that keeps the heuristic from being load-bearing.
		{name: "no terminator, provider sends one: finished sentence is still a stall", content: "Evidence is complete.", want: false},
		{name: "no terminator, provider sends one: question is still a stall", content: "Should I edit the file?", want: false},
		{name: "no terminator, provider sends one: closed fence is still a stall", content: "Done:\n```go\nfmt.Println()\n```", want: false},

		{name: "pending buffered tool call", omitsTerminator: true, content: "Calling a tool.", tools: []provider.ContentBlock{{Type: provider.ContentBlockToolCall, ToolName: "read"}}, want: false},
		{name: "tool call still streaming", omitsTerminator: true, content: "Calling a tool.", streaming: 1, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &Agent{cfg: Config{ProviderOmitsStreamTerminator: tc.omitsTerminator}}
			a.roundSawProtocolTerminator = tc.terminator
			a.contentBuf.WriteString(tc.content)
			a.thinkingBuf.WriteString(tc.thinking)
			a.bufferedToolCalls = tc.tools
			for i := 0; i < tc.streaming; i++ {
				if a.streamingToolCalls == nil {
					a.streamingToolCalls = map[string]*partialToolCall{}
				}
				a.streamingToolCalls["c"] = &partialToolCall{}
			}

			if got := a.roundDeliveredCompleteAnswer(); got != tc.want {
				t.Errorf("roundDeliveredCompleteAnswer() = %v, want %v (terminator=%v omits=%v content=%q thinking=%q)",
					got, tc.want, tc.terminator, tc.omitsTerminator, tc.content, tc.thinking)
			}
		})
	}
}
