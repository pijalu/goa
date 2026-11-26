// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

type idleSummarizeE2ERequest struct {
	purpose       provider.Purpose
	messages      []provider.Message
	promptCacheID string
}

type idleSummarizeE2EProvider struct {
	api provider.Api

	mu       sync.Mutex
	requests []idleSummarizeE2ERequest
}

func (p *idleSummarizeE2EProvider) API() provider.Api { return p.api }

func (p *idleSummarizeE2EProvider) Stream(_ provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	p.mu.Lock()
	p.requests = append(p.requests, idleSummarizeE2ERequest{
		purpose:       opts.Purpose,
		messages:      append([]provider.Message(nil), ctx.Messages...),
		promptCacheID: opts.PromptCacheKey,
	})
	requestNumber := len(p.requests)
	p.mu.Unlock()

	answer := fmt.Sprintf("conversation answer %d", requestNumber)
	if opts.Purpose == provider.PurposeCompaction {
		answer = "## Primary Request and Intent\n- compressed checkpoint from idle timer"
	}
	stream := provider.NewAssistantMessageEventStream(8)
	go func() {
		stream.Push(provider.AssistantMessageEvent{Type: provider.EventTextDelta, Delta: answer})
		stream.End(&provider.AssistantMessage{
			Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: answer}},
			StopReason: provider.StopReasonEndTurn,
		})
	}()
	return stream, nil
}

func (p *idleSummarizeE2EProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(model, ctx, provider.BuildSimpleOptions(model, opts))
}

func (p *idleSummarizeE2EProvider) snapshot() []idleSummarizeE2ERequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]idleSummarizeE2ERequest(nil), p.requests...)
}

func idleSummarizeRequestText(req idleSummarizeE2ERequest) string {
	var b strings.Builder
	for _, msg := range req.messages {
		for _, block := range msg.Content {
			b.WriteString(block.Text)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

type idleSummarizeE2EFixture struct {
	agent           *Agent
	provider        *idleSummarizeE2EProvider
	clock           *idleTestClock
	originalRequest string
	originalAnswer  string
}

func newIdleSummarizeE2EFixture(t *testing.T) *idleSummarizeE2EFixture {
	p := &idleSummarizeE2EProvider{api: provider.Api(fmt.Sprintf("idle-e2e-%d", testProviderCounter.Add(1)))}
	provider.RegisterApiProvider(p)
	model := testModel(p.API())
	model.ContextWindow = 8_000
	idleCfg := IdleSummarizeConfig{TriggerPercent: 10, Delay: 4 * time.Minute}
	agent := NewAgent(Config{
		Model: model, SystemPrompt: "You are helpful", Logger: NewLogger(Error),
		ContextCompression: ContextCompressionConfig{IdleSummarize: idleCfg},
	})
	f := &idleSummarizeE2EFixture{
		agent: agent, provider: p, clock: newIdleTestClock(),
		originalRequest: "ORIGINAL REQUEST " + strings.Repeat("preserve-only-in-summary ", 100),
		originalAnswer:  "ORIGINAL ANSWER " + strings.Repeat("old-context-payload ", 100),
	}
	agent.SetHistory([]Message{
		{Type: Content, Role: System, Content: "You are helpful"},
		{Type: Content, Role: User, Content: f.originalRequest},
		{Type: Content, Role: Assistant, Content: f.originalAnswer},
	})
	installIdleSummarizeTestClock(agent, f.clock, idleCfg)
	t.Cleanup(agent.idleSummarize.Close)
	return f
}

func (f *idleSummarizeE2EFixture) runFirstExchange(t *testing.T) (uint64, string) {
	if err := f.agent.Run(context.Background(), "first exchange"); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if delay := f.clock.activeDelay(t); delay > 4*time.Minute || delay < 4*time.Minute-time.Second {
		t.Fatalf("post-stop timer delay = %v, want four minutes", delay)
	}
	before := f.provider.snapshot()
	if len(before) != 1 || before[0].purpose == provider.PurposeCompaction {
		t.Fatalf("requests before timer = %+v, want one conversation request", before)
	}
	return idleSummarizeCacheGeneration(f.agent), before[0].promptCacheID
}

func (f *idleSummarizeE2EFixture) waitForCompaction(t *testing.T, generationBefore uint64) {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		requests := f.provider.snapshot()
		if len(requests) >= 2 && idleSummarizeCacheGeneration(f.agent) > generationBefore {
			break
		}
		time.Sleep(time.Millisecond)
	}
	requests := f.provider.snapshot()
	if len(requests) != 2 || requests[1].purpose != provider.PurposeCompaction {
		t.Fatalf("requests after timer = %+v, want exactly one summarize request", requests)
	}
	if got := idleSummarizeCacheGeneration(f.agent); got != generationBefore+1 {
		t.Fatalf("cache generation = %d, want %d", got, generationBefore+1)
	}
	checkpoint := f.agent.GetHistory()
	if len(checkpoint) != 2 || !strings.Contains(checkpoint[0].Content, "compressed checkpoint from idle timer") {
		t.Fatalf("post-idle history = %+v, want one summarize checkpoint pair", checkpoint)
	}
	f.agent.idleSummarize.mu.Lock()
	rearmed := f.agent.idleSummarize.timer != nil
	f.agent.idleSummarize.mu.Unlock()
	if rearmed {
		t.Fatal("idle summarize completion rearmed itself without a new exchange")
	}
}

func (f *idleSummarizeE2EFixture) assertNextUsesCheckpoint(t *testing.T, cacheIDBefore string) {
	if err := f.agent.Run(context.Background(), "next exchange"); err != nil {
		t.Fatalf("next Run: %v", err)
	}
	requests := f.provider.snapshot()
	if len(requests) != 3 || requests[2].purpose == provider.PurposeCompaction {
		t.Fatalf("final requests = %+v, want the next conversation request", requests)
	}
	nextText := idleSummarizeRequestText(requests[2])
	if !strings.Contains(nextText, "compressed checkpoint from idle timer") {
		t.Errorf("next request did not contain compressed checkpoint:\n%s", nextText)
	}
	if strings.Contains(nextText, f.originalRequest) || strings.Contains(nextText, f.originalAnswer) {
		t.Errorf("next request still contained the full pre-compaction transcript")
	}
	if !strings.Contains(nextText, "next exchange") {
		t.Errorf("next request omitted the new user input:\n%s", nextText)
	}
	if requests[2].promptCacheID == "" || requests[2].promptCacheID == cacheIDBefore {
		t.Errorf("next request cache ID = %q, want a non-empty rotated ID (before %q)", requests[2].promptCacheID, cacheIDBefore)
	}
}

// TestAgentIdleSummarizeEndToEnd drives the complete SDK path with real Run,
// stream, summarize, checkpoint, and cache-identity machinery. Only the timer
// and provider transport are controlled: no compaction internals are called.
func TestAgentIdleSummarizeEndToEnd(t *testing.T) {
	f := newIdleSummarizeE2EFixture(t)
	generationBefore, cacheIDBefore := f.runFirstExchange(t)
	f.clock.fire()
	f.waitForCompaction(t, generationBefore)
	f.assertNextUsesCheckpoint(t, cacheIDBefore)
}
