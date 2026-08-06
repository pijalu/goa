// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/tui"
)

// TestForkCommand_Filmstrip validates the /fork picker flow as UI data: the
// session selector, the turn selector, the truncated chat replay, the fork
// flash, and the editor prefill — all driven through the production command
// context wiring (ShowSelector + SetEditorText) on the uiScenario harness.
func TestForkCommand_Filmstrip(t *testing.T) {
	sc := newUIScenario(t, 100, 30)
	subs := sc.app.subs
	store, srcEvents := setupForkScenarioStore(t, sc)

	// Run /fork through the production command context on the engine loop.
	// Event-bus dispatch (ClearChat/Flash/replay) is pumped synchronously by
	// the test via pumpEvents — the uiScenario harness is single-goroutine by
	// design, so the production async readers are not started.
	ctx := coreContextForCommand(subs, sc.app)
	sc.engine.ApplySync(func() {
		cmd := &commands.ForkCommand{}
		if err := cmd.Run(ctx, nil); err != nil {
			t.Errorf("fork Run: %v", err)
		}
	})
	pumpEvents(sc)
	sc.engine.RenderNow()
	film := sc.filmstrip()
	film.Capture("fork/session-picker", sc.engine.AgentFrame(), "")

	// Frame 1: session picker visible with the source session item.
	frame := sc.engine.AgentFrame()
	if !frameContains(frame, "Select session to fork:") {
		t.Fatalf("session picker not visible\n%s", frame.Dump())
	}
	if !frameContains(frame, "src") {
		t.Errorf("session picker missing source session\n%s", frame.Dump())
	}

	// Confirm the session (Enter) → turn picker appears.
	sc.engine.SendKey(tui.KeyEnter)
	waitForFrame(t, sc, "at turn:")
	sc.engine.RenderNow()
	film.Capture("fork/turn-picker", sc.engine.AgentFrame(), "")
	frame = sc.engine.AgentFrame()
	if !frameContains(frame, "Turn 1") || !frameContains(frame, "Turn 2") {
		t.Fatalf("turn picker missing turn items\n%s", frame.Dump())
	}
	if !frameContains(frame, "fork-film-apple question") {
		t.Errorf("turn picker missing first-turn label\n%s", frame.Dump())
	}

	// Cursor preselects the last turn (Turn 2 = the banana question). Confirm
	// to fork just before it: history keeps only the apple turn.
	sc.engine.SendKey(tui.KeyEnter)
	waitForFrame(t, sc, "Forked")
	sc.engine.RenderNow()
	film.Capture("fork/forked", sc.engine.AgentFrame(), "")

	// Replay of the truncated stream is async; wait for the pre-cut content to
	// land in the chat (the completion flash races it on a separate bus).
	waitForFrame(t, sc, "Forked session:")
	waitForFrame(t, sc, "fork-film-apple answer")
	sc.engine.RenderNow()
	film.Capture("fork/replayed", sc.engine.AgentFrame(), "")

	frame = sc.engine.AgentFrame()
	if !frameContains(frame, "fork-film-apple answer") {
		t.Errorf("replayed chat missing pre-cut (apple) content\n%s", frame.Dump())
	}
	if frameContains(frame, "fork-film-banana answer") {
		t.Errorf("replayed chat shows post-cut (banana) content; want truncation\n%s", frame.Dump())
	}

	// Editor prefill: the selected turn's message is staged for edit + resend.
	sc.engine.RenderNow()
	film.Capture("fork/editor-prefill", sc.engine.AgentFrame(), "")
	if got := sc.editor.Text(); got != "fork-film-banana question" {
		t.Errorf("editor text = %q, want prefilled selected message", got)
	}

	assertForkedState(t, sc, store, srcEvents)
}

