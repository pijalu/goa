// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/provider/mock"
	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/tui"
	"github.com/pijalu/goa/tui/agentctx"
)

// addScenarioAgent registers a second agent view in the scenario's registry
// (inactive: it never touches the screen) and returns its transcript.
func addScenarioAgent(sc *uiScenario, id string) *agentctx.AgentTranscript {
	sc.tb.Helper()
	tr := agentctx.NewAgentTranscript(id)
	sc.app.subs.agentRegistry.Add(id, &agentctx.AgentView{Transcript: tr, Compositor: tr.Compositor()})
	return tr
}

// captureStep renders the current state synchronously and records a labeled
// filmstrip frame — the switch steps of the scenario.
func captureStep(sc *uiScenario, label string) {
	sc.tb.Helper()
	sc.engine.RenderNow()
	sc.film.Capture(label, sc.engine.AgentFrame(), sc.status.Text())
}

// visibleRows splits the engine's visible text into ANSI-stripped rows.
func visibleRows(sc *uiScenario) []string {
	rows := strings.Split(sc.engine.VisibleText(), "\n")
	for i := range rows {
		rows[i] = ansi.Strip(rows[i])
	}
	return rows
}

// rowOf returns the index of the first row containing text, or -1.
func rowOf(rows []string, text string) int {
	for i, r := range rows {
		if strings.Contains(r, text) {
			return i
		}
	}
	return -1
}

// TestAgentCtx_SwitchFilmstrip is the T2 mandatory filmstrip: two agents
// accumulate transcript rows; switching tabs mounts the target transcript with
// a full visible-window repaint so the screen shows ONLY the active agent's
// lines, the tab bar reflects the active tab immediately above the input
// editor, and the inactive agent's rows never appear.
func TestAgentCtx_SwitchFilmstrip(t *testing.T) {
	sc := newUIScenario(t, 100, 24)
	coder := addScenarioAgent(sc, "dlg-coder-03")

	// Main agent streams through the real event path while main is active.
	// (The user message carries replay metadata: live user content is normally
	// echoed by the submit handler, so the stream handler suppresses it.)
	sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateThinking})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.User, Text: "MAIN-USER-QUESTION", Metadata: map[string]string{"replay": "true"}})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.Assistant, Text: "MAIN-REPLY-CHUNK", IsDelta: true})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventEnd})

	// The coder delegation accumulates rows as pure data (inactive: direct
	// transcript writes, no screen emission).
	sc.engine.ApplySync(func() {
		coder.View().AddUserMessage("CODER-TASK")
		coder.View().AddAssistantMessage("CODER-REPLY-ONE")
	})
	captureStep(sc, "coder-accumulates-inactive")

	// Frame 1: main active — main's rows visible, coder's absent.
	assertOnScreen(t, sc, "MAIN-USER-QUESTION", "MAIN-REPLY-CHUNK")
	assertNotOnScreen(t, sc, "CODER-TASK", "CODER-REPLY-ONE")
	assertTabBar(t, sc, "coder·dlg-03", "[1/2]")

	// Switch to the coder tab: full visible-window repaint mounts its transcript.
	sc.engine.ApplySync(func() {
		if !sc.app.switchAgentView("dlg-coder-03") {
			t.Error("switchAgentView(dlg-coder-03) failed")
		}
	})
	captureStep(sc, "switch-to-coder")

	assertOnScreen(t, sc, "CODER-TASK", "CODER-REPLY-ONE")
	assertNotOnScreen(t, sc, "MAIN-USER-QUESTION", "MAIN-REPLY-CHUNK")
	assertTabBar(t, sc, "coder·dlg-03", "[2/2]")
	assertMountedView(t, sc, coder)

	// While viewing the coder, the MAIN agent keeps streaming (real event
	// path): its rows accumulate in its transcript but must NOT appear.
	sc.apply(&agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.Assistant, Text: "MAIN-WHILE-AWAY", IsDelta: true})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventEnd})
	assertNotOnScreen(t, sc, "MAIN-WHILE-AWAY")
	if got := sc.chat.Snapshot(); !snapshotContains(got, "MAIN-WHILE-AWAY") {
		t.Error("main transcript did not accumulate the row while inactive")
	}

	// Cycle back to main: its full window (including rows accumulated while
	// inactive) repaints; the coder's rows vanish.
	sc.engine.ApplySync(func() { sc.app.cycleAgentView(1) })
	captureStep(sc, "cycle-back-to-main")
	assertOnScreen(t, sc, "MAIN-USER-QUESTION", "MAIN-WHILE-AWAY")
	assertNotOnScreen(t, sc, "CODER-TASK", "CODER-REPLY-ONE")
	assertTabBar(t, sc, "coder·dlg-03", "[1/2]")
}

