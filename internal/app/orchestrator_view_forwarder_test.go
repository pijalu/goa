// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/core/orchestrator"
	"github.com/pijalu/goa/tui"
)

// orchViewScenario wires the production component tree to a fake terminal with
// the simplified multi-agent layout (chat + input + footer, no tab bar or stats
// panel). This matches the real assembleEngine so the forwarder can be driven
// as data and inspected via AgentFrame.
type orchViewScenario struct {
	tb     testing.TB
	engine *tui.TUI
	chat   *tui.ChatViewport
	app    *App
}

func newOrchViewScenario(tb testing.TB, w, h int) *orchViewScenario {
	tb.Helper()
	term := &testTerminal{w: w, h: h}
	engine := tui.NewTUI(term)
	if err := engine.Start(); err != nil {
		tb.Fatalf("engine Start: %v", err)
	}
	tb.Cleanup(func() { engine.Stop() })

	chat := tui.NewChatViewport()
	pendingInputBox := tui.NewPendingInputBox()
	inp := tui.NewEditor()
	header := tui.NewHeader("goa", "test")
	footer := tui.NewFooter()

	engine.AddChild(header)
	engine.AddChild(chat)
	engine.AddChild(pendingInputBox)
	engine.AddChild(inp)
	engine.AddChild(footer)
	inp.SetTUI(engine)
	engine.SetFocus(inp)

	subs := testSubsystems()
	subs.tuiEngine = engine
	subs.chat = chat
	subs.inputEditor = inp
	subs.footer = footer
	subs.pendingInputBox = pendingInputBox
	subs.agentStreams = newAgentStreamRegistry()

	return &orchViewScenario{tb: tb, engine: engine, chat: chat, app: New(subs)}
}

// flush waits for the command loop to finish all queued applies.
func (s *orchViewScenario) flush() {
	s.engine.ApplySync(func() {})
}

// frame renders now and returns the current AgentFrame snapshot.
func (s *orchViewScenario) frame() tui.AgentFrame {
	s.engine.RenderNow()
	return s.engine.AgentFrame()
}

// fakeOrchSource is a test double for the runtime surface the view forwarder
// consumes (Subscribe + Done), fed by pushing events onto the channel.
type fakeOrchSource struct {
	events chan orchestrator.Event
	done   chan struct{}
}

func newFakeOrchSource() *fakeOrchSource {
	return &fakeOrchSource{events: make(chan orchestrator.Event, 32), done: make(chan struct{})}
}

func (f *fakeOrchSource) Subscribe() <-chan orchestrator.Event { return f.events }
func (f *fakeOrchSource) Done() <-chan struct{}               { return f.done }

// lifecycleEvents is a realistic fanout event sequence used by several tests.
func lifecycleEvents() []orchestrator.Event {
	return []orchestrator.Event{
		{Type: orchestrator.EventRunStarted, Payload: map[string]any{"objective": "ship it", "topology": "fanout"}},
		{Type: orchestrator.EventAgentStarted, AgentID: "c-1", Role: "coder", Model: "gemma",
			Payload: map[string]any{"provider": "google", "thinking": "off"}},
		{Type: orchestrator.EventAgentStarted, AgentID: "r-1", Role: "reviewer", Model: "qwen",
			Payload: map[string]any{"provider": "lmstudio", "thinking": "medium"}},
		{Type: orchestrator.EventAgentMessage, AgentID: "c-1", Role: "coder", Payload: map[string]any{"text": "drafting code"}},
		{Type: orchestrator.EventAgentStats, AgentID: "c-1", Role: "coder",
			Payload: map[string]any{"tokens_in": 40, "tokens_out": 12, "cache_read": 1024, "turns": 1, "status": "running", "thinking": "off"}},
		{Type: orchestrator.EventAgentFinished, AgentID: "c-1", Role: "coder", Payload: map[string]any{"outcome": "ok"}},
		{Type: orchestrator.EventRunFinished, Payload: map[string]any{"ok": true}},
	}
}

// applyOrchEvents translates and applies each event to the view on the command
// loop, exactly as drainOrchView does, but deterministically (no goroutine).
func (s *orchViewScenario) applyOrchEvents(events []orchestrator.Event) {
	s.tb.Helper()
	for _, ev := range events {
		ne, ok := translateOrchEvent(ev)
		if !ok {
			continue
		}
		v := s.app.subs.agentView
		if v == nil {
			s.tb.Fatalf("agentView nil; attachOrchView not flushed")
		}
		ev := ne // capture
		s.engine.ApplySync(func() {
			v.ApplyEvent(ev)
			s.app.updateOrchInputPrompt()
			s.app.updateOrchFooterStats()
		})
	}
}

