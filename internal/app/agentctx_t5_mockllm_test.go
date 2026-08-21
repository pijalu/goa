// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider/mock"
)

// TestAgentCtx_T5BadgeMockLLM is the T5 MANDATORY mock-LLM validation, driven
// through the REAL delegation stack (DelegateTool → AgentPool → scripted
// provider → ForegroundOrchestrator feed): a background coder completes while
// the user stays on the main tab — its tab's ✱ badge sets; switching to it
// acknowledges the badge and the FOOTER shows that tab's stats.
func TestAgentCtx_T5BadgeMockLLM(t *testing.T) {
	prov := mock.New(t)
	fx := newDelegationMockFixture(t, prov)

	// Background coder delegation held mid-stream; user stays on MAIN.
	gate, done := startHeldCoderDelegation(t, fx, prov)

	// Release the coder and let it complete.
	close(gate)
	if err := <-done; err != nil {
		t.Fatalf("held coder delegation failed: %v", err)
	}
	fx.drainDelegationEvents(t)

	// Still on main: the completed background tab carries unacknowledged
	// activity (the ✱ badge state).
	sc := fx.sc
	var activity, errFlag bool
	sc.engine.ApplySync(func() {
		activity, errFlag = sc.app.subs.agentRegistry.Badges("dlg-coder-01")
	})
	if !activity || errFlag {
		t.Fatalf("completed background delegation badges = (%v, %v), want (true, false)", activity, errFlag)
	}

	// Switch to it: badge acknowledged, footer shows THIS tab's stats.
	sc.engine.ApplySync(func() {
		if !sc.app.switchAgentView("dlg-coder-01") {
			t.Error("switchAgentView(dlg-coder-01) failed")
		}
	})
	sc.engine.RenderNow()

	var stats string
	sc.engine.ApplySync(func() {
		activity, errFlag = sc.app.subs.agentRegistry.Badges("dlg-coder-01")
		stats = sc.app.subs.footer.Data().AgentTabStats
	})
	if activity || errFlag {
		t.Errorf("badges after viewing = (%v, %v), want cleared", activity, errFlag)
	}
	if want := "Coder·dlg-01: completed · "; len(stats) < len(want) || stats[:len(want)] != want {
		t.Errorf("footer AgentTabStats = %q, want prefix %q<number> blocks", stats, want)
	}

	// The transcript really holds the coder's streamed reply (isolation).
	if text := delegationTranscriptText(t, sc, "dlg-coder-01"); !strings.Contains(text, "CODER-REPLY-ONE") {
		t.Errorf("coder transcript missing its reply:\n%s", text)
	}
}

// TestAgentCtx_T5SteerMockLLM is the T5 steering validation on the real
// stack: while a coder delegation is HELD mid-stream and its tab is active,
// input routed through routeSteering lands in THAT delegation's steering
// queue (drained by the delegated agent between rounds), not in the main
// agent's queue.
func TestAgentCtx_T5SteerMockLLM(t *testing.T) {
	prov := mock.New(t)
	fx := newDelegationMockFixture(t, prov)

	gate, done := startHeldCoderDelegation(t, fx, prov)
	t.Cleanup(func() { close(gate) }) // release on exit so the goroutine ends

	sc := fx.sc
	sc.app.subs.inputEditor = sc.editor

	// Drain lifecycle so the status tracker marks dlg-coder-01 running.
	fx.drainDelegationEvents(t)

	// Activate the coder's tab.
	sc.engine.ApplySync(func() {
		if !sc.app.switchAgentView("dlg-coder-01") {
			t.Error("switchAgentView(dlg-coder-01) failed")
		}
	})

	// The prompt names the steer target while the delegation runs.
	waitFor(t, sc.engine, 5*time.Second, func() bool {
		return sc.editor.Title() == "steer coder·dlg-01"
	}, "editor title did not reflect the active delegation")

	// Typing steers the ACTIVE delegation.
	q := fx.orch.BindDelegationSteering("dlg-coder-01")
	consumed := sc.app.routeSteering(sc.engine, sc.chat, "also add tests")
	if !consumed {
		t.Fatal("input on a running delegation tab must be consumed as steering")
	}
	if got := q.Drain(); len(got) != 1 || got[0] != "also add tests" {
		t.Fatalf("delegation steering queue = %v, want [also add tests]", got)
	}

	_ = done // the held run finishes via the Cleanup gate release
}
