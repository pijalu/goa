// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// TestFinishProcessing_CancelsTurnCtx is the deterministic B2 regression test.
// The per-turn child ctx created by runInternal must be cancelled on every exit
// path. finishProcessing is the cleanup; it must invoke a.cancel (not just
// discard it) and reset processing. go vet -lostcancel cannot see the leak
// because cancel is stored in a struct field.
func TestFinishProcessing_CancelsTurnCtx(t *testing.T) {
	var calls int32
	agent := NewAgent(Config{
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
	})

	agent.mu.Lock()
	agent.processing = true
	agent.cancel = func() { atomic.AddInt32(&calls, 1) }
	agent.mu.Unlock()

	agent.finishProcessing()

	agent.mu.Lock()
	processing := agent.processing
	cancelSet := agent.cancel
	agent.mu.Unlock()

	if processing {
		t.Errorf("processing still true after finishProcessing")
	}
	if cancelSet != nil {
		t.Errorf("a.cancel not cleared after finishProcessing (B2)")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("expected a.cancel to be called exactly once, got %d (B2 leak)", got)
	}
}

// TestFinishProcessing_IdempotentAndNilSafe ensures repeated calls and a nil
// cancel func are handled without panic.
func TestFinishProcessing_IdempotentAndNilSafe(t *testing.T) {
	var calls int32
	agent := NewAgent(Config{
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
	})

	// nil cancel — no panic
	agent.mu.Lock()
	agent.processing = true
	agent.mu.Unlock()
	agent.finishProcessing()

	// set a cancel and call twice
	agent.mu.Lock()
	agent.cancel = func() { atomic.AddInt32(&calls, 1) }
	agent.mu.Unlock()
	agent.finishProcessing()
	agent.finishProcessing()

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("expected cancel called once across duplicate finishProcessing, got %d", got)
	}
}

// TestRunInternal_ProcessingResetOnError verifies a NEW bug found while fixing
// B2: the error path (processTurn returns err) previously left a.processing
// true and a.cancel set, so the next Run() queued forever and the child ctx
// leaked. finishProcessing must run on every exit path.
func TestRunInternal_ProcessingResetOnError(t *testing.T) {
	errAPI := provider.Api("test-err-ctx-leak")
	failing := &failingStreamProvider{api: errAPI}
	provider.RegisterApiProvider(failing)

	agent := NewAgent(Config{
		Model:        testModel(errAPI),
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
	})
	go func() {
		for range agent.Output {
		}
	}()

	_ = agent.Run(context.Background(), "turn")

	agent.mu.Lock()
	processing := agent.processing
	cancelSet := agent.cancel
	agent.mu.Unlock()

	if processing {
		t.Errorf("a.processing still true after error path (next Run would queue forever)")
	}
	if cancelSet != nil {
		t.Errorf("a.cancel still set after error path (ctx leak)")
	}

	// The agent must accept a new turn immediately (not queue it).
	p := textEventProvider("recovered")
	agent.cfg.Model = testModel(p.API())

	done := make(chan struct{})
	go func() {
		_ = agent.Run(context.Background(), "turn2")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("second Run queued instead of processed — processing not reset on error path")
	}
}

// failingStreamProvider's Stream always returns an error.
type failingStreamProvider struct{ api provider.Api }

func (p *failingStreamProvider) API() provider.Api { return p.api }

func (p *failingStreamProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	return nil, errStreamFails
}

func (p *failingStreamProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(model, ctx, provider.StreamOptions{})
}

type ctxLeakErr string

func (e ctxLeakErr) Error() string { return string(e) }

var errStreamFails ctxLeakErr = "stream failed"
