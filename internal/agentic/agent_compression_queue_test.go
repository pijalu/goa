// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

type blockingCompactionProvider struct {
	api                 provider.Api
	compactionStarted   chan struct{}
	releaseCompaction   chan struct{}
	conversationStarted chan struct{}
}

func (p *blockingCompactionProvider) API() provider.Api { return p.api }

func (p *blockingCompactionProvider) Stream(_ provider.Model, _ provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	stream := provider.NewAssistantMessageEventStream(8)
	if opts.Purpose == provider.PurposeCompaction {
		select {
		case p.compactionStarted <- struct{}{}:
		default:
		}
		// Keep Stream itself in flight until released. Returning a stream whose
		// producer is blocked would let cancellation unwind its consumer early.
		<-p.releaseCompaction
		stream.Push(provider.AssistantMessageEvent{Type: provider.EventTextDelta, Delta: "checkpoint"})
		stream.End(&provider.AssistantMessage{StopReason: provider.StopReasonEndTurn})
		return stream, nil
	}
	p.conversationStarted <- struct{}{}
	go func() {
		stream.Push(provider.AssistantMessageEvent{Type: provider.EventTextDelta, Delta: "answer"})
		stream.End(&provider.AssistantMessage{StopReason: provider.StopReasonEndTurn})
	}()
	return stream, nil
}

func (p *blockingCompactionProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(model, ctx, provider.BuildSimpleOptions(model, opts))
}

func TestAgent_AutomaticCompressionBlocksQueuedInputUntilComplete(t *testing.T) {
	p := &blockingCompactionProvider{
		api:                 provider.Api(fmt.Sprintf("compression-queue-%d", testProviderCounter.Add(1))),
		compactionStarted:   make(chan struct{}, 1),
		releaseCompaction:   make(chan struct{}),
		conversationStarted: make(chan struct{}, 4),
	}
	provider.RegisterApiProvider(p)
	t.Cleanup(func() {
		select {
		case <-p.releaseCompaction:
		default:
			close(p.releaseCompaction)
		}
	})

	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
		ContextCompression: ContextCompressionConfig{
			MaxTokens:        1000,
			DisableCacheGate: true,
			Thresholds:       CompressionThresholds{HardPercent: 50},
			Strategies:       CompressionLayerStrategies{Hard: CompressionSummarize},
		},
	})
	agent.SetHistory([]Message{
		{Type: Content, Role: User, Content: strings.Repeat("old input ", 100)},
		{Type: Content, Role: Assistant, Content: strings.Repeat("old answer ", 100)},
	})

	runDone := make(chan error, 1)
	go func() { runDone <- agent.Run(context.Background(), "first") }()
	select {
	case <-p.compactionStarted:
	case <-time.After(time.Second):
		t.Fatal("automatic summarize did not start")
	}

	if err := agent.Run(context.Background(), "queued"); err != nil {
		t.Fatalf("queue input during compression: %v", err)
	}
	select {
	case <-p.conversationStarted:
		t.Fatal("conversation request started before compression completed")
	case <-time.After(20 * time.Millisecond):
	}

	close(p.releaseCompaction)
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("agent Run: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued turns did not finish after compression completed")
	}
	if got := len(p.conversationStarted); got != 2 {
		t.Errorf("conversation requests = %d, want original input and queued input", got)
	}
}
