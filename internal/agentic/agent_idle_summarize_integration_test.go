package agentic

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

func idleSummarizeCacheGeneration(agent *Agent) uint64 {
	agent.mu.Lock()
	defer agent.mu.Unlock()
	return agent.cacheGeneration
}

func installIdleSummarizeTestClock(agent *Agent, clock *idleTestClock, config IdleSummarizeConfig) {
	if agent.idleSummarize != nil {
		agent.idleSummarize.Close()
	}
	agent.idleSummarize = newIdleSummarizeSchedulerWithAfterFunc(agent.runIdleSummarize, clock.AfterFunc)
	agent.mu.Lock()
	agent.cfg.ContextCompression.IdleSummarize = config
	agent.mu.Unlock()
	agent.idleSummarize.Configure(config)
	agent.idleSummarize.Started()
}

func waitForIdleSummarizeCall(t *testing.T, p *provenanceSummarizeProvider, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if p.callCount() == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("summarize calls = %d, want %d", p.callCount(), want)
}

func TestAgentIdleSummarizeCompactsOnceAndAdvancesCacheGeneration(t *testing.T) {
	p := &provenanceSummarizeProvider{
		api:   provider.Api(fmt.Sprintf("idle-success-%d", testProviderCounter.Add(1))),
		reply: "## Primary Request and Intent\n- idle checkpoint",
	}
	agent := newProvenanceAgent(p, provenanceHistory())
	agent.SetContextWindow(10_000)
	clock := newIdleTestClock()
	installIdleSummarizeTestClock(agent, clock, IdleSummarizeConfig{TriggerPercent: 10, Delay: time.Hour})
	t.Cleanup(agent.idleSummarize.Close)
	generation := idleSummarizeCacheGeneration(agent)

	agent.idleSummarize.Stopped()
	clock.fire()
	waitForIdleSummarizeCall(t, p, 1)

	deadline := time.Now().Add(2 * time.Second)
	for idleSummarizeCacheGeneration(agent) == generation && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	history := agent.GetHistory()
	if len(history) != 2 || history[0].Role != User || history[1].Role != Assistant {
		t.Fatalf("compacted history = %+v, want summarize checkpoint pair", history)
	}
	if !strings.Contains(history[0].Content, "idle checkpoint") {
		t.Fatalf("summary user message = %q, want provider summary", history[0].Content)
	}
	if idleSummarizeCacheGeneration(agent) <= generation {
		t.Fatalf("cache generation = %d, want > %d after history replacement", idleSummarizeCacheGeneration(agent), generation)
	}
	clock.fire()
	time.Sleep(20 * time.Millisecond)
	if got := p.callCount(); got != 1 {
		t.Fatalf("summarize calls after duplicate firing = %d, want one attempt", got)
	}
}

func TestAgentIdleSummarizeRechecksContextThreshold(t *testing.T) {
	p := &provenanceSummarizeProvider{
		api:   provider.Api(fmt.Sprintf("idle-threshold-%d", testProviderCounter.Add(1))),
		reply: "unused",
	}
	agent := newProvenanceAgent(p, []Message{{Type: Content, Role: User, Content: "tiny"}})
	agent.SetContextWindow(1_000_000)
	clock := newIdleTestClock()
	installIdleSummarizeTestClock(agent, clock, IdleSummarizeConfig{TriggerPercent: 10, Delay: time.Hour})
	t.Cleanup(agent.idleSummarize.Close)

	agent.idleSummarize.Stopped()
	clock.fire()
	time.Sleep(20 * time.Millisecond)
	if got := p.callCount(); got != 0 {
		t.Fatalf("summarize calls below threshold = %d, want zero", got)
	}
}

func TestAgentRunStopArmsIdleSummarizeOnlyAfterTurn(t *testing.T) {
	p := &provenanceSummarizeProvider{
		api:   provider.Api(fmt.Sprintf("idle-stop-%d", testProviderCounter.Add(1))),
		reply: "answer",
	}
	agent := newProvenanceAgent(p, nil)
	clock := newIdleTestClock()
	installIdleSummarizeTestClock(agent, clock, IdleSummarizeConfig{TriggerPercent: 10, Delay: time.Hour})
	t.Cleanup(agent.idleSummarize.Close)

	if err := agent.Run(context.Background(), "hello"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	_ = clock.activeDelay(t)
}

func TestAgentIdleSummarizeFailureEmitsNotificationWithoutHistoryRewrite(t *testing.T) {
	p := &provenanceSummarizeProvider{
		api:     provider.Api(fmt.Sprintf("idle-failure-%d", testProviderCounter.Add(1))),
		failErr: fmt.Errorf("provider unavailable"),
	}
	original := provenanceHistory()
	agent := newProvenanceAgent(p, original)
	agent.SetContextWindow(10_000)
	clock := newIdleTestClock()
	installIdleSummarizeTestClock(agent, clock, IdleSummarizeConfig{TriggerPercent: 10, Delay: time.Hour})
	t.Cleanup(agent.idleSummarize.Close)
	generation := idleSummarizeCacheGeneration(agent)

	agent.idleSummarize.Stopped()
	clock.fire()
	waitForIdleSummarizeCall(t, p, 1)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		messages := agent.GetHistory()
		if len(messages) > 0 && messages[len(messages)-1].Metadata != nil && messages[len(messages)-1].Metadata["category"] == "system-notification" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	history := agent.GetHistory()
	last := history[len(history)-1]
	if last.Role != System || last.Metadata["category"] != "system-notification" {
		t.Fatalf("last message = %+v, want system failure notification", last)
	}
	if idleSummarizeCacheGeneration(agent) != generation {
		t.Fatalf("failed summarize changed cache generation from %d to %d", generation, idleSummarizeCacheGeneration(agent))
	}
}

func TestAgentNewRunCancelsAndWaitsForInflightIdleSummarize(t *testing.T) {
	p := &blockingCompactionProvider{
		api:                 provider.Api(fmt.Sprintf("idle-cancel-%d", testProviderCounter.Add(1))),
		compactionStarted:   make(chan struct{}, 1),
		releaseCompaction:   make(chan struct{}),
		conversationStarted: make(chan struct{}, 2),
	}
	provider.RegisterApiProvider(p)
	agent := NewAgent(Config{Model: testModel(p.API()), SystemPrompt: "test", Logger: NewLogger(Error)})
	agent.SetHistory(provenanceHistory())
	agent.SetContextWindow(10_000)
	clock := newIdleTestClock()
	installIdleSummarizeTestClock(agent, clock, IdleSummarizeConfig{TriggerPercent: 10, Delay: time.Hour})
	t.Cleanup(agent.idleSummarize.Close)

	agent.idleSummarize.Stopped()
	clock.fire()
	select {
	case <-p.compactionStarted:
	case <-time.After(time.Second):
		t.Fatal("idle summarize did not start")
	}
	runDone := make(chan error, 1)
	go func() { runDone <- agent.Run(context.Background(), "next exchange") }()
	select {
	case <-p.conversationStarted:
		t.Fatal("next exchange started before in-flight idle summarize unwound")
	case <-time.After(20 * time.Millisecond):
	}
	close(p.releaseCompaction)
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("next Run: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("next Run did not finish after summarize provider unwound")
	}
	for _, msg := range agent.GetHistory() {
		if strings.Contains(msg.Content, "checkpoint") {
			t.Fatalf("cancelled idle summarize installed checkpoint: %+v", msg)
		}
		if msg.Metadata["category"] == "system-notification" && strings.Contains(msg.Content, "Idle summarize failed") {
			t.Fatalf("cancelled idle summarize emitted failure notice: %+v", msg)
		}
	}
}
