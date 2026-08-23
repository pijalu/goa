// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/mock"
	"github.com/pijalu/goa/tui/agentctx"
)

// enableReplay turns the T3 scrollback-replay gate ON for the scenario and
// creates the ReplayRunner against the scenario engine's compositor, mirroring
// createTUIComponents (internal/app/tui.go). It returns the runner so the test
// can drive the watermark hand-back synchronously.
func enableReplay(sc *uiScenario) *agentctx.ReplayRunner {
	sc.tb.Helper()
	tru := true
	sc.app.subs.cfg.Features.MultiAgentScrollbackReplay = &tru
	runner := agentctx.NewReplayRunner(sc.engine.Compositor(), 0)
	sc.app.subs.replayRunner = runner
	sc.tb.Cleanup(func() {
		runner.Close()
		sc.app.subs.replayRunner = nil
	})
	return runner
}

// drainReplay applies every pending runner result on the command loop (the
// test-local equivalent of runReplayResultReader). It blocks until at least
// one result arrives (a scheduled replay must always produce exactly one),
// then applies any immediately-available follow-ups. This keeps the watermark
// hand-back deterministic without spinning the production reader goroutine.
func drainReplay(t *testing.T, sc *uiScenario, runner *agentctx.ReplayRunner) {
	t.Helper()
	res, ok := <-runner.Results()
	if !ok {
		t.Fatal("replay results channel closed unexpectedly")
	}
	sc.engine.ApplySync(func() { sc.app.applyReplayResult(res) })
	// Apply any coalesced follow-ups that already completed (non-blocking).
	for {
		select {
		case r := <-runner.Results():
			sc.engine.ApplySync(func() { sc.app.applyReplayResult(r) })
		default:
			return
		}
	}
}

// scrollEmitCount counts how many times a row carrying marker was SCROLLED
// into the terminal scrollback (committed) — as opposed to repainted in place
// in the visible window. Both forms erase the line with EL (\x1b[2K) before
// writing the content; they differ in the cursor motion just before the EL:
//
//   - scroll emission: "... \r \x1b[2K <content>" — a carriage return (the row
//     is written at the scroll region's current line while advancing with
//     line-feeds; the row then scrolls off into scrollback).
//   - in-place repaint: "\x1b[<row>;1H \x1b[2K <content>" — an absolute CUP to
//     a fixed screen row (the visible window repainting a row that stays on
//     screen). No CR precedes the EL.
//
// A committed scrollback row must be scroll-emitted EXACTLY once across any
// amount of tab churn: re-scrolling it is the corruption this regression
// guards against. In-place repaints of a visible row are normal and are NOT
// counted here. The content is indented/padded, so we locate the EL that
// erases the marker's line and inspect the byte before it.
func scrollEmitCount(sc *uiScenario, marker string) int {
	sc.tb.Helper()
	full := strings.Join(sc.term.writes, "")
	count := 0
	idx := 0
	for {
		j := strings.Index(full[idx:], marker)
		if j < 0 {
			break
		}
		j += idx
		idx = j + len(marker)
		// Find the EL (\x1b[2K) that erased this line, scanning back a few
		// bytes (the content may be indented between the EL and the marker).
		lo := j - 8
		if lo < 0 {
			lo = 0
		}
		ctx := full[lo:j]
		el := strings.LastIndex(ctx, "\x1b[2K")
		if el < 0 {
			continue // no erase-line just before: not a fresh row write
		}
		before := ctx[:el]
		// Scroll form has a CR as the last cursor-motion byte before the EL;
		// the repaint form ends in ";1H" (absolute CUP). A leading "\n" (the
		// scroll-advance line-feed) before the CR also marks the scroll form.
		if strings.HasSuffix(before, "\r") || strings.HasSuffix(before, "\n\r") {
			count++
		}
	}
	return count
}

// fillRows appends n distinctly-marked assistant rows to view via ApplySync and
// returns the markers. Prefix keeps each agent's rows in a unique namespace.
func fillRows(sc *uiScenario, view interface{ AddAssistantMessage(string) }, prefix string, n int) []string {
	markers := make([]string, 0, n)
	sc.engine.ApplySync(func() {
		for i := 0; i < n; i++ {
			m := prefix + padIdx(i)
			markers = append(markers, m)
			view.AddAssistantMessage(m)
		}
	})
	return markers
}

// assertScrollEmittedOnce asserts every marker was scroll-emitted at most once
// (the no-corruption invariant) and returns how many were emitted exactly once.
func assertScrollEmittedOnce(t *testing.T, sc *uiScenario, markers []string) int {
	t.Helper()
	once := 0
	for _, m := range markers {
		n := scrollEmitCount(sc, m)
		if n > 1 {
			t.Errorf("row %q scroll-emitted %d times, want <= 1 (scrollback corruption/dup)", m, n)
		}
		if n == 1 {
			once++
		}
	}
	return once
}

