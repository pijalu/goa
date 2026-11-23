// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"context"
	"fmt"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

func summarizeProgressEvents(events []OutputEvent) []OutputEvent {
	var progress []OutputEvent
	for _, ev := range events {
		if ev.Type == EventCompactionProgress {
			progress = append(progress, ev)
		}
	}
	return progress
}

func TestSummarizeHistory_EmitsLiveTokenProgress(t *testing.T) {
	p := &scriptedStreamProvider{
		api: provider.Api(fmt.Sprintf("summarize-progress-%d", testProviderCounter.Add(1))),
		steps: []scriptedStreamStep{{
			events: []provider.AssistantMessageEvent{
				{Type: provider.EventTextDelta, Delta: "condensed "},
				{Type: provider.EventTextDelta, Delta: "conversation summary"},
			},
		}},
	}
	provider.RegisterApiProvider(p)
	agent := summarizeRetryAgent(t, p, 0)
	obs := &mockEventObserver{}
	agent.AddObserver(obs)

	summary, _, err := agent.summarizeHistory(context.Background())
	if err != nil {
		t.Fatalf("summarizeHistory: %v", err)
	}

	progress := summarizeProgressEvents(obs.Events())
	if len(progress) == 0 {
		t.Fatal("summarize emitted no live progress events")
	}
	last := progress[len(progress)-1]
	if last.Timings == nil {
		t.Fatal("summarize progress has no token timings")
	}
	if got, want := last.Timings.PredictedN, estimateTokens(summary); got != want {
		t.Errorf("generated tokens = %d, want estimate %d for completed summary", got, want)
	}
	if last.Timings.PredictedMs < 0 {
		t.Errorf("elapsed ms = %f, want non-negative", last.Timings.PredictedMs)
	}
	for _, ev := range obs.Events() {
		if ev.Type == EventContent {
			t.Errorf("summarize checkpoint leaked into ordinary content: %#v", ev)
		}
		if ev.Type == EventTokenStats {
			t.Errorf("live summarize progress must not alter turn token stats: %#v", ev)
		}
	}
}

func TestSummarizeHistory_RetryProgressResetsFailedAttempt(t *testing.T) {
	p := &scriptedStreamProvider{
		api: provider.Api(fmt.Sprintf("summarize-progress-retry-%d", testProviderCounter.Add(1))),
		steps: []scriptedStreamStep{
			{events: []provider.AssistantMessageEvent{
				{Type: provider.EventTextDelta, Delta: "a much longer partial answer from the failed attempt"},
				{Type: provider.EventError, Error: rateLimitErrWithRetry(1)},
			}},
			{events: []provider.AssistantMessageEvent{
				{Type: provider.EventTextDelta, Delta: "fresh"},
			}},
		},
	}
	provider.RegisterApiProvider(p)
	agent := summarizeRetryAgent(t, p, 1)
	obs := &mockEventObserver{}
	agent.AddObserver(obs)

	summary, _, err := agent.summarizeHistory(context.Background())
	if err != nil {
		t.Fatalf("summarizeHistory: %v", err)
	}
	progress := summarizeProgressEvents(obs.Events())
	if len(progress) < 2 {
		t.Fatalf("progress events = %d, want failed and retried attempts", len(progress))
	}
	last := progress[len(progress)-1]
	if last.Timings == nil {
		t.Fatal("last summarize progress has no token timings")
	}
	if got, want := last.Timings.PredictedN, estimateTokens(summary); got != want {
		t.Errorf("retry generated tokens = %d, want fresh-attempt estimate %d (failed attempt must not accumulate)", got, want)
	}
}
