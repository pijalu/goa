// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
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
// user-facing progress notice (how long quiet, when auto-retry) before the
// stall window elapses — previously the user watched a dead spinner with zero
// feedback until the 2-minute watchdog fired, which reads as "stuck". The lead
// is execution.activity_warn_after (30s of the shipped 45s window; two thirds of
// the window when unset — see effectiveStallWarnAfter).
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

// slowToolProvider emits a single tool call then ends the stream, so the
// agent enters tool execution. The paired slowTool sleeps longer than the
// quiet/stall window, so — if the stall watchdog and quiet warning were still
// armed during tool execution (the regression) — the user would see a
// spurious "provider quiet" notice and the watchdog would trip a retry.
type slowToolProvider struct {
	api   provider.Api
	calls atomic.Int32
}

func (p *slowToolProvider) API() provider.Api { return p.api }

func (p *slowToolProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	n := p.calls.Add(1)
	stream := provider.NewAssistantMessageEventStream(64)
	go func() {
		if n == 1 {
			stream.Push(provider.AssistantMessageEvent{Type: provider.EventToolCallEnd, ToolCall: &provider.ContentBlock{
				Type: provider.ContentBlockToolCall, ToolName: "slow", ToolArguments: `{}`, ToolCallID: "slow-1",
			}})
			stream.End(&provider.AssistantMessage{StopReason: provider.StopReasonEndTurn})
			return
		}
		// Round 2: deliver a substantive final answer so the turn ends cleanly
		// (a terse or empty reply after tool work is auto-continued as a
		// premature stop, which would add provider calls unrelated to the stall
		// guard under test).
		stream.Push(provider.AssistantMessageEvent{Type: provider.EventTextDelta, Delta: "The slow command finished successfully."})
		stream.End(&provider.AssistantMessage{
			Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "The slow command finished successfully."}},
			StopReason: provider.StopReasonEndTurn,
		})
	}()
	return stream, nil
}

func (p *slowToolProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(model, ctx, provider.BuildSimpleOptions(model, opts))
}

// slowTool blocks longer than the quiet/stall window to simulate a
// long-running command (e.g. `go test`).
type slowTool struct {
	name string
	d    time.Duration
}

func (m slowTool) Schema() ToolSchema {
	return ToolSchema{
		Name:        m.name,
		Description: "sleeps to simulate a long-running command",
		Schema:      map[string]interface{}{"type": "object"},
	}
}

func (m slowTool) Execute(input string) (string, error) {
	time.Sleep(m.d)
	return "slow done", nil
}

func (m slowTool) IsRetryable(err error) bool { return false }

// TestToolExecution_NoQuietWarningOrStallRetry is the regression test for the
// report: while a tool runs, the provider is legitimately idle (the stream
// already ended), so the quiet-provider notice must NOT fire and the stall
// watchdog must NOT trip a retry. Before the fix, both timers stayed armed
// through tool execution: a tool running longer than half the stall window
// produced "provider quiet … will auto-retry" mid-tool, and crossing the full
// stall window logged "Stream stalled" and CloseWithError'd the ended stream.
func TestToolExecution_NoQuietWarningOrStallRetry(t *testing.T) {
	p := &slowToolProvider{api: provider.Api(fmt.Sprintf("test-slow-tool-%d", testProviderCounter.Add(1)))}
	provider.RegisterApiProvider(p)

	const idle = 400 * time.Millisecond // stall window; the quiet warning fires at 2/3 of it
	agent := NewAgent(Config{
		Model: provider.Model{
			ID:         "slow-tool-test",
			Api:        p.API(),
			Provider:   provider.ProviderCustom,
			InputTypes: []string{"text"},
		},
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
		Tools:        []Tool{slowTool{name: "slow", d: 1200 * time.Millisecond}}, // > 2x stall window
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

	runQuietTestTurn(t, agent)

	assert.False(t, hasQuietWarning(obs),
		"a running tool must not be reported as provider silence")

	// The stall watchdog firing during tool execution would force a re-stream.
	// The turn must complete in exactly 2 provider calls (the tool-call round
	// plus the follow-up answer round); any extra call means a spurious retry
	// fired while the tool was running.
	assert.Equal(t, int32(2), p.calls.Load(),
		"no stall-retry may fire while a tool is running (tool-call round + follow-up round only)")
}