// TestOrchestratorViewForwarder_RendersSimplifiedView is the canonical UI
// validation: the simplified layout always shows the chat viewport and the
// footer with per-model stats, but never shows the tab bar or stats panel.
func TestOrchestratorViewForwarder_RendersSimplifiedView(t *testing.T) {
	sc := newOrchViewScenario(t, 100, 30)
	sc.chat.AddSystemMessage("hello") // so the chat renders a layer at baseline

	base := sc.frame()
	checkAbsent(t, base, "orchestrator.AgentTabBar", "tab bar present before any run")
	checkAbsent(t, base, "orchestrator.AgentContent", "content present before any run")
	if base.FindNode("ChatViewport") == nil {
		t.Error("chat absent before any run")
	}

	sc.app.attachOrchView(newFakeOrchSource())
	sc.flush()
	sc.applyOrchEvents(lifecycleEvents())

	frame := sc.frame()
	checkAbsent(t, frame, "orchestrator.AgentTabBar", "tab bar should not appear in simplified UI")
	checkAbsent(t, frame, "orchestrator.AgentContent", "stats panel should not appear in simplified UI")
	checkNodeText(t, frame, "ChatViewport", []string{"hello"})
	checkNodeText(t, frame, "Footer", []string{"CH", "Coder"})

	if sc.app.subs.agentView == nil || !sc.app.subs.agentView.Finished() {
		t.Error("view not attached or not marked finished")
	}
}

func checkAbsent(t *testing.T, f tui.AgentFrame, name, msg string) {
	t.Helper()
	if f.FindNode(name) != nil {
		t.Error(msg)
	}
}

func checkNodeText(t *testing.T, f tui.AgentFrame, name string, want []string) {
	t.Helper()
	node := f.FindNode(name)
	if node == nil {
		t.Fatalf("%s layer missing", name)
	}
	for _, w := range want {
		if !strings.Contains(node.Text, w) {
			t.Errorf("%s missing %q: %q", name, w, node.Text)
		}
	}
}

// TestOrchestratorViewForwarder_SteerPickerJumpsByNumber verifies the ctrl+x
// steering target picker: opening it and pressing a number selects that target
// and updates the input prompt. Per-agent tabs were removed; the picker shows
// steering targets instead.
func TestOrchestratorViewForwarder_SteerPickerJumpsByNumber(t *testing.T) {
	sc := newOrchViewScenario(t, 100, 30)
	sc.app.attachOrchView(newFakeOrchSource())
	sc.flush()
	sc.applyOrchEvents(lifecycleEvents())

	sc.engine.ApplySync(func() { sc.app.openAgentTabSelector() })
	sc.engine.ApplySync(func() { sc.engine.SendKey("2") }) // coder (index 1)

	if got := sc.app.subs.agentView.SteerTarget(); got != "c-1" {
		t.Errorf("after picker digit 2 steer target = %q, want c-1", got)
	}
	if got := sc.app.subs.getInput().Title(); got != "steer coder" {
		t.Errorf("prompt = %q, want 'steer coder'", got)
	}
}

// TestOrchestratorViewForwarder_SteerPromptReflectsTarget verifies the input
// editor prompt reflects the current ctrl-x steering target (default "all").
func TestOrchestratorViewForwarder_SteerPromptReflectsTarget(t *testing.T) {
	sc := newOrchViewScenario(t, 100, 30)
	sc.app.attachOrchView(newFakeOrchSource())
	sc.flush()
	sc.applyOrchEvents(lifecycleEvents())

	inp := sc.app.subs.getInput()
	if got := inp.Title(); got != "steer all" {
		t.Errorf("default prompt = %q, want 'steer all'", got)
	}

	// Cycling the steering target to the first agent changes the prompt.
	sc.app.subs.agentView.CycleSteerTarget(1)
	sc.app.updateOrchInputPrompt()
	if got := inp.Title(); got != "steer coder" {
		t.Errorf("after cycling prompt = %q, want 'steer coder'", got)
	}
}

// TestOrchestratorViewForwarder_DrainsWithoutRace drives the event sequence
// through drainOrchView from a separate goroutine (as the real forwarder does)
// while the render loop runs, asserting under -race that the single-owner
// invariant holds and the final frame is consistent (validates 4.6).
func TestOrchestratorViewForwarder_DrainsWithoutRace(t *testing.T) {
	sc := newOrchViewScenario(t, 100, 30)
	src := newFakeOrchSource()
	sc.app.attachOrchView(src)
	sc.flush()

	appDone := make(chan struct{})
	go func() { sc.app.drainOrchView(appDone, src); close(appDone) }()

	// Producer goroutine: feed events with small yields so the drain consumer
	// interleaves with renders, then signal run completion.
	go func() {
		for _, ev := range lifecycleEvents() {
			src.events <- ev
			time.Sleep(2 * time.Millisecond)
		}
		close(src.done)
	}()

	select {
	case <-appDone:
	case <-time.After(3 * time.Second):
		t.Fatal("drainOrchView did not return after Done")
	}
	sc.flush()

	var finished bool
	sc.engine.ApplySync(func() {
		finished = sc.app.subs.agentView != nil && sc.app.subs.agentView.Finished()
	})
	frame := sc.frame()
	hasBar := frame.FindNode("orchestrator.AgentTabBar") != nil
	if !finished {
		t.Error("view not finished after drain")
	}
	if hasBar {
		t.Error("tab bar should not render in simplified UI after drain")
	}
	if frame.FindNode("ChatViewport") == nil {
		t.Error("chat should be visible after drain")
	}
	if footer := frame.FindNode("Footer"); footer == nil || !strings.Contains(footer.Text, "CH") {
		t.Errorf("footer should show per-model stats after drain; footer=%v", footer)
	}
}
