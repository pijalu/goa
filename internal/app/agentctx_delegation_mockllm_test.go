// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/agentic/provider/mock"
	"github.com/pijalu/goa/multiagent"
	"github.com/pijalu/goa/tui"
	"github.com/pijalu/goa/tui/agentctx"
)

// delegationMockFixture wires the REAL delegation stack against the scripted
// mock-LLM provider: DelegateTool → AgentPool (per-role mock models) →
// ForegroundOrchestrator event feed → the T4 routing entry point → the
// per-agent registry. The app side stays single-goroutine (the uiScenario
// harness runs no engine loops): the test drains the orchestrator's buffered
// event channel synchronously, replacing only the forwarder's select loop.
type delegationMockFixture struct {
	sc   *uiScenario
	orch *multiagent.ForegroundOrchestrator
	tool *multiagent.DelegateTool
}

func newDelegationMockFixture(t *testing.T, prov *mock.Provider) *delegationMockFixture {
	t.Helper()
	sc := newUIScenario(t, 100, 24)

	// Non-eligible retry policy so scripted failures surface immediately
	// instead of burning the real backoff schedule.
	opts := agenticprovider.StreamOptions{
		RetryPolicy: &agenticprovider.RetryPolicy{Mode: agenticprovider.RetryModeNormal, MaxRetries: 1, Codes: []string{}},
	}
	pool := multiagent.NewAgentPool(prov.Model("default-model"), opts, nil)
	pool.ModelFactory = func(modelName string) (agenticprovider.Model, error) {
		return prov.Model(modelName), nil
	}
	pool.SetConfig("coder", multiagent.AgentConfig{ModelName: "coder-model"})
	pool.SetConfig("planner", multiagent.AgentConfig{ModelName: "planner-model"})

	orch := multiagent.NewForegroundOrchestrator(pool)
	sc.app.subs.foregroundOrch = orch

	return &delegationMockFixture{
		sc:   sc,
		orch: orch,
		tool: &multiagent.DelegateTool{Orchestrator: orch, Pool: pool, Enabled: true},
	}
}

// drainDelegationEvents feeds every buffered orchestrator message through the
// exact T4 routing entry point, on the test goroutine (the sole owner of app
// state in the single-goroutine harness).
func (f *delegationMockFixture) drainDelegationEvents(t *testing.T) {
	t.Helper()
	for {
		select {
		case msg := <-f.orch.Events():
			f.sc.app.handleOrchestratorStreamMsg(msg, newStreamForwarder())
		default:
			return
		}
	}
}

// delegationTranscriptText returns the joined snapshot text of one registry view.
func delegationTranscriptText(t *testing.T, sc *uiScenario, id string) string {
	t.Helper()
	v, ok := sc.app.subs.agentRegistry.Get(id)
	if !ok {
		t.Fatalf("no registry view for %q (views: %v)", id, sc.app.subs.agentRegistry.IDs())
	}
	var b strings.Builder
	for _, m := range v.Transcript.Snapshot() {
		b.WriteString(m.Text)
		b.WriteByte('\n')
	}
	return b.String()
}

// wantDelegationViews is every registry view the scenario must produce, in
// creation order: the main agent plus four delegations.
var wantDelegationViews = []string{
	agentctx.MainAgentID, "dlg-coder-01", "dlg-planner-01", "dlg-coder-02", "dlg-planner-02",
}

// successfulDelegationIDs are the delegations expected to complete cleanly.
var successfulDelegationIDs = []string{"dlg-coder-01", "dlg-planner-01", "dlg-coder-02"}

// interleaveMarkers are delegation artifacts that must never reach the main chat.
var interleaveMarkers = []string{"CODER-REPLY-ONE", "CODER-REPLY-TWO", "PLANNER-REPLY", "FAILED"}

