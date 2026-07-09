// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/core/orchestrator"
	"github.com/pijalu/goa/tui"
)

// TestOrchestratorView_Filmstrip_PersistenceAndFooterStats drives the FULL
// event sequence (start → stream → stats → finish) through the simplified
// view and records a Filmstrip, asserting:
//   - the tab bar is never present (it was removed in the simplified UI);
//   - the chat viewport is always visible;
//   - the footer eventually shows the per-model CH column once stats arrive;
//   - the view PERSISTS after finish (the last frame still shows the footer).
//
// This is the regression guard for the simplified "single chat + stats" layout.
func TestOrchestratorView_Filmstrip_PersistenceAndFooterStats(t *testing.T) {
	sc := newOrchViewScenario(t, 100, 30)
	sc.app.attachOrchView(newFakeOrchSource())
	sc.flush()

	film := captureLifecycleFilmstrip(t, sc)

	frames := film.Frames()
	if len(frames) < len(lifecycleEvents())+1 {
		t.Fatalf("captured %d frames, want at least %d", len(frames), len(lifecycleEvents())+1)
	}

	assertBarAbsent(t, frames[0], "pre-run frame should not show the tab bar")
	for i := 1; i < len(frames); i++ {
		assertBarAbsent(t, frames[i], "frame %d should not show the tab bar", i)
		assertChatVisible(t, frames[i], "frame %d chat should be visible", i)
	}
	assertBarAbsent(t, frames[len(frames)-1], "tab bar should never appear after run finished")
	if sc.app.subs.agentView == nil || !sc.app.subs.agentView.Finished() {
		t.Error("view not finished after run_finished event")
	}

	// The footer should show per-model stats including the CH column.
	frame := sc.frame()
	footer := frame.FindNode("Footer")
	if footer == nil || !strings.Contains(footer.Text, "CH=") {
		t.Errorf("footer should show per-model CH stats; footer=%v", footer)
	}
}

// captureLifecycleFilmstrip records a pre-run frame plus one frame per
// translated lifecycle event, applying each on the command loop.
func captureLifecycleFilmstrip(t *testing.T, sc *orchViewScenario) *tui.Filmstrip {
	t.Helper()
	film := tui.NewFilmstrip()
	film.Capture("pre-run", sc.frame(), "")
	for _, ev := range lifecycleEvents() {
		ne, ok := translateOrchEvent(ev)
		if !ok {
			continue
		}
		v := sc.app.subs.agentView
		sc.engine.ApplySync(func() {
			v.ApplyEvent(ne)
			sc.app.updateOrchInputPrompt()
			sc.app.updateOrchFooterStats()
		})
		film.Capture(string(ev.Type), sc.frame(), "")
	}
	return film
}

func assertBarAbsent(t *testing.T, s tui.Snapshot, format string, args ...any) {
	t.Helper()
	if s.Frame.FindNode("orchestrator.AgentTabBar") != nil {
		t.Errorf("tab bar should not appear: "+format, args...)
	}
}

func assertChatVisible(t *testing.T, s tui.Snapshot, format string, args ...any) {
	t.Helper()
	if s.Frame.FindNode("ChatViewport") == nil {
		t.Errorf("chat should be visible: "+format, args...)
	}
}

// TestOrchestratorTabs_SpinnerClearsOnRunFinish verifies Bug 1: after the run
// finishes (EvSourceFinished), the shared status spinner is cleared so it does
// not linger as "orchestrator answering..." beneath the finish banner. Drives
// events through handleOrchViewEvent so the status spinner is exercised.
func TestOrchestratorTabs_SpinnerClearsOnRunFinish(t *testing.T) {
	sc := newOrchViewScenario(t, 100, 30)
	sc.app.attachOrchView(newFakeOrchSource())
	sc.flush()

	events := []orchestrator.Event{
		{Type: orchestrator.EventRunStarted, Payload: map[string]any{"objective": "ship it", "topology": "hub", "name": "daring.hawk"}},
		{Type: orchestrator.EventAgentStarted, AgentID: "o-1", Role: "orchestrator", Model: "qwen"},
		{Type: orchestrator.EventAgentMessage, AgentID: "o-1", Role: "orchestrator", Payload: map[string]any{"text": "drafting plan"}},
		{Type: orchestrator.EventRunFinished, Payload: map[string]any{"ok": true}},
	}

	film := tui.NewFilmstrip()
	for _, ev := range events {
		ne, ok := translateOrchEvent(ev)
		if !ok {
			continue
		}
		nev := ne
		sc.engine.ApplySync(func() {
			sc.app.handleOrchViewEvent(nev)
			sc.app.updateOrchInputPrompt()
			sc.app.updateOrchFooterStats()
		})
		film.Capture(string(ev.Type), sc.frame(), sc.app.subs.statusMsg.Text())
	}

	trace := film.StatusTrace()
	answeringSeen := false
	for _, s := range trace {
		if strings.Contains(s, "answering") {
			answeringSeen = true
		}
	}
	if !answeringSeen {
		t.Errorf("expected 'answering' status mid-run; trace=%v", trace)
	}
	if last := trace[len(trace)-1]; last != "" {
		t.Errorf("expected empty status after run finished; last=%q trace=%v", last, trace)
	}

	// The final visible frame must not show a lingering spinner with "answering".
	frame := sc.frame()
	visible := strings.Join(frame.Visible, "\n")
	if strings.Contains(visible, "answering") {
		t.Errorf("final frame should not contain 'answering' (spinner must be cleared):\n%s", visible)
	}
}

