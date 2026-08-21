//go:build e2e

// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Local-LM end-to-end validation of the T3 multi-agent scrollback replay.
// Requires an OpenAI-compatible server at http://localhost:1234 (LMStudio /
// llama.cpp). Skips when the server is unreachable.
//
// Run: go test -count=1 -tags e2e -run TestE2E_MultiAgentScrollbackReplay ./internal/app/
package app

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	_ "github.com/pijalu/goa/internal/agentic/provider/openai" // register the openai-completions backend
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/provider"
	"github.com/pijalu/goa/tui/agentctx"
)

const (
	replayE2EEndpoint = "http://localhost:1234/v1/chat/completions"
	replayE2EModel    = "google/gemma-4-e4b"
	replayE2EProvider = "local"
)

// skipIfNoReplayLLM skips when the local LM is unreachable (the live-LM e2e
// gate: these tests are opt-in via the e2e build tag AND a running server).
func skipIfNoReplayLLM(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	modelsURL := strings.TrimSuffix(replayE2EEndpoint, "/chat/completions") + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL, nil)
	if err != nil {
		t.Fatalf("build models request: %v", err)
	}
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		t.Skipf("local LM not reachable at %s: %v", replayE2EEndpoint, err)
	}
	resp.Body.Close()
}

// replayE2EConfig builds a config pointing at the local LM with the T3
// scrollback-replay gate ON.
func replayE2EConfig() *config.Config {
	tru := true
	return &config.Config{
		ActiveProvider: replayE2EProvider,
		ActiveModel:    replayE2EModel,
		Providers: []config.ProviderConfig{
			{
				ID:           replayE2EProvider,
				Name:         "Local LM",
				Endpoint:     replayE2EEndpoint,
				DefaultModel: replayE2EModel,
			},
		},
		Features:  config.FeaturesConfig{MultiAgentScrollbackReplay: &tru},
		Execution: config.ExecutionConfig{Mode: internal.ExecutionYolo},
	}
}

// runLiveAgent runs one real agent against the local LM to completion and
// returns its captured event stream, in order.
func runLiveAgent(t *testing.T, mdl agenticprovider.Model, opts agenticprovider.StreamOptions, prompt string) []agentic.OutputEvent {
	t.Helper()
	agent := agentic.NewAgent(agentic.Config{
		Model:        mdl,
		SystemPrompt: "You are a terse test agent. Reply briefly.",
		StreamOptions: opts,
	})
	collector := &eventCollector{}
	agent.AddObserver(collector)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if _, err := agent.RunAndCollect(ctx, prompt); err != nil {
		t.Fatalf("live agent run: %v", err)
	}
	return collector.drain()
}

// TestE2E_MultiAgentScrollbackReplay is the Local-LM T3 e2e: two real agents
// produce genuine transcripts (a "main" turn and a "delegation" turn); the
// delegation accumulates a tall backlog; the replay gate is ON; deterministic
// tab switches replay the delegation's committed rows into the terminal
// scrollback. The assertion is the T3 invariant: no committed row is ever
// scroll-emitted twice across the switches (scrollable, uncorrupted history),
// and the switched-to view's tail is visible.
func TestE2E_MultiAgentScrollbackReplay(t *testing.T) {
	skipIfNoReplayLLM(t)

	cfg := replayE2EConfig()
	pm := provider.NewProviderManager(cfg)
	mdl, err := pm.ResolveActiveModel()
	if err != nil {
		t.Fatalf("ResolveActiveModel: %v", err)
	}
	opts := pm.BuildStreamOptions()

	// Two real turns on the live LM. Keep prompts trivial so the slow local
	// model stays within the timeout.
	mainEvents := runLiveAgent(t, mdl, opts, "Reply with exactly: MAIN-TURN-OK")
	delegEvents := runLiveAgent(t, mdl, opts, "Reply with exactly: DELEGATION-TURN-OK")

	// Build the UI scenario with the replay gate ON. The scenario's own cfg is
	// replaced by the e2e config so replayEnabled() resolves true.
	sc := newUIScenario(t, 100, 24)
	sc.app.subs.cfg = cfg
	runner := enableReplay(sc)

	// The main agent's turn streams through the real event path (active view).
	for i := range mainEvents {
		ev := mainEvents[i]
		sc.apply(&ev)
	}

	// The delegation runs as a second view, accumulating its real transcript
	// (plus a tall backlog so part of it commits to scrollback on switch).
	delegTr := addScenarioAgent(sc, "dlg-deleg-01")
	applyEventsToTranscript(sc, delegEvents, delegTr)
	delegBacklog := fillRows(sc, delegTr.View(), "DELEG-BACKLOG-", 40)
	sc.engine.RenderNow()

	if !snapshotContains(delegTr.Snapshot(), "DELEG-BACKLOG-"+padIdx(39)) {
		t.Fatal("delegation transcript missing its backlog")
	}

	switchTo := replaySwitch(t, sc, runner)

	// Switch to the delegation: its tall backlog replays into scrollback; the
	// visible window shows the backlog tail.
	switchTo("dlg-deleg-01")
	assertOnScreen(t, sc, "DELEG-BACKLOG-"+padIdx(39))
	assertNotOnScreen(t, sc, "MAIN-TURN-OK")

	// Churn back to main and re-switch to the delegation twice.
	for cycle := 0; cycle < 2; cycle++ {
		switchTo(agentctx.MainAgentID)
		switchTo("dlg-deleg-01")
		assertOnScreen(t, sc, "DELEG-BACKLOG-"+padIdx(39))
	}

	// The T3 invariant: no committed delegation row is scroll-emitted twice
	// across the whole churn (the watermark hand-back prevents re-scroll). The
	// count of exactly-once rows also guards vacuity (the backlog must reach
	// scrollback via the replay).
	scrolled := assertScrollEmittedOnce(t, sc, delegBacklog)
	if scrolled == 0 {
		t.Error("no delegation backlog row reached scrollback; replay path not exercised")
	}
	t.Logf("scrollback replay e2e: %d/%d delegation rows committed exactly once", scrolled, len(delegBacklog))
}