// startHeldCoderDelegation launches dlg-coder-01 on a background goroutine
// with its provider reply held open mid-stream, and waits for its tab to be
// created. It returns the release gate and the run's result channel.
func startHeldCoderDelegation(t *testing.T, fx *delegationMockFixture, prov *mock.Provider) (chan struct{}, <-chan error) {
	t.Helper()
	turn := mock.TextTurn("CODER-REPLY-ONE")
	gate := make(chan struct{})
	prov.Script("coder-model", mock.Turn{Events: turn.Events, Final: turn.Final, Hold: gate})
	prov.ReplyText("planner-model", "PLANNER-REPLY")

	done := make(chan error, 1)
	go func() {
		_, err := fx.tool.Execute(`{"agent":"coder","task":"build feature one"}`)
		done <- err
	}()

	// Wait until the coder delegation is created (tab spawned on creation).
	waitFor(t, fx.sc.engine, 5*time.Second, func() bool {
		_, ok := fx.sc.app.subs.agentRegistry.Get("dlg-coder-01")
		if !ok {
			fx.drainDelegationEvents(t)
		}
		return ok
	}, "coder delegation tab did not appear on creation")
	return gate, done
}

// runDelegationOK executes one delegate_to and fails the test if it errors.
func runDelegationOK(t *testing.T, fx *delegationMockFixture, input string) {
	t.Helper()
	if _, err := fx.tool.Execute(input); err != nil {
		t.Fatalf("delegate_to(%s): %v", input, err)
	}
	fx.drainDelegationEvents(t)
}

// runFailingDelegation executes one delegate_to expected to fail (scripted
// provider error) and drains its terminal lifecycle into the views.
func runFailingDelegation(t *testing.T, fx *delegationMockFixture, input string) {
	t.Helper()
	if _, err := fx.tool.Execute(input); err == nil {
		t.Fatalf("delegate_to(%s) should fail", input)
	}
	fx.drainDelegationEvents(t)
}

// assertDelegationTabs checks that every delegation spawned exactly one tab.
func assertDelegationTabs(t *testing.T, fx *delegationMockFixture) {
	t.Helper()
	got := fx.sc.app.subs.agentRegistry.IDs()
	if strings.Join(got, ",") != strings.Join(wantDelegationViews, ",") {
		t.Fatalf("registry views = %v, want %v", got, wantDelegationViews)
	}
}

// assertDelegationIsolation proves each delegation's transcript holds ONLY its
// own reply — no cross-contamination between concurrent delegations.
func assertDelegationIsolation(t *testing.T, fx *delegationMockFixture) {
	t.Helper()
	coderOne := delegationTranscriptText(t, fx.sc, "dlg-coder-01")
	if !strings.Contains(coderOne, "CODER-REPLY-ONE") || strings.Contains(coderOne, "CODER-REPLY-TWO") || strings.Contains(coderOne, "PLANNER-REPLY") {
		t.Errorf("dlg-coder-01 transcript contaminated:\n%s", coderOne)
	}
	coderTwo := delegationTranscriptText(t, fx.sc, "dlg-coder-02")
	if !strings.Contains(coderTwo, "CODER-REPLY-TWO") || strings.Contains(coderTwo, "CODER-REPLY-ONE") {
		t.Errorf("dlg-coder-02 transcript contaminated:\n%s", coderTwo)
	}
	planner := delegationTranscriptText(t, fx.sc, "dlg-planner-01")
	if !strings.Contains(planner, "PLANNER-REPLY") {
		t.Errorf("dlg-planner-01 transcript missing its reply:\n%s", planner)
	}
}

// assertDelegationTerminals checks the successful delegations carry a
// completed marker and no error badge.
func assertDelegationTerminals(t *testing.T, fx *delegationMockFixture) {
	t.Helper()
	for _, id := range successfulDelegationIDs {
		s := delegationTranscriptText(t, fx.sc, id)
		if !strings.Contains(s, "completed") {
			t.Errorf("%s transcript missing terminal marker:\n%s", id, s)
		}
		if _, errFlag := fx.sc.app.subs.agentRegistry.Badges(id); errFlag {
			t.Errorf("%s carries the error state but succeeded", id)
		}
	}
}