// replaySwitch returns a function that switches the active view and, when a
// scrollback replay was scheduled, synchronously drains the watermark hand-back
// before rendering the resume frame. Mirrors the production reader goroutine.
func replaySwitch(t *testing.T, sc *uiScenario, runner *agentctx.ReplayRunner) func(id string) {
	t.Helper()
	return func(id string) {
		t.Helper()
		var scheduled bool
		sc.engine.ApplySync(func() {
			if !sc.app.switchAgentView(id) {
				t.Errorf("switchAgentView(%s) failed", id)
			}
			scheduled = sc.engine.ReplaySuppressed()
		})
		if scheduled {
			drainReplay(t, sc, runner)
		}
		sc.engine.RenderNow()
	}
}

// TestAgentCtx_ReplayFilmstripCorruption is the MANDATORY T3 corruption
// regression: agent A commits a tall tool box (multi-row, > window height so
// part of it scrolls to scrollback); agent B streams heavily; the user churns
// A→B→A→B. With the replay gate ON, A's committed rows must remain byte-stable
// — each is emitted into the terminal scrollback EXACTLY once across the whole
// churn (no duplicate scrollback, no loss), and the visible window always shows
// only the active agent.
func TestAgentCtx_ReplayFilmstripCorruption(t *testing.T) {
	sc := newUIScenario(t, 100, 24)
	runner := enableReplay(sc)
	coder := addScenarioAgent(sc, "dlg-coder-03")
	switchTo := replaySwitch(t, sc, runner)

	// Phase 1: agent A (main) commits a TALL tool box while active (enough
	// rows that the committed prefix exceeds the 24-row window and scrolls).
	const aRows = 40
	aMarkers := fillRows(sc, sc.chat, "A-COMMITTED-ROW-", aRows)
	sc.engine.RenderNow()
	captureStep(sc, "a-commits-tall-toolbox")
	if assertScrollEmittedOnce(t, sc, aMarkers) == 0 {
		t.Fatal("tall A tool box produced no scrollback; test setup does not exercise the committed prefix")
	}

	// Phase 2: agent B accumulates a HEAVY backlog while inactive.
	const bRows = 60
	bMarkers := fillRows(sc, coder.View(), "B-HEAVY-ROW-", bRows)

	// Phase 3: churn A→B→A→B→A. Each switch to B replays B's newly committed
	// backlog; each return to A replays only A's NEW rows (none here), so A's
	// committed prefix must never be re-emitted.
	switchTo("dlg-coder-03")
	captureStep(sc, "switch-to-b-replay")
	assertOnScreen(t, sc, bMarkers[bRows-1])
	assertNotOnScreen(t, sc, aMarkers[0])
	for cycle := 0; cycle < 3; cycle++ {
		switchTo(agentctx.MainAgentID)
		assertOnScreen(t, sc, aMarkers[aRows-1])
		switchTo("dlg-coder-03")
		assertOnScreen(t, sc, bMarkers[bRows-1])
	}
	captureStep(sc, "after-churn")

	// Invariant: no committed row of either agent is ever scroll-emitted twice
	// across the ENTIRE churn — the watermark hand-back (applyReplayResult) is
	// what prevents the re-scroll. And B's backlog really did reach scrollback
	// via the replay (the regression is vacuous if nothing scrolled).
	assertScrollEmittedOnce(t, sc, aMarkers)
	if assertScrollEmittedOnce(t, sc, bMarkers) == 0 {
		t.Error("B's heavy backlog never reached scrollback; replay did not exercise the committed path")
	}
}

