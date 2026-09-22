// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// unmappedFloodProvider streams only unmapped event types forever (a pacing /
// keep-alive stream the user can never see). Push returns false once the
// stream is terminated, so the flood goroutine exits on stall-close.
type unmappedFloodProvider struct {
	api   provider.Api
	calls atomic.Int32
}

func (p *unmappedFloodProvider) API() provider.Api { return p.api }

func (p *unmappedFloodProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	p.calls.Add(1)
	stream := provider.NewAssistantMessageEventStream(8)
	go func() {
		for stream.Push(provider.AssistantMessageEvent{Type: provider.EventType("provider_ping")}) {
			time.Sleep(20 * time.Millisecond)
		}
	}()
	return stream, nil
}

func (p *unmappedFloodProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(model, ctx, provider.BuildSimpleOptions(model, opts))
}

// TestUnmappedEventFlood_TripsStallWatchdog is the F1b regression test: the
// event-stall watchdog used to Reset on EVERY event while unmapped types were
// silent no-ops — a provider streaming only keep-alive/pacing events kept the
// watchdog (and byte-idle) alive forever: healthy logs, frozen UI, no escape.
// Unmapped events must NOT re-arm the guard, so the stall fires, the retry
// path engages, and the turn ends.
func TestUnmappedEventFlood_TripsStallWatchdog(t *testing.T) {
	p := &unmappedFloodProvider{api: provider.Api(fmt.Sprintf("test-flood-%d", testProviderCounter.Add(1)))}
	provider.RegisterApiProvider(p)

	agent, obs := quietTestAgent(t, p.API(), 300*time.Millisecond)
	runQuietTestTurn(t, agent)

	assert.GreaterOrEqual(t, p.calls.Load(), int32(2),
		"stall must fire on an unmapped-only stream and engage the retry path")
	require.Eventually(t, func() bool { return hasQuietWarning(obs) },
		3*time.Second, 25*time.Millisecond,
		"unmapped floods are invisible to the user: the quiet warning must surface")
}

// TestNoteStreamEventProgress unit-checks the classification: mapped handler
// types count as progress; unmapped types do not.
func TestNoteStreamEventProgress(t *testing.T) {
	a := NewAgent(Config{SystemPrompt: "test", Logger: NewLogger(Error)})
	unmapped := make(map[provider.EventType]bool)

	assert.True(t, a.noteStreamEventProgress(
		provider.AssistantMessageEvent{Type: provider.EventTextDelta}, unmapped))
	assert.True(t, a.noteStreamEventProgress(
		provider.AssistantMessageEvent{Type: provider.EventToolCallEnd}, unmapped))
	assert.True(t, a.noteStreamEventProgress(
		provider.AssistantMessageEvent{Type: provider.EventDone}, unmapped))

	assert.False(t, a.noteStreamEventProgress(
		provider.AssistantMessageEvent{Type: provider.EventType("provider_ping")}, unmapped))
	assert.False(t, a.noteStreamEventProgress(
		provider.AssistantMessageEvent{Type: provider.EventType("provider_ping")}, unmapped),
		"second sighting stays unmapped (and must not duplicate the log)")
	assert.True(t, unmapped[provider.EventType("provider_ping")], "type recorded once per stream")
}
