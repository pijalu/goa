// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/agentic"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/agentic/testutil"
	"github.com/pijalu/goa/internal/event"
)

// contextResetObserver counts EventContextReset emissions.
type contextResetObserver struct {
	mu     sync.Mutex
	resets int
}

func (o *contextResetObserver) OnEvent(ev agentic.OutputEvent) {
	if ev.Type == agentic.EventContextReset {
		o.mu.Lock()
		o.resets++
		o.mu.Unlock()
	}
}

func (o *contextResetObserver) count() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.resets
}

// TestAgentManagerRunner_RunFreshEmitsContextReset is the wiring test for the
// fresh-context CM bug (bugs.md): the begin turn of a fresh-context goal —
// which clears the live context and rotates the provider cache key — must
// emit exactly one EventContextReset so the cache-bust detector re-arms and
// the new conversation's cold start is not counted as a cache miss.
// Subsequent turns of the same goal (begin=false) must NOT re-emit.
func TestAgentManagerRunner_RunFreshEmitsContextReset(t *testing.T) {
	p := testutil.NewSimulatedProvider([]testutil.SimulatedResponse{
		{Content: "turn one"},
		{Content: "turn two"},
	})
	agenticprovider.RegisterApiProvider(p)

	cfg := &config.Config{}
	bus := event.MakeBus(100, 100, 100, 100)
	am := core.NewAgentManager(cfg, nil, nil, nil, bus, "")
	if _, err := am.StartSession(goalSteeringModel(p.API()), agenticprovider.StreamOptions{}, "sys", nil, cfg); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	obs := &contextResetObserver{}
	am.CurrentAgent().AddObserver(obs)

	runner := &agentManagerRunner{agentMgr: am}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := runner.RunFresh(ctx, core.ContinuationPrompt, true); err != nil {
		t.Fatalf("RunFresh(begin=true): %v", err)
	}
	if got := obs.count(); got != 1 {
		t.Errorf("after begin=true: EventContextReset count = %d, want 1", got)
	}

	if err := runner.RunFresh(ctx, core.ContinuationPrompt, false); err != nil {
		t.Fatalf("RunFresh(begin=false): %v", err)
	}
	if got := obs.count(); got != 1 {
		t.Errorf("after begin=false: EventContextReset count = %d, want 1 (no re-emit on subsequent turns)", got)
	}
}
