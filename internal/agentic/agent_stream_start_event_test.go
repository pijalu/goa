// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"bytes"
	"fmt"
	"log"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lateStartProvider mirrors what every real protocol does — push EventStart as
// the first event of the stream — but with a delay comparable to the stall
// window, so the event can only keep the turn alive if it re-arms the watchdog.
//
// Timeline (W = stall window):
//
//	t=0.0W  stream opens, watchdog armed for W
//	t=0.6W  EventStart  -> re-arms: deadline becomes 1.6W
//	t=1.3W  text delta -> only reachable when EventStart re-armed
//	t≈1.4W  stream ends
//
// Without the mapping the deadline stays 1.0W, the watchdog kills the stream
// while no answer has arrived yet, and the turn replays (bugs.md #2).
type lateStartProvider struct {
	api   provider.Api
	calls atomic.Int32
}

func (p *lateStartProvider) API() provider.Api { return p.api }

func (p *lateStartProvider) Stream(_ provider.Model, _ provider.Context, _ provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	p.calls.Add(1)
	const w = 400 * time.Millisecond // stall window; must match the agent's StreamOptions
	stream := provider.NewAssistantMessageEventStream(16)
	go func() {
		time.Sleep(6 * w / 10)
		stream.Push(provider.AssistantMessageEvent{Type: provider.EventStart, Partial: &provider.AssistantMessage{}})
		time.Sleep(7 * w / 10)
		stream.Push(provider.AssistantMessageEvent{Type: provider.EventTextDelta, Delta: "Answer after a late start."})
		stream.End(&provider.AssistantMessage{
			Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "Answer after a late start."}},
			StopReason: provider.StopReasonEndTurn,
		})
	}()
	return stream, nil
}

func (p *lateStartProvider) StreamSimple(m provider.Model, c provider.Context, o provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(m, c, provider.BuildSimpleOptions(m, o))
}

func registerLateStartProvider() *lateStartProvider {
	p := &lateStartProvider{api: provider.Api(fmt.Sprintf("test-late-start-%d", testProviderCounter.Add(1)))}
	provider.RegisterApiProvider(p)
	return p
}

// TestEventStart_ReArmsStallWatchdog is the bugs.md #2 regression test: the
// stream-open lifecycle event must count as progress. It is pushed by every
// protocol (openai completions/responses, anthropic, google, bedrock, mistral)
// and used to be classified unmapped, so it neither re-armed the stall/quiet
// guards nor warned silently at the user.
func TestEventStart_ReArmsStallWatchdog(t *testing.T) {
	p := registerLateStartProvider()

	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "You are helpful",
		Logger:       NewLogger(Error),
		StreamOptions: provider.StreamOptions{
			MaxRetries:  2,
			IdleTimeout: 400 * time.Millisecond,
		},
	})
	obs := &mockEventObserver{}
	agent.AddObserver(obs)

	runAgentToDone(t, agent, "Hi")

	assert.Equal(t, int32(1), p.calls.Load(),
		"EventStart must re-arm the stall watchdog: a delta arriving past the pre-start deadline must not trip a retry")
	assertEventObserved(t, obs.Events(), EventContent, Assistant, "Answer after a late start.")
}

// TestEventStart_NoUnmappedWarning pins the second half of the bug: the
// per-request WARN ("provider sent unmapped event type \"start\" — not rendered;
// it does not re-arm the stall watchdog") blamed the provider for a goa-internal
// lifecycle event, on every request of every turn.
func TestEventStart_NoUnmappedWarning(t *testing.T) {
	p := registerTestProvider("event-start-warn", []provider.AssistantMessageEvent{
		{Type: provider.EventStart, Partial: &provider.AssistantMessage{}},
		{Type: provider.EventTextDelta, Delta: "Plain answer."},
	})

	var buf bytes.Buffer
	logger := NewLoggerWithStdLogger(log.New(&buf, "", 0), Warn)

	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "You are helpful",
		Logger:       logger,
	})
	obs := &mockEventObserver{}
	agent.AddObserver(obs)

	runAgentToDone(t, agent, "Hi")

	out := buf.String()
	assert.NotContains(t, out, `unmapped event type "start"`,
		"the misleading per-request WARN must be gone:\n%s", out)
	assert.NotContains(t, out, "unmapped event type",
		"a normal EventStart + deltas + done sequence must log no unmapped-event warning:\n%s", out)
}

// TestEventStart_CountsAsMappedProgress unit-checks the classification the WARN
// and the watchdog both key on.
func TestEventStart_CountsAsMappedProgress(t *testing.T) {
	a := NewAgent(Config{SystemPrompt: "test", Logger: NewLogger(Error)})
	unmapped := make(map[provider.EventType]bool)

	assert.True(t, a.noteStreamEventProgress(
		provider.AssistantMessageEvent{Type: provider.EventStart}, unmapped),
		"EventStart is a mapped lifecycle event and must re-arm the guards")
	assert.False(t, unmapped[provider.EventStart], "no unmapped record for a mapped type")
}

// startOnlyProvider opens a stream, announces EventStart, and then ends it
// carrying nothing — a truncated response under provider load.
type startOnlyProvider struct {
	api   provider.Api
	calls atomic.Int32
}

func (p *startOnlyProvider) API() provider.Api { return p.api }

func (p *startOnlyProvider) Stream(_ provider.Model, _ provider.Context, _ provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	p.calls.Add(1)
	stream := provider.NewAssistantMessageEventStream(8)
	go func() {
		stream.Push(provider.AssistantMessageEvent{Type: provider.EventStart, Partial: &provider.AssistantMessage{}})
		stream.End(&provider.AssistantMessage{StopReason: provider.StopReasonEndTurn})
	}()
	return stream, nil
}

func (p *startOnlyProvider) StreamSimple(m provider.Model, c provider.Context, o provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(m, c, provider.BuildSimpleOptions(m, o))
}

// TestEventStart_AloneIsStillAnEmptyResponse guards the seam the mapping opened:
// EventStart is now a handled event, so the empty-response guard (genSawEvent)
// must NOT be satisfied by it. A stream that opens and then carries nothing is
// a truncated response and must keep taking the retry path, not end silently.
func TestEventStart_AloneIsStillAnEmptyResponse(t *testing.T) {
	p := &startOnlyProvider{api: provider.Api(fmt.Sprintf("test-start-only-%d", testProviderCounter.Add(1)))}
	provider.RegisterApiProvider(p)

	agent := NewAgent(Config{
		Model:         testModel(p.API()),
		SystemPrompt:  "You are helpful",
		Logger:        NewLogger(Error),
		StreamOptions: provider.StreamOptions{MaxRetries: 2},
	})
	obs := &mockEventObserver{}
	agent.AddObserver(obs)

	runAgentToDone(t, agent, "Hi")

	require.Greater(t, p.calls.Load(), int32(1),
		"EventStart carries no answer: the empty-response retry must still engage")
	var surfaced bool
	for _, e := range obs.Events() {
		if e.Type == EventContent && e.Role == System && strings.Contains(strings.ToLower(e.Text), "empty response") {
			surfaced = true
		}
	}
	assert.True(t, surfaced, "an always-empty stream must surface the empty-response notice, not stop silently")
}