// TestAgentCtx_ReplayMockLLM is the MANDATORY T3 mock-LLM validation with the
// replay gate ON: two concurrent real agents (planner held mid-stream via
// Turn.Hold, coder completes), a TALL tool output on the coder, then
// deterministic switches. Each agent's committed rows land in scrollback
// exactly once and switching shows only the active role.
func TestAgentCtx_ReplayMockLLM(t *testing.T) {
	// NOTE: runHeldThenReleased hardcodes the model ids "planner-mock" and
	// "coder-mock", so the scripts must use those exact ids (a mismatched id
	// falls back to the provider's default reply, which has no Hold).
	prov := mock.New(t)
	hold := make(chan struct{})
	plannerTurn := mock.TextTurn("PLANNER-REPLAY-REPLY")
	plannerTurn.Hold = hold
	prov.Script("planner-mock", plannerTurn)
	prov.Script("coder-mock", mock.TextTurn("CODER-REPLAY-REPLY"))

	plannerEvents, coderEvs := runHeldThenReleased(t, prov, hold)

	sc := newUIScenario(t, 100, 24)
	runner := enableReplay(sc)
	plannerTr := addScenarioAgent(sc, "dlg-planner-01")
	coderTr := addScenarioAgent(sc, "dlg-coder-02")
	switchTo := replaySwitch(t, sc, runner)

	// Route each agent's real event stream into its own transcript (both
	// inactive). The coder also commits a TALL tool-output block so a backlog
	// exists to replay.
	applyEventsToTranscript(sc, plannerEvents, plannerTr)
	applyEventsToTranscript(sc, coderEvs, coderTr)
	coderToolMarkers := fillRows(sc, coderTr.View(), "CODER-TOOL-OUT-", 30)
	sc.engine.RenderNow()

	if !snapshotContains(plannerTr.Snapshot(), "PLANNER-REPLAY-REPLY") {
		t.Fatal("planner transcript missing its mock reply")
	}
	if !snapshotContains(coderTr.Snapshot(), "CODER-REPLAY-REPLY") {
		t.Fatal("coder transcript missing its mock reply")
	}

	// Main active: neither sub-agent visible.
	assertNotOnScreen(t, sc, "PLANNER-REPLAY-REPLY", "CODER-REPLAY-REPLY")

	// Switch to the coder: the coder's transcript is TALL (mock reply + 30
	// tool rows), so the reply has scrolled into the replayed scrollback and
	// the LAST tool rows fill the visible window. The planner's reply must
	// never appear; the coder's reply must be in its transcript (replayed to
	// scrollback), and the tail of the tool backlog must be on screen.
	switchTo("dlg-coder-02")
	captureStep(sc, "replay-switch-to-coder")
	assertOnScreen(t, sc, "CODER-TOOL-OUT-"+padIdx(29))
	assertNotOnScreen(t, sc, "PLANNER-REPLAY-REPLY")
	assertTabBar(t, sc, "coder·dlg-02", "[3/3]")
	if !snapshotContains(coderTr.Snapshot(), "CODER-REPLAY-REPLY") {
		t.Error("coder reply must remain in its transcript after the replay switch")
	}

	// Switch to the planner: the planner's transcript is short (just the
	// reply), so it IS visible; the coder's rows must vanish.
	switchTo("dlg-planner-01")
	captureStep(sc, "replay-switch-to-planner")
	assertOnScreen(t, sc, "PLANNER-REPLAY-REPLY")
	assertNotOnScreen(t, sc, "CODER-TOOL-OUT-"+padIdx(29))
	assertTabBar(t, sc, "planner·dlg-01", "[2/3]")

	// Return to the coder: NO committed row is scroll-emitted twice (the
	// watermark hand-back prevents re-scrolling the already-committed prefix).
	// Visible-window rows may be repainted in place — that is normal and is
	// not a scrollback duplicate.
	switchTo("dlg-coder-02")
	assertScrollEmittedOnce(t, sc, coderToolMarkers)
}

// TestAgentCtx_ReplayGateOffKeepsT2 verifies the flag-OFF path is byte-for-byte
// the T2 behavior: no runner is created and a switch repaints in place without
// scheduling a scrollback replay (no suppression, no watermark hand-back).
func TestAgentCtx_ReplayGateOffKeepsT2(t *testing.T) {
	sc := newUIScenario(t, 100, 24)
	// Gate stays OFF (no enableReplay): subs.replayRunner must be nil.
	if sc.app.subs.replayRunner != nil {
		t.Fatal("replay runner must be nil when the gate is OFF")
	}
	coder := addScenarioAgent(sc, "dlg-coder-03")

	sc.engine.ApplySync(func() {
		for i := 0; i < 30; i++ {
			coder.View().AddAssistantMessage("OFF-PATH-ROW-" + padIdx(i))
		}
	})

	sc.engine.ApplySync(func() {
		if !sc.app.switchAgentView("dlg-coder-03") {
			t.Fatal("switchAgentView failed")
		}
	})
	// T2 path: rendering is NOT suppressed (no replay scheduled).
	if sc.engine.ReplaySuppressed() {
		t.Error("gate OFF must not suppress rendering (no replay scheduled)")
	}
	sc.engine.RenderNow()
	assertOnScreen(t, sc, "OFF-PATH-ROW-"+padIdx(29))
}

// padIdx renders i as a zero-padded 4-digit string so row markers are
// prefix-free for substring counting (mirrors the agentctx test helper).
func padIdx(i int) string {
	var buf [4]byte
	for p := 3; p >= 0; p-- {
		buf[p] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[:])
}
