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

// rateLimitErrWithRetry builds a classified 429 provider error carrying a
// Retry-After hint, mirroring the fixture used by the retry-policy tests.
func rateLimitErrWithRetry(retryAfterMS int) error {
	return (&hooks.ErrorContext{
		StatusCode:   http.StatusTooManyRequests,
		Body:         `{"error":{"message":"slow down","type":"rate_limit"}}`,
		IsRateLimit:  true,
		IsRetryable:  true,
		RetryAfterMs: retryAfterMS,
	}).ToError()
}

// runAgentCapture drives one prompt to completion and returns every observed
// output event together with Run's terminal error.
func runAgentCapture(t *testing.T, agent *Agent, prompt string) ([]OutputEvent, error) {
	t.Helper()
	obs := &mockEventObserver{}
	agent.AddObserver(obs)
	go func() {
		for range agent.Output {
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := agent.Run(ctx, prompt)
	return obs.Events(), err
}

// rateLimitEvents filters the observed events down to EventRateLimit.
func rateLimitEvents(events []OutputEvent) []OutputEvent {
	var out []OutputEvent
	for _, e := range events {
		if e.Type == EventRateLimit {
			out = append(out, e)
		}
	}
	return out
}

// newScriptedRateLimitAgent builds an agent over a scripted stream provider
// with a fast, jitter-free retry policy so backoff assertions stay exact.
func newScriptedRateLimitAgent(t *testing.T, steps []scriptedStreamStep, maxRetries int) *scriptedStreamProvider {
	t.Helper()
	p := &scriptedStreamProvider{
		api:   provider.Api(fmt.Sprintf("test-ratelimit-%d", testProviderCounter.Add(1))),
		steps: steps,
	}
	provider.RegisterApiProvider(p)
	return p
}

// TestAgent_RateLimitEventNotEmittedOnCleanTurn pins the §6 invariant that
// EventRateLimit is a failure-path-only event: a successful turn must not
// carry a single one.
func TestAgent_RateLimitEventNotEmittedOnCleanTurn(t *testing.T) {
	p := newScriptedRateLimitAgent(t, []scriptedStreamStep{{
		events: []provider.AssistantMessageEvent{
			{Type: provider.EventTextDelta, Delta: "Hello there."},
		},
	}}, 0)

	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
	})

	events, err := runAgentCapture(t, agent, "Hi")
	if err != nil {
		t.Fatalf("Run on clean turn: %v", err)
	}
	if got := rateLimitEvents(events); len(got) != 0 {
		t.Errorf("expected zero EventRateLimit on a clean turn, got %d (%+v)", len(got), got)
	}
}

// TestAgent_RateLimitEventsOnRetryEpisode verifies one scheduled-retry event
// per retry attempt (will_retry=true, 0-based attempt, server Retry-After in
// ms) followed by exactly one terminal event (will_retry=false) when the
// finite budget is exhausted.
func TestAgent_RateLimitEventsOnRetryEpisode(t *testing.T) {
	p := newScriptedRateLimitAgent(t, []scriptedStreamStep{
		{err: rateLimitErrWithRetry(1)},
		{err: rateLimitErrWithRetry(1)},
		{err: rateLimitErrWithRetry(1)},
	}, 0)

	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
		StreamOptions: provider.StreamOptions{
			MaxRetries: 2,
			RetryPolicy: &provider.RetryPolicy{
				Backoff: provider.RetryBackoff{
					InitialDelay: time.Millisecond,
					MaxDelay:     5 * time.Millisecond,
					Jitter:       0,
				},
			},
		},
	})

	events, err := runAgentCapture(t, agent, "Hi")
	if err == nil {
		t.Fatal("expected Run to fail after exhausting the retry budget")
	}
	if p.Calls() != 3 {
		t.Errorf("expected 3 provider calls (1 initial + 2 retries), got %d", p.Calls())
	}

	rl := rateLimitEvents(events)
	if len(rl) != 3 {
		t.Fatalf("expected 2 scheduled + 1 terminal rate_limit event, got %d", len(rl))
	}
	for i, wantAttempt := range []int{0, 1} {
		e := rl[i]
		if !e.RateLimit.WillRetry {
			t.Errorf("event %d: expected will_retry=true", i)
		}
		if e.RateLimit.Attempt != wantAttempt {
			t.Errorf("event %d: expected attempt=%d, got %d", i, wantAttempt, e.RateLimit.Attempt)
		}
		if e.RateLimit.RetryAfterMS != 1 {
			t.Errorf("event %d: expected server Retry-After (1ms) in retry_after_ms, got %d", i, e.RateLimit.RetryAfterMS)
		}
		if e.RateLimit.Model != "test-model" || e.RateLimit.Provider != string(provider.ProviderCustom) {
			t.Errorf("event %d: unexpected model/provider identity %+v", i, e.RateLimit)
		}
	}
	terminal := rl[2]
	if terminal.RateLimit.WillRetry {
		t.Error("expected final event will_retry=false when giving up")
	}
	if terminal.RateLimit.Classified != "rate_limit" {
		t.Errorf("expected classified=rate_limit on terminal event, got %q", terminal.RateLimit.Classified)
	}
}