// assertOnScreen asserts every marker is on the visible screen.
func assertOnScreen(t *testing.T, sc *uiScenario, markers ...string) {
	t.Helper()
	rows := visibleRows(sc)
	for _, m := range markers {
		if rowOf(rows, m) < 0 {
			t.Errorf("%q missing from the visible screen:\n%s", m, strings.Join(rows, "\n"))
		}
	}
}

// assertNotOnScreen asserts no marker is on the visible screen.
func assertNotOnScreen(t *testing.T, sc *uiScenario, markers ...string) {
	t.Helper()
	rows := visibleRows(sc)
	for _, m := range markers {
		if rowOf(rows, m) >= 0 {
			t.Errorf("%q must not be on the visible screen:\n%s", m, strings.Join(rows, "\n"))
		}
	}
}

// assertTabBar asserts the tab strip renders (immediately above the input
// editor) with the given tab label and [n/total] indicator.
func assertTabBar(t *testing.T, sc *uiScenario, label, indicator string) {
	t.Helper()
	rows := visibleRows(sc)
	tabRow := rowOf(rows, label)
	if tabRow < 0 {
		t.Fatalf("tab bar not rendered (label %q):\n%s", label, strings.Join(rows, "\n"))
	}
	if !strings.Contains(rows[tabRow], indicator) {
		t.Errorf("tab bar = %q, want indicator %s", rows[tabRow], indicator)
	}
	if !strings.Contains(rows[tabRow+1], "─") {
		t.Errorf("tab bar must sit immediately above the input editor; row below = %q", rows[tabRow+1])
	}
}

// assertMountedView asserts the registry's active view is tr's transcript and
// it is mounted, while the app's chat binding stays on the main transcript.
func assertMountedView(t *testing.T, sc *uiScenario, tr *agentctx.AgentTranscript) {
	t.Helper()
	if _, v := sc.app.subs.agentRegistry.Active(); v.Transcript.View() != tr.View() || !tr.Mounted() {
		t.Error("target transcript not the mounted active view after switch")
	}
	if tr.ID() != agentctx.MainAgentID && sc.chat == tr.View() {
		t.Error("subs.chat binding must stay on the MAIN transcript (routing is per-agent)")
	}
}

// snapshotContains reports whether any transcript entry carries the text.
func snapshotContains(entries []tui.MessageData, text string) bool {
	for _, e := range entries {
		if strings.Contains(e.Text, text) {
			return true
		}
	}
	return false
}

// TestAgentCtx_SwitchKeepsSingleOwner guards the R1 invariant: switching must
// not corrupt the registry/tree when the inactive agent kept growing — the
// inactive transcript stays pure data until mounted.
func TestAgentCtx_InactiveAccumulatesOffscreen(t *testing.T) {
	sc := newUIScenario(t, 100, 24)
	coder := addScenarioAgent(sc, "dlg-coder-03")

	sc.engine.ApplySync(func() {
		for i := 0; i < 30; i++ {
			coder.View().AddAssistantMessage("coder-row")
		}
	})
	captureStep(sc, "coder-grows-inactive")

	if coder.Mounted() {
		t.Error("inactive transcript must not be mounted")
	}
	if coder.Len() != 30 {
		t.Errorf("coder Len = %d, want 30 (pure-data accumulation)", coder.Len())
	}
	rows := visibleRows(sc)
	if rowOf(rows, "coder-row") >= 0 {
		t.Error("inactive agent rows must never reach the screen")
	}
	// The main transcript (empty here) is still the only mounted view.
	if id, _ := sc.app.subs.agentRegistry.Active(); id != agentctx.MainAgentID {
		t.Errorf("active = %q, want main", id)
	}
}

// applyEventsToTranscript replays a captured agent event stream through the
// app's REAL event handler with the chat binding aimed at the given
// transcript's viewport. Streams are applied sequentially (one complete agent
// stream at a time) so the app-global stream accumulator stays consistent —
// this mirrors T4's command-loop serialization, not the final routing.
func applyEventsToTranscript(sc *uiScenario, events []agentic.OutputEvent, tr *agentctx.AgentTranscript) {
	sc.tb.Helper()
	for i := range events {
		ev := events[i]
		sc.engine.ApplySync(func() {
			saved := sc.app.subs.chat
			sc.app.subs.chat = tr.View()
			sc.app.handleAgentOutputEvent(&ev)
			sc.app.subs.chat = saved
		})
	}
	sc.engine.RenderNow()
}

