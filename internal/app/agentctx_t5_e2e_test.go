//go:build e2e

// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Local-LM end-to-end validation of the T5 navigation/attribution surface:
// tab cycling with the hotkey handlers, steering the ACTIVE delegation, and
// per-tab footer stats on a real run. Requires an OpenAI-compatible server at
// http://localhost:1234 (LMStudio / llama.cpp). Skips when unreachable.
//
// Run: go test -count=1 -tags e2e -run TestE2E_MultiAgentT5 ./internal/app/
package app

import (
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/multiagent"
	"github.com/pijalu/goa/tui"
)

// t5Drain returns the event drain loop for the given orchestrator feed: every
// buffered message goes through the exact T4 routing entry point.
func t5Drain(sc *uiScenario, events <-chan multiagent.OrchestratorMessage) func() {
	return func() {
		for {
			select {
			case msg := <-events:
				sc.app.handleOrchestratorStreamMsg(msg, newStreamForwarder())
			default:
				return
			}
		}
	}
}

// TestE2E_MultiAgentT5BadgesAndFooter is the Local-LM T5 badge/footer e2e:
// two real coder delegations complete while the user stays on main (both tabs
// badge ✱); handleCycleAgentTab cycles to a delegation, the footer shows ITS
// stats and viewing acknowledges the badge.
func TestE2E_MultiAgentT5BadgesAndFooter(t *testing.T) {
	skipIfNoReplayLLM(t)

	sc := newUIScenario(t, 100, 24)
	sc.app.subs.inputEditor = sc.editor
	orch, tool := newE2EDelegationStack(t, sc)
	drain := t5Drain(sc, orch.Events())

	// Two REAL delegations against the live LM; user stays on main.
	for _, task := range []string{"Reply with exactly: LIVE-T5-CODER-ONE", "Reply with exactly: LIVE-T5-CODER-TWO"} {
		if _, err := tool.Execute(`{"agent":"coder","task":"` + task + `"}`); err != nil {
			t.Fatalf("live delegate_to: %v", err)
		}
	}
	drain()
	assertT5BackgroundBadges(t, sc)

	// Cycle with the hotkey handler: lands on dlg-coder-01.
	sc.app.handleCycleAgentTab(1)
	if id, _ := sc.app.subs.agentRegistry.Active(); id != "dlg-coder-01" {
		t.Fatalf("after cycle: active = %q, want dlg-coder-01", id)
	}

	assertT5ActiveTabChrome(t, sc)
}

// assertT5BackgroundBadges checks both completed background tabs carry the
// unacknowledged activity badge state.
func assertT5BackgroundBadges(t *testing.T, sc *uiScenario) {
	t.Helper()
	sc.engine.ApplySync(func() {
		for _, id := range []string{"dlg-coder-01", "dlg-coder-02"} {
			if activity, errFlag := sc.app.subs.agentRegistry.Badges(id); !activity || errFlag {
				t.Errorf("%s badges = (%v, %v), want (true, false)", id, activity, errFlag)
			}
		}
	})
}

// assertT5ActiveTabChrome checks the active delegation tab's chrome: footer
// stat line present, badge acknowledged.
func assertT5ActiveTabChrome(t *testing.T, sc *uiScenario) {
	t.Helper()
	sc.engine.RenderNow()
	var stats string
	var activity bool
	sc.engine.ApplySync(func() {
		stats = sc.app.subs.footer.Data().AgentTabStats
		activity, _ = sc.app.subs.agentRegistry.Badges("dlg-coder-01")
	})
	if activity {
		t.Error("badge must be acknowledged after viewing")
	}
	if want := "Coder·dlg-01: completed · "; len(stats) < len(want) || stats[:len(want)] != want {
		t.Errorf("footer AgentTabStats = %q, want prefix %q", stats, want)
	}
}

// TestE2E_MultiAgentT5Steering is the Local-LM T5 steering e2e: on a
// COMPLETED delegation's tab input falls through (not consumed); on a LIVE
// running delegation's tab input lands in that delegation's steering queue.
func TestE2E_MultiAgentT5Steering(t *testing.T) {
	skipIfNoReplayLLM(t)

	sc := newUIScenario(t, 100, 24)
	sc.app.subs.inputEditor = sc.editor
	orch, tool := newE2EDelegationStack(t, sc)
	drain := t5Drain(sc, orch.Events())

	// Fallback rule on a completed delegation.
	if _, err := tool.Execute(`{"agent":"coder","task":"Reply with exactly: LIVE-T5-DONE"}`); err != nil {
		t.Fatalf("live delegate_to: %v", err)
	}
	drain()
	sc.engine.ApplySync(func() { sc.app.switchAgentView("dlg-coder-01") })
	q := orch.BindDelegationSteering("dlg-coder-01")
	if consumed := sc.app.routeSteering(sc.engine, sc.chat, "should fall through"); consumed {
		t.Error("input on a COMPLETED delegation tab must not be consumed")
	} else if n := q.Len(); n != 0 {
		t.Errorf("completed delegation queue received %d messages", n)
	}
	orch.UnbindDelegationSteering("dlg-coder-01")

	// Positive steering on a LIVE delegation: start one, wait for its tab,
	// activate it, steer it mid-run.
	done := make(chan error, 1)
	go func() {
		_, err := tool.Execute(`{"agent":"coder","task":"Count slowly from 1 to 3, then reply DONE-T5"}`)
		done <- err
	}()
	waitFor(t, sc.engine, 30*time.Second, func() bool {
		drain()
		_, ok := sc.app.subs.agentRegistry.Get("dlg-coder-02")
		return ok
	}, "second delegation tab did not appear")

	sc.engine.ApplySync(func() { sc.app.switchAgentView("dlg-coder-02") })
	waitFor(t, sc.engine, 10*time.Second, func() bool {
		return sc.editor.Title() == "steer coder·dlg-02"
	}, "prompt did not name the running delegation")

	liveQ := orch.BindDelegationSteering("dlg-coder-02")
	if consumed := sc.app.routeSteering(sc.engine, sc.chat, "be terse"); !consumed {
		t.Error("input on a RUNNING delegation tab must be consumed")
	} else if got := liveQ.Drain(); len(got) != 1 || got[0] != "be terse" {
		t.Errorf("live delegation queue = %v", got)
	}

	if err := <-done; err != nil {
		t.Fatalf("live delegation failed: %v", err)
	}
	drain()
}