// TestAgent_RateLimitTerminalOnlyWhenNonRetryable verifies a non-retryable
// failure produces exactly one terminal event — no scheduled-retry event may
// precede it.
func TestAgent_RateLimitTerminalOnlyWhenNonRetryable(t *testing.T) {
	authErr := (&hooks.ErrorContext{
		StatusCode: http.StatusUnauthorized,
		Body:       `{"error":{"message":"invalid api key","type":"invalid_request"}}`,
	}).ToError()
	p := newScriptedRateLimitAgent(t, []scriptedStreamStep{{err: authErr}}, 0)

	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
	})

	events, err := runAgentCapture(t, agent, "Hi")
	if err == nil {
		t.Fatal("expected Run to fail for a non-retryable error")
	}
	if p.Calls() != 1 {
		t.Errorf("expected no retry attempts (1 call), got %d", p.Calls())
	}

	rl := rateLimitEvents(events)
	if len(rl) != 1 {
		t.Fatalf("expected exactly 1 terminal rate_limit event, got %d", len(rl))
	}
	if rl[0].RateLimit.WillRetry {
		t.Error("expected will_retry=false on the terminal event")
	}
	if rl[0].RateLimit.Attempt != 0 {
		t.Errorf("expected attempt=0 on the terminal event, got %d", rl[0].RateLimit.Attempt)
	}
	if rl[0].RateLimit.Classified == "" || rl[0].RateLimit.Classified == "rate_limit" {
		t.Errorf("expected a non-rate-limit classification, got %q", rl[0].RateLimit.Classified)
	}
}

// TestAgent_RateLimitNoTerminalAfterRecovery verifies an episode that recovers
// on its first retry emits only the scheduled-retry event and no terminal
// give-up event.
func TestAgent_RateLimitNoTerminalAfterRecovery(t *testing.T) {
	p := newScriptedRateLimitAgent(t, []scriptedStreamStep{
		{err: rateLimitErrWithRetry(1)},
		{events: []provider.AssistantMessageEvent{
			{Type: provider.EventTextDelta, Delta: "Recovered."},
		}},
	}, 0)

	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
		StreamOptions: provider.StreamOptions{
			MaxRetries: 3,
			RetryPolicy: &provider.RetryPolicy{
				Backoff: provider.RetryBackoff{
					InitialDelay: time.Millisecond,
					MaxDelay:     5 * time.Millisecond,
					Jitter:       0,
				},
			},
		},
	})

	events, err := runAgentCapture(t, agent, "Hi")
	if err != nil {
		t.Fatalf("expected recovery on first retry: %v", err)
	}
	if p.Calls() != 2 {
		t.Errorf("expected 2 provider calls, got %d", p.Calls())
	}

	rl := rateLimitEvents(events)
	if len(rl) != 1 {
		t.Fatalf("expected exactly 1 scheduled-retry event after recovery, got %d", len(rl))
	}
	if !rl[0].RateLimit.WillRetry || rl[0].RateLimit.Attempt != 0 || rl[0].RateLimit.RetryAfterMS != 1 {
		t.Errorf("unexpected scheduled-retry payload: %+v", rl[0].RateLimit)
	}
}
