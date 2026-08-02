// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal/agentic/provider"
)

// busyBlockingProvider holds its stream open until released, keeping the
// agent in the processing state for as long as the test needs.
type busyBlockingProvider struct {
	api       provider.Api
	started   chan struct{}
	release   chan struct{}
	startOnce sync.Once
}

func (p *busyBlockingProvider) API() provider.Api { return p.api }

func (p *busyBlockingProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	result := provider.NewAssistantMessageEventStream(64)
	p.startOnce.Do(func() { close(p.started) })
	go func() {
		<-p.release
		result.Push(provider.AssistantMessageEvent{Type: provider.EventTextStart, ContentIndex: 0})
		result.Push(provider.AssistantMessageEvent{Type: provider.EventTextDelta, ContentIndex: 0, Delta: "done"})
		result.Push(provider.AssistantMessageEvent{Type: provider.EventTextEnd, ContentIndex: 0})
		result.End(&provider.AssistantMessage{
			Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "done"}},
			StopReason: provider.StopReasonEndTurn,
		})
	}()
	return result, nil
}

func (p *busyBlockingProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(model, ctx, provider.StreamOptions{})
}

// TestAgentManager_IsBusyDuringExternallyDrivenTurn pins the semantics behind
// the goal-steering fix: IsRunning only tracks manager-owned user turns, so an
// externally driven turn (the goal driver calls agent.Run directly) leaves it
// false. IsBusy must additionally reflect the agent's own processing state so
// steering routing works during goal continuation turns.
func TestAgentManager_IsBusyDuringExternallyDrivenTurn(t *testing.T) {
	p := &busyBlockingProvider{
		api:     provider.Api("test-am-busy-" + time.Now().Format("150405.000000000")),
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	provider.RegisterApiProvider(p)

	cfg := &config.Config{}
	am := NewAgentManager(cfg, nil, nil, nil, nil, "")
	mdl := provider.Model{
		ID:       "test-model",
		Name:     "test-model",
		Api:      p.api,
		Provider: provider.ProviderCustom,
	}
	if _, err := am.StartSession(mdl, provider.StreamOptions{}, "sys", nil, cfg); err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	if am.IsBusy() {
		t.Error("IsBusy = true before any turn, want false")
	}

	// Drive the turn the way the goal driver does: agent.Run directly.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	turnDone := make(chan struct{})
	go func() {
		_ = am.CurrentAgent().Run(ctx, ContinuationPrompt)
		close(turnDone)
	}()

	select {
	case <-p.started:
	case <-time.After(2 * time.Second):
		t.Fatal("turn did not reach the provider stream")
	}

	if am.IsRunning() {
		t.Error("IsRunning = true during externally driven turn, want false (only SendUserInput sets it)")
	}
	if !am.IsBusy() {
		t.Error("IsBusy = false during externally driven turn, want true (agent is processing)")
	}

	close(p.release)
	select {
	case <-turnDone:
	case <-time.After(2 * time.Second):
		t.Fatal("turn did not finish after release")
	}
	if am.IsBusy() {
		t.Error("IsBusy = true after turn completion, want false")
	}
}