// assertFailedDelegationCard verifies the bug-2 regression: a scripted
// provider-400 delegation leaves a marked tab plus an error card carrying the
// provider detail in its own transcript.
func assertFailedDelegationCard(t *testing.T, fx *delegationMockFixture) {
	t.Helper()
	if _, errFlag := fx.sc.app.subs.agentRegistry.Badges("dlg-planner-02"); !errFlag {
		t.Error("failed delegation dlg-planner-02 did not mark its tab")
	}
	s := delegationTranscriptText(t, fx.sc, "dlg-planner-02")
	if !strings.Contains(s, "FAILED") || !strings.Contains(s, "provider 400: invalid request") {
		t.Errorf("failed delegation transcript missing the error card:\n%s", s)
	}
}

// assertMainTranscriptClean proves the main chat holds no delegation content
// and no companion-section entries at all — the AddCompanionCycle interleave
// is retired for delegations.
func assertMainTranscriptClean(t *testing.T, fx *delegationMockFixture) {
	t.Helper()
	main := fx.sc.chat.Snapshot()
	for _, marker := range interleaveMarkers {
		if snapshotContains(main, marker) {
			t.Errorf("main transcript contains delegation marker %q — interleave not retired", marker)
		}
	}
	for _, m := range main {
		if m.Type == tui.ConsoleCompanionMessage {
			t.Error("main transcript holds a companion section for delegation traffic")
		}
	}
}

// TestAgentCtx_DelegationMockLLM is the T4 mandatory scripted-provider test:
// real delegate_to runs against the mock LLM — a coder held mid-stream while
// a planner completes (true cross-role concurrency), a SECOND coder
// delegation reusing the pooled agent under a new id, and a scripted
// provider-400 delegation. Each delegation's stream must land ONLY in its own
// per-delegation transcript, the main transcript must carry NO interleaved
// delegation content, and the failed delegation must leave a marked tab with
// an error card (bug-2).
func TestAgentCtx_DelegationMockLLM(t *testing.T) {
	prov := mock.New(t)
	fx := newDelegationMockFixture(t, prov)

	// Delegation 1 (coder, dlg-coder-01) blocks mid-stream on a background
	// goroutine so delegation 2 is genuinely concurrent with it.
	gate, coderOneDone := startHeldCoderDelegation(t, fx, prov)

	// Delegation 2 (planner, dlg-planner-01) runs to completion while coder 1
	// is still held.
	runDelegationOK(t, fx, `{"agent":"planner","task":"plan the work"}`)

	// Release coder 1 and let it finish.
	close(gate)
	if err := <-coderOneDone; err != nil {
		t.Fatalf("coder delegate_to: %v", err)
	}
	fx.drainDelegationEvents(t)

	// Delegation 3: a SECOND coder (dlg-coder-02) — the pooled coder agent is
	// reused, but the stream routes under the new id.
	prov.ReplyText("coder-model", "CODER-REPLY-TWO")
	runDelegationOK(t, fx, `{"agent":"coder","task":"build feature two"}`)

	// Delegation 4: scripted provider-400 — the delegation fails before any
	// chunk streams (the previously invisible case, bug-2). The error text is
	// a plain non-retryable 400 (NOT the max_tokens/context-overflow class,
	// which is deliberately recoverable via the agent's compress+retry path).
	prov.FailNext("planner-model", errors.New("provider 400: invalid request"))
	runFailingDelegation(t, fx, `{"agent":"planner","task":"plan the impossible"}`)

	// --- Assertions -------------------------------------------------------
	assertDelegationTabs(t, fx)
	assertDelegationIsolation(t, fx)
	assertDelegationTerminals(t, fx)
	assertFailedDelegationCard(t, fx)
	assertMainTranscriptClean(t, fx)
}