// TestAgentCtx_SwitchMockLLM is the T2 mandatory mock-LLM validation: two real
// agents (planner + coder) run CONCURRENTLY against the scripted mock provider
// — the planner is held mid-stream via Turn.Hold while the coder completes —
// and each agent's events land ONLY in its own transcript. The visible tab
// shows only the active role; switching swaps the visible window wholesale.
func TestAgentCtx_SwitchMockLLM(t *testing.T) {
	prov := mock.New(t)
	hold := make(chan struct{})
	plannerTurn := mock.TextTurn("PLANNER-MOCK-REPLY")
	plannerTurn.Hold = hold
	prov.Script("planner-mock", plannerTurn)
	prov.Script("coder-mock", mock.TextTurn("CODER-MOCK-REPLY"))

	plannerEvents, coderEvs := runHeldThenReleased(t, prov, hold)

	// Route each agent's stream into its own transcript (both inactive).
	sc := newUIScenario(t, 100, 24)
	plannerTr := addScenarioAgent(sc, "dlg-planner-01")
	coderTr := addScenarioAgent(sc, "dlg-coder-02")
	applyEventsToTranscript(sc, plannerEvents, plannerTr)
	applyEventsToTranscript(sc, coderEvs, coderTr)

	if !snapshotContains(plannerTr.Snapshot(), "PLANNER-MOCK-REPLY") {
		t.Fatal("planner transcript missing its mock reply")
	}
	if !snapshotContains(coderTr.Snapshot(), "CODER-MOCK-REPLY") {
		t.Fatal("coder transcript missing its mock reply")
	}

	// Main tab active: neither sub-agent role is visible.
	assertNotOnScreen(t, sc, "PLANNER-MOCK-REPLY", "CODER-MOCK-REPLY")

	// Switch to the coder: only the coder's stream shows.
	sc.engine.ApplySync(func() { sc.app.switchAgentView("dlg-coder-02") })
	captureStep(sc, "mock-switch-to-coder")
	assertOnScreen(t, sc, "CODER-MOCK-REPLY")
	assertNotOnScreen(t, sc, "PLANNER-MOCK-REPLY")
	assertTabBar(t, sc, "coder·dlg-02", "[3/3]")

	// Switch to the planner: only the planner's stream shows.
	sc.engine.ApplySync(func() { sc.app.switchAgentView("dlg-planner-01") })
	captureStep(sc, "mock-switch-to-planner")
	assertOnScreen(t, sc, "PLANNER-MOCK-REPLY")
	assertNotOnScreen(t, sc, "CODER-MOCK-REPLY")
	assertTabBar(t, sc, "planner·dlg-01", "[2/3]")
}

// runHeldThenReleased runs the planner (held mid-stream via hold) and coder
// (released) agents CONCURRENTLY against the scripted provider and returns
// both captured event streams (planner, coder).
func runHeldThenReleased(t *testing.T, prov *mock.Provider, hold chan struct{}) (planner, coder []agentic.OutputEvent) {
	t.Helper()
	runAgent := func(model, prompt string, c *eventCollector, done chan<- error) {
		agent := agentic.NewAgent(agentic.Config{Model: prov.Model(model), SystemPrompt: "test"})
		agent.AddObserver(c)
		_, err := agent.RunAndCollect(context.Background(), prompt)
		done <- err
	}

	plannerEvents, coderEvents := &eventCollector{}, &eventCollector{}
	plannerDone := make(chan error, 1)
	go runAgent("planner-mock", "plan this", plannerEvents, plannerDone)

	// Wait until the planner's stream has started and is held mid-reply.
	waitForCalls(t, prov, "planner-mock", 1)

	// The coder runs to completion while the planner is frozen mid-stream.
	coderDone := make(chan error, 1)
	go runAgent("coder-mock", "code this", coderEvents, coderDone)
	if err := <-coderDone; err != nil {
		t.Fatalf("coder run: %v", err)
	}
	select {
	case err := <-plannerDone:
		t.Fatalf("planner finished while held (Hold broken): %v", err)
	default:
	}
	close(hold) // release the planner mid-stream
	if err := <-plannerDone; err != nil {
		t.Fatalf("planner run: %v", err)
	}

	pevs, cevs := plannerEvents.drain(), coderEvents.drain()
	if len(pevs) == 0 || len(cevs) == 0 {
		t.Fatalf("agents emitted no events (planner=%d coder=%d)", len(pevs), len(cevs))
	}
	return pevs, cevs
}

// drain returns a copy of the collected events.
func (c *eventCollector) drain() []agentic.OutputEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]agentic.OutputEvent(nil), c.events...)
}

// waitForCalls polls until the model has been served n streams (deadline 2s).
func waitForCalls(t *testing.T, prov *mock.Provider, model string, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for prov.Calls(model) < n {
		if time.Now().After(deadline) {
			t.Fatalf("%s: stream never started (calls=%d)", model, prov.Calls(model))
		}
		time.Sleep(time.Millisecond)
	}
}

