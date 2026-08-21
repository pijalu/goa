// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"testing"

	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/agentic/provider/mock"
	"github.com/pijalu/goa/multiagent"
	"github.com/pijalu/goa/tui/agentctx"
)

// TestSteerActiveDelegation is the T5 steering test: with a RUNNING
// delegation's tab active, submitted input steers THAT delegation id (its
// bound queue, drained by the delegated agent mid-turn); on the main tab the
// same input falls through to the normal paths ("all").
func TestSteerActiveDelegation(t *testing.T) {
	// Real orchestrator so SteerDelegation/BindDelegationSteering are live.
	prov := mock.New(t)
	opts := agenticprovider.StreamOptions{
		RetryPolicy: &agenticprovider.RetryPolicy{Mode: agenticprovider.RetryModeNormal, MaxRetries: 1, Codes: []string{}},
	}
	pool := multiagent.NewAgentPool(prov.Model("default-model"), opts, nil)
	orch := multiagent.NewForegroundOrchestrator(pool)

	sc := newUIScenario(t, 100, 24)
	sc.app.subs.foregroundOrch = orch
	sc.app.subs.inputEditor = sc.editor

	// A running coder delegation whose tab is ACTIVE.
	sc.engine.ApplySync(func() {
		sc.app.ensureDelegationView("dlg-coder-01", "coder")
		sc.app.setDelegationStatus("dlg-coder-01", multiagent.DelegationRunning)
		if !sc.app.switchAgentView("dlg-coder-01") {
			t.Error("switchAgentView(dlg-coder-01) failed")
		}
	})
	q := orch.BindDelegationSteering("dlg-coder-01")
	t.Cleanup(func() { orch.UnbindDelegationSteering("dlg-coder-01") })

	// Typing on the delegation tab consumes the input as delegation steering.
	consumed := sc.app.routeSteering(sc.engine, sc.chat, "focus on the parser bug")
	if !consumed {
		t.Fatal("input on a running delegation tab must be consumed as steering")
	}
	got := q.Drain()
	if len(got) != 1 || got[0] != "focus on the parser bug" {
		t.Fatalf("delegation queue = %v, want the typed text", got)
	}

	// The prompt label names the steer target.
	if title := sc.editor.Title(); title != "steer coder·dlg-01" {
		t.Errorf("editor title = %q, want %q", title, "steer coder·dlg-01")
	}

	// Back on main: the same text falls through (not consumed here — no
	// busy main agent in this scenario → dispatched as a normal message).
	sc.engine.ApplySync(func() {
		sc.app.switchAgentView(agentctx.MainAgentID)
	})
	if consumed := sc.app.routeSteering(sc.engine, sc.chat, "hello main"); consumed {
		t.Error("input on main must not be routed to a delegation queue")
	}
	if n := q.Len(); n != 0 {
		t.Errorf("delegation queue grew from main-tab input: %d messages", n)
	}
}

// TestSteerActiveDelegation_NotRunning pins the fallback rule: typing on a
// COMPLETED delegation's tab must NOT vanish into its dead queue.
func TestSteerActiveDelegation_NotRunning(t *testing.T) {
	prov := mock.New(t)
	pool := multiagent.NewAgentPool(prov.Model("default-model"), agenticprovider.StreamOptions{}, nil)
	orch := multiagent.NewForegroundOrchestrator(pool)

	sc := newUIScenario(t, 100, 24)
	sc.app.subs.foregroundOrch = orch

	sc.engine.ApplySync(func() {
		sc.app.ensureDelegationView("dlg-coder-02", "coder")
		sc.app.setDelegationStatus("dlg-coder-02", multiagent.DelegationCompleted)
		if !sc.app.switchAgentView("dlg-coder-02") {
			t.Error("switchAgentView(dlg-coder-02) failed")
		}
	})
	q := orch.BindDelegationSteering("dlg-coder-02")
	t.Cleanup(func() { orch.UnbindDelegationSteering("dlg-coder-02") })

	if consumed := sc.app.routeSteering(sc.engine, sc.chat, "anyone there?"); consumed {
		t.Error("completed delegation tab must fall back to normal dispatch")
	}
	if n := q.Len(); n != 0 {
		t.Errorf("dead delegation queue received steering: %d messages", n)
	}
}
