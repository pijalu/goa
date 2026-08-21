// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/multiagent"
)

// feedDelegation drives one delegation-source OrchestratorMessage through the
// exact T4 routing entry point on the engine command loop (production: the
// forwarder applies via a.apply), then captures a filmstrip frame.
func feedDelegation(sc *uiScenario, label string, msg multiagent.OrchestratorMessage) {
	sc.tb.Helper()
	sc.engine.ApplySync(func() {
		sc.app.handleOrchestratorStreamMsg(msg, newStreamForwarder())
	})
	sc.engine.RenderNow()
	sc.film.Capture(label, sc.engine.AgentFrame(), sc.status.Text())
}

// TestAgentCtx_DelegationFilmstrip is the T4 mandatory filmstrip: a delegation
// spawns a visible tab the moment it is created (delegation_state running —
// the PENDING→RUNNING edge), streams under RUNNING into its own transcript
// (the main screen is undisturbed), and is marked terminal on completion; the
// screen shows only the ACTIVE delegation's transcript after a switch.
func TestAgentCtx_DelegationFilmstrip(t *testing.T) {
	sc := newUIScenario(t, 100, 24)

	// Main agent has content on screen first.
	sc.engine.ApplySync(func() {
		sc.chat.AddUserMessage("MAIN-QUESTION")
		sc.chat.AddAssistantMessage("MAIN-ANSWER")
	})
	sc.engine.RenderNow()
	sc.film.Capture("main-only", sc.engine.AgentFrame(), sc.status.Text())
	assertOnScreen(t, sc, "MAIN-QUESTION", "MAIN-ANSWER")

	// PENDING→RUNNING: the delegation is created — a tab must appear NOW,
	// before any chunk streams (bug-2: delegations are visible from birth).
	feedDelegation(sc, "delegation-created", multiagent.OrchestratorMessage{
		From: "coder", To: "delegation", Kind: "delegation_state",
		Content: "running|", DelegationID: "dlg-coder-01",
	})
	assertTabBar(t, sc, "coder·dlg-01", "[1/2]")
	assertOnScreen(t, sc, "MAIN-QUESTION") // main view undisturbed

	// RUNNING: chunks stream into the delegation's own transcript — pure data
	// while its tab is inactive; the visible screen must NOT show them.
	feedDelegation(sc, "delegation-streams", multiagent.OrchestratorMessage{
		From: "coder", To: "stream_chunk", Kind: "content",
		Content: "CODER-STREAMING-OUTPUT", DelegationID: "dlg-coder-01",
	})
	assertNotOnScreen(t, sc, "CODER-STREAMING-OUTPUT")
	assertOnScreen(t, sc, "MAIN-QUESTION", "MAIN-ANSWER")

	// Terminal: completed. The tab is marked (registry badge state) and the
	// delegation transcript gains a terminal marker.
	feedDelegation(sc, "delegation-completed", multiagent.OrchestratorMessage{
		From: "coder", To: "delegation", Kind: "delegation_state",
		Content: "completed|", DelegationID: "dlg-coder-01",
	})
	activity, errFlag := sc.app.subs.agentRegistry.Badges("dlg-coder-01")
	if !activity || errFlag {
		t.Errorf("completed delegation badges = (activity=%v, err=%v), want (true, false)", activity, errFlag)
	}

	// Switch to the delegation tab: ONLY its transcript is on screen, with
	// the streamed content and the terminal marker. (The tab-bar assertion
	// keys on the [2/2] indicator — unique to the strip — because the
	// transcript's own labeled blocks also contain the tab label.)
	sc.engine.ApplySync(func() {
		if !sc.app.switchAgentView("dlg-coder-01") {
			t.Error("switchAgentView(dlg-coder-01) failed")
		}
	})
	captureStep(sc, "view-delegation")
	assertOnScreen(t, sc, "CODER-STREAMING-OUTPUT", "completed")
	assertNotOnScreen(t, sc, "MAIN-QUESTION", "MAIN-ANSWER")
	assertDelegationTabBar(t, sc, "coder·dlg-01", "[2/2]")

	sc.tb.Logf("filmstrip:\n%s", sc.filmstrip().Render())
}

// assertDelegationTabBar asserts the tab strip row (identified by its
// right-justified [n/total] indicator, which only the strip renders) shows
// the given tab label.
func assertDelegationTabBar(t *testing.T, sc *uiScenario, label, indicator string) {
	t.Helper()
	rows := visibleRows(sc)
	idx := rowOf(rows, indicator)
	if idx < 0 {
		t.Fatalf("tab strip not rendered (indicator %q):\n%s", indicator, strings.Join(rows, "\n"))
	}
	if !strings.Contains(rows[idx], label) {
		t.Errorf("tab strip row = %q, want label %q", rows[idx], label)
	}
}

// TestAgentCtx_DelegationFailureFilmstrip is the T4 bug-2 filmstrip: a FAILED
// delegation leaves a marked tab whose transcript holds the error card, and
// the failure does not corrupt the main view.
func TestAgentCtx_DelegationFailureFilmstrip(t *testing.T) {
	sc := newUIScenario(t, 100, 24)

	sc.engine.ApplySync(func() {
		sc.chat.AddUserMessage("MAIN-QUESTION")
	})
	sc.engine.RenderNow()

	// A coder delegation starts, streams a partial line, then the provider
	// fails (scripted-400 class): the tab must be marked and the error card
	// must land in the delegation's transcript.
	feedDelegation(sc, "delegation-created", multiagent.OrchestratorMessage{
		From: "coder", To: "delegation", Kind: "delegation_state",
		Content: "running|", DelegationID: "dlg-coder-01",
	})
	assertTabBar(t, sc, "coder·dlg-01", "[1/2]")

	feedDelegation(sc, "delegation-failed", multiagent.OrchestratorMessage{
		From: "coder", To: "delegation", Kind: "delegation_state",
		Content: "failed|provider 400: max_output_tokens exceeded", DelegationID: "dlg-coder-01",
	})

	// The tab is marked failed in the registry (the ▲ badge state).
	if _, errFlag := sc.app.subs.agentRegistry.Badges("dlg-coder-01"); !errFlag {
		t.Error("failed delegation did not mark its tab (registry error state)")
	}
	// Main view intact, no error card leaked into it.
	assertOnScreen(t, sc, "MAIN-QUESTION")
	assertNotOnScreen(t, sc, "FAILED")

	// Opening the tab shows the error card with the failure detail.
	sc.engine.ApplySync(func() {
		if !sc.app.switchAgentView("dlg-coder-01") {
			t.Error("switchAgentView(dlg-coder-01) failed")
		}
	})
	captureStep(sc, "view-failed-delegation")
	assertOnScreen(t, sc, "FAILED", "provider 400: max_output_tokens exceeded")
	assertNotOnScreen(t, sc, "MAIN-QUESTION")
}