// TestOrchestratorTabs_PendingInputBoxStaysVisible verifies Bug 3: the
// pending-input prompt rendered by PendingInputBox stays visible in the
// simplified UI. It also verifies updateOrchInputPrompt no longer clobbers the
// prompt title mid-prompt, and that cancelling removes the box.
func TestOrchestratorTabs_PendingInputBoxStaysVisible(t *testing.T) {
	sc := newOrchViewScenario(t, 100, 30)
	sc.app.attachOrchView(newFakeOrchSource())
	sc.flush()
	sc.applyOrchEvents(lifecycleEvents())

	const prompt = "Describe the issue (optional), then press Enter:"
	sc.engine.ApplySync(func() {
		sc.app.requestMainInput(prompt, func(string) {})
	})

	// An orchestration event fires updateOrchInputPrompt; it must NOT overwrite
	// the pending-input title with the steer prompt.
	sc.engine.ApplySync(func() { sc.app.updateOrchInputPrompt() })
	if got := sc.app.subs.getInput().Title(); !strings.Contains(got, "Describe the issue") {
		t.Errorf("title clobbered mid-prompt = %q, want to retain prompt", got)
	}

	// The chat and pending input box are always visible in the simplified UI.
	frame := sc.frame()
	if frame.FindNode("ChatViewport") == nil {
		t.Error("ChatViewport should be visible")
	}
	box := frame.FindNode("PendingInputBox")
	if box == nil || !strings.Contains(box.Text, "Describe the issue") {
		t.Errorf("PendingInputBox should show prompt; node=%v", box)
	}

	// Cancel: the box must clear.
	sc.engine.ApplySync(func() { sc.app.cancelPendingMainInput() })
	cleared := sc.frame()
	if node := cleared.FindNode("PendingInputBox"); node != nil {
		t.Errorf("PendingInputBox should be absent after cancel; node=%v", node)
	}
}

// TestOrchestratorTabs_ToolCallShowsNameInStatusAndWidget verifies Bug 8:
// when an orchestration sub-agent calls a tool, the spinner status names the
// tool ("coder tool calling: glob") and the widget header shows the tool name
// + formatted args instead of the literal "run tool". Drives events through
// handleOrchViewEvent (the real forwarder path) so the status spinner is
// exercised.
func TestOrchestratorTabs_ToolCallShowsNameInStatusAndWidget(t *testing.T) {
	sc := newOrchViewScenario(t, 100, 30)
	sc.app.attachOrchView(newFakeOrchSource())
	sc.flush()

	// "glob" has no dedicated renderer → exercises the generic fallback.
	events := []orchestrator.Event{
		{Type: orchestrator.EventRunStarted, Payload: map[string]any{"objective": "test", "topology": "hub"}},
		{Type: orchestrator.EventAgentStarted, AgentID: "c-1", Role: "coder", Model: "gemma",
			Payload: map[string]any{"provider": "google", "thinking": "off"}},
		{Type: orchestrator.EventAgentToolCall, AgentID: "c-1", Role: "coder",
			Payload: map[string]any{"tool": "glob", "input": `{"pattern":"**/*.go"}`, "call_id": "t1"}},
	}

	film := captureOrchFilmstripOnTab(t, sc, events, "")

	trace := film.StatusTrace()
	nameInStatus := false
	for _, s := range trace {
		if strings.Contains(s, "glob") {
			nameInStatus = true
		}
	}
	if !nameInStatus {
		t.Errorf("status trace should mention tool name 'glob'; trace=%v", trace)
	}

	frame := sc.frame()
	chat := frame.FindNode("ChatViewport")
	if chat == nil {
		t.Fatal("ChatViewport missing after tool call")
	}
	if strings.Contains(chat.Text, "run tool") {
		t.Errorf("generic tool widget should not show 'run tool'; got:\n%s", chat.Text)
	}
	if !strings.Contains(chat.Text, "glob") || !strings.Contains(chat.Text, "**/*.go") {
		t.Errorf("widget should show tool name + arg 'glob **/*.go'; got:\n%s", chat.Text)
	}
}
