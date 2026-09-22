// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pacedStreamProvider emits a text delta every interval, then ends cleanly —
// a healthy chatty provider whose gaps stay under the quiet threshold.
type pacedStreamProvider struct {
	api      provider.Api
	interval time.Duration
	steps    int
}

func (p *pacedStreamProvider) API() provider.Api { return p.api }

func (p *pacedStreamProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	stream := provider.NewAssistantMessageEventStream(64)
	go func() {
		for i := 0; i < p.steps; i++ {
			time.Sleep(p.interval)
			stream.Push(provider.AssistantMessageEvent{Type: provider.EventTextDelta, Delta: "x"})
		}
		stream.End(&provider.AssistantMessage{
			Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "done"}},
			StopReason: provider.StopReasonEndTurn,
		})
	}()
	return stream, nil
}

func (p *pacedStreamProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(model, ctx, provider.BuildSimpleOptions(model, opts))
}

// quietTestAgent builds an agent bound to the given api provider with a small
// idle window so the quiet/stall timers fire quickly.
func quietTestAgent(t *testing.T, api provider.Api, idle time.Duration) (*Agent, *mockEventObserver) {
	t.Helper()
	agent := NewAgent(Config{
		Model: provider.Model{
			ID:         "quiet-test",
			Api:        api,
			Provider:   provider.ProviderCustom,
			InputTypes: []string{"text"},
		},
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
		StreamOptions: provider.StreamOptions{
			MaxRetries:  1,
			IdleTimeout: idle,
		},
	})
	obs := &mockEventObserver{}
	agent.AddObserver(obs)
	go func() {
		for range agent.Output {
		}
	}()
	return agent, obs
}

func runQuietTestTurn(t *testing.T, agent *Agent) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- agent.Run(ctx, "prompt") }()
	select {
	case err := <-done:
		_ = err // a stall error is acceptable; a hang is not
	case <-time.After(5 * time.Second):
		t.Fatalf("HANG: turn did not end within 5s (idleTimeout small, watchdog must act)")
	}
}

func hasQuietWarning(obs *mockEventObserver) bool {
	for _, e := range obs.Events() {
		if e.Type == EventProgress && strings.Contains(e.Text, "provider quiet") {
			return true
		}
	}
	return false
}

// TestSilentStream_EmitsQuietProviderWarning is the F5 regression test: a
// provider that goes silent after opening the stream must surface a
// user-facing progress notice (how long quiet, when auto-retry) at half the
// stall window — previously the user watched a dead spinner with zero
// feedback until the 2-minute watchdog fired, which reads as "stuck".
func TestSilentStream_EmitsQuietProviderWarning(t *testing.T) {
	p := &silentStreamProvider{api: provider.Api(fmt.Sprintf("test-quiet-%d", testProviderCounter.Add(1)))}
	provider.RegisterApiProvider(p)

	agent, obs := quietTestAgent(t, p.API(), 400*time.Millisecond)
	runQuietTestTurn(t, agent)

	require.Eventually(t, func() bool { return hasQuietWarning(obs) },
		3*time.Second, 25*time.Millisecond,
		"expected a 'provider quiet' progress warning before the stall watchdog acted")
}

// TestPacedStream_NoQuietProviderWarning guards against false positives: a
// healthy stream whose event gaps stay below the quiet threshold must NOT
// emit the warning.
func TestPacedStream_NoQuietProviderWarning(t *testing.T) {
	p := &pacedStreamProvider{
		api:      provider.Api(fmt.Sprintf("test-paced-%d", testProviderCounter.Add(1))),
		interval: 50 * time.Millisecond,
		steps:    6, // 300ms total, gaps of 50ms < 200ms quiet threshold
	}
	provider.RegisterApiProvider(p)

	agent, obs := quietTestAgent(t, p.API(), 400*time.Millisecond)
	runQuietTestTurn(t, agent)

	assert.False(t, hasQuietWarning(obs),
		"a stream with activity every 50ms must not be reported as quiet")
}
