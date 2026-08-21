//go:build e2e

// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Local-LM end-to-end validation of the T4 per-delegation routing (bug-2 fix).
// Requires an OpenAI-compatible server at http://localhost:1234 (LMStudio /
// llama.cpp). Skips when the server is unreachable.
//
// Run: go test -count=1 -tags e2e -run TestE2E_MultiAgentDelegation ./internal/app/
package app

import (
	"strings"
	"testing"

	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/multiagent"
	"github.com/pijalu/goa/tui"
	"github.com/pijalu/goa/tui/agentctx"
)

// newE2EDelegationStack wires a real AgentPool + orchestrator against the
// live LM, with one twist: the "planner" role resolves to a dead endpoint so
// its delegation fails terminally. The non-eligible retry policy (a codes
// list matching nothing) makes that failure surface immediately instead of
// burning the real backoff schedule; the live role never errors, so the
// policy is inert for it.
func newE2EDelegationStack(t *testing.T, sc *uiScenario) (*multiagent.ForegroundOrchestrator, *multiagent.DelegateTool) {
	t.Helper()
	live := agenticprovider.Model{
		ID:         replayE2EModel,
		Name:       replayE2EModel,
		Api:        "openai-completions",
		Provider:   replayE2EProvider,
		InputTypes: []string{"text"},
		BaseURL:    replayE2EEndpoint,
	}
	dead := agenticprovider.Model{
		ID:         replayE2EModel,
		Name:       replayE2EModel,
		Api:        "openai-completions",
		Provider:   replayE2EProvider,
		InputTypes: []string{"text"},
		BaseURL:    "http://localhost:9/v1/chat/completions", // discard port: terminally unreachable
	}
	opts := agenticprovider.StreamOptions{
		RetryPolicy: &agenticprovider.RetryPolicy{
			Mode:       agenticprovider.RetryModeNormal,
			MaxRetries: 1,
			Codes:      []string{"__never__"},
		},
	}
	pool := multiagent.NewAgentPool(live, opts, nil)
	pool.ModelFactory = func(modelName string) (agenticprovider.Model, error) {
		if modelName == "dead-model" {
			return dead, nil
		}
		return live, nil
	}
	pool.SetConfig("coder", multiagent.AgentConfig{ModelName: "live-model"})
	pool.SetConfig("planner", multiagent.AgentConfig{ModelName: "dead-model"})

	orch := multiagent.NewForegroundOrchestrator(pool)
	sc.app.subs.foregroundOrch = orch
	tool := &multiagent.DelegateTool{Orchestrator: orch, Pool: pool, Enabled: true}
	return orch, tool
}

// assertLiveDelegationTab checks the live coder delegation's tab: the result
// rendered in ITS transcript, terminal marker present, no leak into the main
// transcript, and no error state.
func assertLiveDelegationTab(t *testing.T, sc *uiScenario) {
	t.Helper()
	coderText := delegationTranscriptText(t, sc, "dlg-coder-01")
	if !strings.Contains(coderText, "LIVE-DELEGATION-OK") {
		t.Errorf("delegation transcript missing the live result:\n%s", coderText)
	}
	if !strings.Contains(coderText, "completed") {
		t.Errorf("delegation transcript missing the terminal marker:\n%s", coderText)
	}
	if snapshotContains(sc.chat.Snapshot(), "LIVE-DELEGATION-OK") {
		t.Error("delegation result leaked into the main transcript")
	}
	if _, errFlag := sc.app.subs.agentRegistry.Badges("dlg-coder-01"); errFlag {
		t.Error("successful delegation must not carry the error state")
	}
}

// assertFailedE2ETab checks bug-2's fix on the dead-endpoint delegation: the
// FAILED tab is marked and its transcript holds the error card.
func assertFailedE2ETab(t *testing.T, sc *uiScenario) {
	t.Helper()
	if _, errFlag := sc.app.subs.agentRegistry.Badges("dlg-planner-01"); !errFlag {
		t.Error("failed delegation did not mark its tab")
	}
	failedText := delegationTranscriptText(t, sc, "dlg-planner-01")
	if !strings.Contains(failedText, "FAILED") {
		t.Errorf("failed delegation transcript missing the error card:\n%s", failedText)
	}
}

// TestE2E_MultiAgentDelegationTabs is the Local-LM T4 e2e: a real delegate_to
// run against the live LM spawns a per-delegation tab whose transcript holds
// the result (and nothing leaks into the main transcript), and a delegation
// whose provider is terminally unreachable surfaces a marked FAILED tab with
// an error card (bug-2: delegations are visible from creation, failures
// always).
func TestE2E_MultiAgentDelegationTabs(t *testing.T) {
	skipIfNoReplayLLM(t)

	sc := newUIScenario(t, 100, 24)
	orch, tool := newE2EDelegationStack(t, sc)

	// drain feeds every buffered orchestrator message through the exact T4
	// routing entry point, on the test goroutine.
	drain := func() {
		for {
			select {
			case msg := <-orch.Events():
				sc.app.handleOrchestratorStreamMsg(msg, newStreamForwarder())
			default:
				return
			}
		}
	}

	// A REAL delegation against the live LM. Terse task to stay fast.
	if _, err := tool.Execute(`{"agent":"coder","task":"Reply with exactly: LIVE-DELEGATION-OK"}`); err != nil {
		t.Fatalf("live delegate_to: %v", err)
	}
	drain()
	assertLiveDelegationTab(t, sc)

	// A forced terminal failure: the dead endpoint rejects the request and
	// the non-eligible retry policy surfaces it immediately.
	if _, err := tool.Execute(`{"agent":"planner","task":"plan anything"}`); err == nil {
		t.Fatal("dead-endpoint delegate_to should fail")
	}
	drain()
	assertFailedE2ETab(t, sc)

	// Tab strip state: main + two delegations, main still active.
	if got := sc.app.subs.agentRegistry.Len(); got != 3 {
		t.Errorf("registry views = %d, want 3 (main + coder + failed planner)", got)
	}
	if active, _ := sc.app.subs.agentRegistry.Active(); active != agentctx.MainAgentID {
		t.Errorf("active view = %q, want main", active)
	}

	sc.engine.RenderNow()
	sc.film.Capture("e2e-final", sc.engine.AgentFrame(), sc.status.Text())
	t.Logf("e2e filmstrip:\n%s", sc.film.Render())
}
