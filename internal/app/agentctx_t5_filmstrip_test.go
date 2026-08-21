// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/multiagent"
	"github.com/pijalu/goa/tui/agentctx"
)

// TestAgentCtx_T5BadgeAndFooterFilmstrip is the T5 MANDATORY filmstrip: the
// ✱ badge appears on the correct tab while a background delegation works (and
// ▲ after a failure), viewing the tab acknowledges the badge, and the footer
// reflects the ACTIVE tab's stats after a switch.
func TestAgentCtx_T5BadgeAndFooterFilmstrip(t *testing.T) {
	sc := newUIScenario(t, 100, 24)
	sc.app.subs.inputEditor = sc.editor

	// Main agent content on screen; main is the active tab.
	sc.engine.ApplySync(func() {
		sc.chat.AddUserMessage("MAIN-QUESTION")
	})
	sc.engine.RenderNow()
	sc.film.Capture("main-only", sc.engine.AgentFrame(), sc.status.Text())

	// A coder delegation runs in the background and completes while the user
	// stays on main: its tab must show the ✱ badge.
	feedDelegation(sc, "coder-created", multiagent.OrchestratorMessage{
		From: "coder", To: "delegation", Kind: "delegation_state",
		Content: "running|", DelegationID: "dlg-coder-01",
	})
	feedDelegation(sc, "coder-streams", multiagent.OrchestratorMessage{
		From: "coder", To: "stream_chunk", Kind: "content",
		Content: "CODER-WORK-OUTPUT", DelegationID: "dlg-coder-01",
	})
	feedDelegation(sc, "coder-completed", multiagent.OrchestratorMessage{
		From: "coder", To: "delegation", Kind: "delegation_state",
		Content: "completed|", DelegationID: "dlg-coder-01",
	})

	rows := visibleRows(sc)
	tabIdx := rowOf(rows, "[1/2]")
	if tabIdx < 0 {
		t.Fatalf("tab strip missing:\n%s", strings.Join(rows, "\n"))
	}
	if !strings.Contains(rows[tabIdx], "coder·dlg-01 ✱") {
		t.Errorf("completed background delegation must carry ✱ on its tab: %q", rows[tabIdx])
	}
	captureStep(sc, "badge-activity")

	// A second delegation FAILS: its tab must carry ▲ (error precedence).
	feedDelegation(sc, "planner-failed", multiagent.OrchestratorMessage{
		From: "planner", To: "delegation", Kind: "delegation_state",
		Content: "failed|provider 400: invalid request", DelegationID: "dlg-planner-02",
	})
	rows = visibleRows(sc)
	tabIdx = rowOf(rows, "[1/3]")
	if tabIdx < 0 {
		t.Fatalf("tab strip missing after failure:\n%s", strings.Join(rows, "\n"))
	}
	if !strings.Contains(rows[tabIdx], "planner·dlg-02 ▲") {
		t.Errorf("failed delegation must carry ▲ on its tab: %q", rows[tabIdx])
	}
	captureStep(sc, "badge-error")

	// Switch to the completed coder tab: badge acknowledged (gone), only its
	// transcript on screen, and the FOOTER shows this tab's stats.
	sc.engine.ApplySync(func() {
		if !sc.app.switchAgentView("dlg-coder-01") {
			t.Error("switchAgentView(dlg-coder-01) failed")
		}
	})
	sc.engine.RenderNow()
	captureStep(sc, "view-coder")
	assertOnScreen(t, sc, "CODER-WORK-OUTPUT")
	assertNotOnScreen(t, sc, "MAIN-QUESTION")

	rows = visibleRows(sc)
	footerIdx := rowOf(rows, "Coder·dlg-01:")
	if footerIdx < 0 {
		t.Fatalf("footer must show the active tab's stats:\n%s", strings.Join(rows, "\n"))
	}
	if !strings.Contains(rows[footerIdx], "completed") || !strings.Contains(rows[footerIdx], "blocks") {
		t.Errorf("footer stat line incomplete: %q", rows[footerIdx])
	}

	// Back on main: the per-tab footer line clears.
	sc.engine.ApplySync(func() {
		sc.app.switchAgentView(agentctx.MainAgentID)
	})
	sc.engine.RenderNow()
	for _, r := range visibleRows(sc) {
		if strings.Contains(r, "Coder·dlg-01:") {
			t.Errorf("per-tab footer line must clear on main: %q", r)
		}
	}
	assertOnScreen(t, sc, "MAIN-QUESTION")
}

// TestAgentCtx_T5ReplayCommandScrollCount drives /agent:replay through the
// production host wiring with the T3 gate ON: the active tab's FULL history
// is re-emitted into scrollback exactly once (spy-terminal scroll count).
func TestAgentCtx_T5ReplayCommandScrollCount(t *testing.T) {
	sc := newUIScenario(t, 100, 24)
	runner := enableReplay(sc)
	coder := addScenarioAgent(sc, "dlg-coder-01")

	// A tall committed backlog (well beyond the 24-row window).
	const n = 30
	markers := fillRows(sc, coder.View(), "REPLAYROW-", n)

	// First switch replays the backlog exactly once (T3 path). Only the
	// committed prefix [0, naturalVt) reaches scrollback — the visible tail
	// stays on screen.
	replaySwitch(t, sc, runner)("dlg-coder-01")
	before := scrollEmitCount(sc, "REPLAYROW-")
	if before == 0 {
		t.Fatal("no backlog reached scrollback; test setup does not exercise replay")
	}

	// /agent:replay through the production wiring: the FULL committed
	// history again, exactly once more.
	cmd := sc.app.newAgentCommand()
	label, started := cmd.ReplayActiveTab()
	if !started || label != "coder·dlg-01" {
		t.Fatalf("ReplayActiveTab = (%q, %v), want started replay of coder·dlg-01", label, started)
	}
	drainReplay(t, sc, runner)
	after := scrollEmitCount(sc, "REPLAYROW-")
	if after-before != before {
		t.Errorf("deliberate replay emitted %d rows, want exactly the committed history (%d)", after-before, before)
	}

	// The final frame shows the tail of the replayed transcript.
	assertOnScreen(t, sc, markers[n-1])
}