// assertForkedState verifies the agent history was truncated at the fork
// point and the session writer switched to a fresh derived fork ID.
func assertForkedState(t *testing.T, sc *uiScenario, store *core.SessionStore, srcEvents []agentic.OutputEvent) {
	t.Helper()
	// Agent history equals EventsToHistory(events[:3]) — the apple turn only.
	hist := sc.app.subs.agentMgr.CurrentAgent().GetHistory()
	want := agentic.EventsToHistory(srcEvents[:3])
	if len(hist) != len(want) {
		t.Fatalf("agent history len = %d, want %d", len(hist), len(want))
	}
	for i := range want {
		if hist[i].Role != want[i].Role || hist[i].Content != want[i].Content {
			t.Errorf("history[%d] = {%v %q}, want {%v %q}", i, hist[i].Role, hist[i].Content, want[i].Role, want[i].Content)
		}
	}

	// The session writer switched to a fresh derived fork ID.
	if sid := store.SessionID(); !strings.HasPrefix(sid, "src_fork_") {
		t.Errorf("store session ID = %q, want src_fork_*", sid)
	}
}

// setupForkScenarioStore builds a session store holding one session ("src")
// with two user turns, wires it plus an agent manager into the scenario
// subsystems, and returns the store and its source events.
func setupForkScenarioStore(t *testing.T, sc *uiScenario) (*core.SessionStore, []agentic.OutputEvent) {
	t.Helper()
	subs := sc.app.subs
	store := core.NewSessionStore(t.TempDir())
	store.StartSessionWithID("src")
	srcEvents := []agentic.OutputEvent{
		{Type: agentic.EventContent, Role: agentic.User, Text: "fork-film-apple question"},
		{Type: agentic.EventContent, Role: agentic.Assistant, Text: "fork-film-apple answer"},
		{Type: agentic.EventEnd},
		{Type: agentic.EventContent, Role: agentic.User, Text: "fork-film-banana question"},
		{Type: agentic.EventContent, Role: agentic.Assistant, Text: "fork-film-banana answer"},
		{Type: agentic.EventEnd},
	}
	for _, ev := range srcEvents {
		store.WriteEvent(ev)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	subs.sessionStore = store
	subs.inputEditor = sc.editor
	if subs.agentMgr == nil {
		subs.agentMgr = core.NewAgentManager(subs.cfg, store, nil, core.NewSessionState(subs.cfg.DefaultModeState()), subs.events, "")
	}
	subs.agentMgr.SetActiveAgentForTest(agentic.NewAgent(agentic.Config{}))
	return store, srcEvents
}

// frameContains reports whether any visible frame line holds substr.
func frameContains(frame tui.AgentFrame, substr string) bool {
	for _, line := range frame.Visible {
		if strings.Contains(line, substr) {
			return true
		}
	}
	return false
}

// pumpEvents drains the app event buses, dispatching each event through the
// production handlers on the engine loop (single-goroutine actor model, same
// ownership as the live commandLoop). Chat/Footer are drained before Agent:
// senders causally order control events (ClearChat) before the replay stream
// they trigger, and the pump must preserve that order.
func pumpEvents(sc *uiScenario) {
	subs := sc.app.subs
	// Drain Chat and Footer first, in send order relative to Agent events.
	for {
		select {
		case ev := <-subs.events.Chat:
			sc.engine.ApplySync(func() { sc.app.handleChatEvent(ev) })
		case ev := <-subs.events.Footer:
			sc.engine.ApplySync(func() { sc.app.handleFooterEvent(ev) })
		default:
			goto chatDrained
		}
	}
chatDrained:
	for {
		select {
		case ev := <-subs.events.Agent:
			sc.engine.ApplySync(func() { sc.app.handleAgentOutputEvent(&ev.Event) })
		default:
			return
		}
	}
}

// waitForFrame pumps the engine until substr is visible or the deadline hits.
func waitForFrame(t *testing.T, sc *uiScenario, substr string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		pumpEvents(sc)
		sc.engine.ApplySync(func() {})
		sc.engine.RenderNow()
		if frameContains(sc.engine.AgentFrame(), substr) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q\n%s", substr, sc.engine.AgentFrame().Dump())
}
