// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/agentic/provider/hooks"
)

// summarizeRetryAgent builds a minimal agent over a scripted provider with a
// fast, jitter-free retry policy so retry-count assertions stay exact and the
// test does not sleep through real backoff windows.
func summarizeRetryAgent(t *testing.T, p *scriptedStreamProvider, maxRetries int) *Agent {
	t.Helper()
	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
		StreamOptions: provider.StreamOptions{
			MaxRetries: maxRetries,
			RetryPolicy: &provider.RetryPolicy{
				Backoff: provider.RetryBackoff{
					InitialDelay: time.Millisecond,
					MaxDelay:     5 * time.Millisecond,
					Jitter:       0,
				},
			},
		},
	})
	agent.SetHistory([]Message{
		{Type: Content, Role: User, Content: "hello"},
		{Type: Content, Role: Assistant, Content: "hi there"},
	})
	return agent
}

// summarizeOverflowErr builds a classified context-overflow provider error
// (the shape the summarize-overflow recovery path in compactOrdered reacts to).
func summarizeOverflowErr() error {
	return (&hooks.ErrorContext{
		StatusCode:        http.StatusBadRequest,
		Body:              `{"error":{"message":"This model's maximum context length is 4096 tokens. However, you requested 5000 tokens","type":"invalid_request_error"}}`,
		IsContextOverflow: true,
		IsRetryable:       true,
	}).ToError()
}

// TestSummarizeHistoryRetriesRateLimitThenSucceeds is the bugs.md regression:
// a 429 on the summarize stream must send the compaction into the provider
// retry/backoff path, not fail the compression. The first attempt fails with
// a classified 429, the second succeeds.
func TestSummarizeHistoryRetriesRateLimitThenSucceeds(t *testing.T) {
	p := &scriptedStreamProvider{
		api: provider.Api(fmt.Sprintf("summarize-retry-%d", testProviderCounter.Add(1))),
		steps: []scriptedStreamStep{
			{err: rateLimitErrWithRetry(1)},
			{events: []provider.AssistantMessageEvent{
				{Type: provider.EventTextDelta, Delta: "condensed conversation summary"},
			}},
		},
	}
	provider.RegisterApiProvider(p)

	agent := summarizeRetryAgent(t, p, 3)
	summary, _, err := agent.summarizeHistory(context.Background())
	if err != nil {
		t.Fatalf("summarizeHistory must recover from a 429 via retries: %v", err)
	}
	if summary == "" {
		t.Error("summarizeHistory returned an empty summary after retry")
	}
	if got := p.Calls(); got != 2 {
		t.Errorf("expected 2 summarize attempts (1 failed + 1 success), got %d", got)
	}
}

// TestSummarizeHistoryRetriesMidStreamRateLimit pins the error-chain fix: a
// 429 arriving as a MID-STREAM error event (HTTP 200, then an error frame)
// must stay classifiable (ProviderError chain preserved through %w) so the
// retry loop can honor it.
func TestSummarizeHistoryRetriesMidStreamRateLimit(t *testing.T) {
	p := &scriptedStreamProvider{
		api: provider.Api(fmt.Sprintf("summarize-midretry-%d", testProviderCounter.Add(1))),
		steps: []scriptedStreamStep{
			{events: []provider.AssistantMessageEvent{
				{Type: provider.EventTextDelta, Delta: "partial"},
				{Type: provider.EventError, Error: rateLimitErrWithRetry(1)},
			}},
			{events: []provider.AssistantMessageEvent{
				{Type: provider.EventTextDelta, Delta: "condensed conversation summary"},
			}},
		},
	}
	provider.RegisterApiProvider(p)

	agent := summarizeRetryAgent(t, p, 3)
	summary, _, err := agent.summarizeHistory(context.Background())
	if err != nil {
		t.Fatalf("summarizeHistory must recover from a mid-stream 429 via retries: %v", err)
	}
	if summary != "condensed conversation summary" {
		t.Errorf("summary = %q, want the retried attempt's text", summary)
	}
	if got := p.Calls(); got != 2 {
		t.Errorf("expected 2 summarize attempts, got %d", got)
	}
}

// TestSummarizeHistoryRateLimitRetriesExhausted verifies the budget: with
// MaxRetries=2 and a persistent 429, summarizeHistory makes exactly
// 1 + 2 attempts and then surfaces the error (the compaction transaction
// closes with it).
func TestSummarizeHistoryRateLimitRetriesExhausted(t *testing.T) {
	p := &scriptedStreamProvider{
		api: provider.Api(fmt.Sprintf("summarize-exhaust-%d", testProviderCounter.Add(1))),
		steps: []scriptedStreamStep{
			{err: rateLimitErrWithRetry(1)}, // repeat-last keeps 429ing
		},
	}
	provider.RegisterApiProvider(p)

	agent := summarizeRetryAgent(t, p, 2)
	_, _, err := agent.summarizeHistory(context.Background())
	if err == nil {
		t.Fatal("expected summarizeHistory to fail after exhausting the retry budget")
	}
	if got := p.Calls(); got != 3 {
		t.Errorf("expected 3 summarize attempts (1 initial + 2 retries), got %d", got)
	}
}

// TestSummarizeHistoryDoesNotRetryContextOverflow pins the boundary with the
// existing recovery design: a context-overflow summarize failure is NOT
// retried inside summarizeHistory — compactOrdered owns the once-only
// shrink-and-retry recovery (micro + drop-oldest), so the error must come
// back to it after exactly one attempt.
func TestSummarizeHistoryDoesNotRetryContextOverflow(t *testing.T) {
	p := &scriptedStreamProvider{
		api: provider.Api(fmt.Sprintf("summarize-overflow-%d", testProviderCounter.Add(1))),
		steps: []scriptedStreamStep{
			{err: summarizeOverflowErr()},
			{events: []provider.AssistantMessageEvent{
				{Type: provider.EventTextDelta, Delta: "should never be reached"},
			}},
		},
	}
	provider.RegisterApiProvider(p)

	agent := summarizeRetryAgent(t, p, 3)
	_, _, err := agent.summarizeHistory(context.Background())
	if err == nil {
		t.Fatal("expected the context-overflow error to surface to compactOrdered")
	}
	if !isContextLengthError(err) {
		t.Errorf("error must stay context-length classified, got: %v", err)
	}
	if got := p.Calls(); got != 1 {
		t.Errorf("overflow must not retry inside summarizeHistory; got %d calls", got)
	}
}
