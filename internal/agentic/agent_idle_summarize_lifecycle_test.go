// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

func TestAgentIdleSummarizeThresholdBoundary(t *testing.T) {
	for _, tc := range []struct {
		name         string
		anchorTokens int
		wantCalls    int
	}{
		{name: "below ten percent", anchorTokens: 999, wantCalls: 0},
		{name: "exactly ten percent", anchorTokens: 1000, wantCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &provenanceSummarizeProvider{
				api:   provider.Api(fmt.Sprintf("idle-boundary-%d", testProviderCounter.Add(1))),
				reply: "boundary checkpoint",
			}
			agent := newProvenanceAgent(p, provenanceHistory())
			agent.SetContextWindow(10_000)
			agent.mu.Lock()
			agent.cfg.ContextCompression.IdleSummarize = IdleSummarizeConfig{TriggerPercent: 10, Delay: time.Hour}
			agent.lastGrossInputTokens = tc.anchorTokens
			agent.lastUsageHistoryLen = len(agent.history)
			agent.mu.Unlock()

			if err := agent.runIdleSummarize(context.Background()); err != nil {
				t.Fatalf("runIdleSummarize: %v", err)
			}
			if got := p.callCount(); got != tc.wantCalls {
				t.Fatalf("summarize calls = %d, want %d at projected occupancy %d/10000", got, tc.wantCalls, tc.anchorTokens)
			}
		})
	}
}

func TestAgentIdleSummarizeDoesNotOverlapActiveHistoryRead(t *testing.T) {
	p := &provenanceSummarizeProvider{
		api:   provider.Api(fmt.Sprintf("idle-active-%d", testProviderCounter.Add(1))),
		reply: "must not run",
	}
	agent := newProvenanceAgent(p, provenanceHistory())
	agent.SetContextWindow(10_000)
	agent.mu.Lock()
	agent.cfg.ContextCompression.IdleSummarize = IdleSummarizeConfig{TriggerPercent: 10, Delay: time.Hour}
	agent.mu.Unlock()

	agent.activityMu.RLock()
	done := make(chan error, 1)
	go func() { done <- agent.runIdleSummarize(context.Background()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runIdleSummarize during activity: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("idle summarize blocked behind an active turn instead of yielding")
	}
	agent.activityMu.RUnlock()
	if got := p.callCount(); got != 0 {
		t.Fatalf("summarize calls during active turn = %d, want zero", got)
	}
}

func TestAgentIdleSummarizeStopClearAndRestoreInvalidateTimer(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reset func(*Agent)
	}{
		{name: "stop", reset: func(a *Agent) { a.Stop() }},
		{name: "clear", reset: func(a *Agent) { a.Clear() }},
		{name: "history restore", reset: func(a *Agent) { a.SetHistory(provenanceHistory()) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &provenanceSummarizeProvider{
				api:   provider.Api(fmt.Sprintf("idle-reset-%d", testProviderCounter.Add(1))),
				reply: "stale checkpoint",
			}
			agent := newProvenanceAgent(p, provenanceHistory())
			agent.SetContextWindow(10_000)
			clock := newIdleTestClock()
			installIdleSummarizeTestClock(agent, clock, IdleSummarizeConfig{TriggerPercent: 10, Delay: time.Hour})
			t.Cleanup(agent.idleSummarize.Close)
			agent.idleSummarize.Stopped()
			_ = clock.activeDelay(t)

			tc.reset(agent)
			clock.fire()
			time.Sleep(20 * time.Millisecond)
			if got := p.callCount(); got != 0 {
				t.Fatalf("stale timer made %d summarize calls, want zero", got)
			}
		})
	}
}

func TestAgentSetContextCompressionReconfiguresIdleTimer(t *testing.T) {
	p := &provenanceSummarizeProvider{
		api:   provider.Api(fmt.Sprintf("idle-reconfigure-%d", testProviderCounter.Add(1))),
		reply: "reconfigured checkpoint",
	}
	agent := newProvenanceAgent(p, provenanceHistory())
	agent.SetContextWindow(10_000)
	clock := newIdleTestClock()
	installIdleSummarizeTestClock(agent, clock, IdleSummarizeConfig{TriggerPercent: 10, Delay: time.Hour})
	t.Cleanup(agent.idleSummarize.Close)
	agent.idleSummarize.Stopped()

	cfg := agent.CompressionConfig()
	cfg.IdleSummarize.Delay = 0
	agent.SetContextCompression(cfg)
	clock.fire()
	time.Sleep(20 * time.Millisecond)
	if got := p.callCount(); got != 0 {
		t.Fatalf("disabled reconfiguration made %d summarize calls, want zero", got)
	}

	cfg.IdleSummarize.Delay = time.Hour
	agent.SetContextCompression(cfg)
	_ = clock.activeDelay(t)
	clock.fire()
	waitForIdleSummarizeCall(t, p, 1)
}
