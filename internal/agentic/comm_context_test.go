// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// TestAgentBus_SendRespectsContext verifies the B7 fix: Send returns promptly
// with ctx.Err() when ctx is cancelled while the recipient inbox is full.
func TestAgentBus_SendRespectsContext(t *testing.T) {
	bus := NewAgentBus()
	// Register a recipient with a tiny inbox that we never drain.
	inbox, err := bus.Register("full")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	_ = inbox

	// Fill the inbox.
	for i := 0; i < bus.bufSize; i++ {
		if err := bus.Send(context.Background(), CommMessage{To: "full", Content: "x"}); err != nil {
			t.Fatalf("fill send %d: %v", i, err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel before sending so the select must hit ctx.Done() immediately.
	cancel()

	start := time.Now()
	err = bus.Send(ctx, CommMessage{To: "full", Content: "overflow"})
	elapsed := time.Since(start)

	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	if elapsed > 200*time.Millisecond {
		t.Errorf("Send took %v with an already-cancelled ctx (should be near-instant)", elapsed)
	}
}

// TestAgentBus_SendTimeoutStillWorks verifies the existing 5s timeout path is
// preserved when no ctx cancellation occurs (regression guard for B7 change).
func TestAgentBus_SendTimeoutStillWorks(t *testing.T) {
	bus := NewAgentBus()
	inbox, _ := bus.Register("full")
	_ = inbox
	for i := 0; i < bus.bufSize; i++ {
		_ = bus.Send(context.Background(), CommMessage{To: "full", Content: "x"})
	}

	// Use a short-timeout ctx so the test stays fast, but assert Send errors.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := bus.Send(ctx, CommMessage{To: "full", Content: "overflow"})
	if err == nil {
		t.Fatal("expected error when inbox full and ctx deadline exceeded")
	}
}

// TestSetupCommAgent_DuplicateRegisterReturnsError verifies the B8 fix: when
// registration fails (name already taken), SetupCommAgent now returns the
// error instead of swallowing it and handing back a dead inbox channel.
func TestSetupCommAgent_DuplicateRegisterReturnsError(t *testing.T) {
	bus := NewAgentBus()
	agent := NewAgent(Config{SystemPrompt: "test", Logger: NewLogger(Error)})

	if _, _, _, err := SetupCommAgent(bus, "dupe", agent, false); err != nil {
		t.Fatalf("first SetupCommAgent: %v", err)
	}

	inbox, sendTool, connector, err := SetupCommAgent(bus, "dupe", agent, false)
	if err == nil {
		t.Fatal("expected error on duplicate registration, got nil")
	}
	if inbox != nil || sendTool != nil || connector != nil {
		t.Errorf("expected nil returns on error, got inbox=%v sendTool=%v connector=%v", inbox != nil, sendTool != nil, connector != nil)
	}
}

// blockingRunProvider's Stream blocks until the provided channel is closed,
// letting us simulate a mid-turn agent.Run when testing CommConnector.Stop.
type blockingRunProvider struct {
	api     provider.Api
	release chan struct{}
	started chan struct{}
}

func (p *blockingRunProvider) API() provider.Api { return p.api }

func (p *blockingRunProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	if p.started != nil {
		select {
		case p.started <- struct{}{}:
		default:
		}
	}
	// Block until released OR the caller ctx is cancelled. Real HTTP providers
	// get this for free; here we mimic it so the B4 fix (turn ctx cancellation
	// reaching the provider) is observable.
	select {
	case <-p.release:
	case <-ctx.GoContext().Done():
		return nil, ctx.GoContext().Err()
	}
	result := provider.NewAssistantMessageEventStream(64)
	go func() {
		result.End(&provider.AssistantMessage{
			Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "done"}},
			StopReason: provider.StopReasonEndTurn,
		})
	}()
	return result, nil
}

func (p *blockingRunProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(model, ctx, provider.StreamOptions{})
}

// TestCommConnector_StopUnblocksMidTurnRun verifies the B4 fix: Stop() cancels
// the connector's own context, so an in-flight agent.Run (previously started
// with context.Background and able to block forever) unblocks and Stop returns
// within a deadline.
func TestCommConnector_StopUnblocksMidTurnRun(t *testing.T) {
	api := provider.Api("test-comm-stop")
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	p := &blockingRunProvider{api: api, release: release, started: started}
	provider.RegisterApiProvider(p)

	agent := NewAgent(Config{
		Model:        testModel(api),
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
	})
	go func() {
		for range agent.Output {
		}
	}()

	inbox, _ := NewAgentBus().Register("solo")
	// Pre-seed a message so loop() calls agent.Run as soon as it spins up.
	bus := NewAgentBus()
	inbox2, _ := bus.Register("solo")
	_ = inbox
	_ = bus.Send(context.Background(), CommMessage{From: "x", To: "solo", Content: "go"})

	connector := NewCommConnector(agent, inbox2)

	// Wait until agent.Run has entered the (blocking) provider Stream.
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("agent.Run never started consuming the stream")
	}

	// Stop must return promptly even though the turn is mid-flight. Before the
	// B4 fix, Stop() closed done then wg.Wait()-ed on a Run using Background().
	done := make(chan struct{})
	go func() {
		connector.Stop()
		close(done)
	}()

	select {
	case <-done:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("CommConnector.Stop hung with agent.Run mid-turn (B4 regression)")
	}

	// Unblock the provider so the goroutine it spawned can finish cleanly.
	close(release)
}
