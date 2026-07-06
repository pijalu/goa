// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/core/orchestrator"
	orchpanel "github.com/pijalu/goa/tui/orchestrator"
	"github.com/pijalu/goa/tui"
)

// orchViewScenario wires the production component tree to a fake terminal with
// the persistent multi-agent view components (AgentContent, AgentTabBar)
// inserted exactly as assembleEngine places them, so the forwarder can be
// driven as data and inspected via AgentFrame.
type orchViewScenario struct {
	tb           testing.TB
	engine       *tui.TUI
	chat         *tui.ChatViewport
	agentContent *orchpanel.AgentContent
	agentTabBar  *orchpanel.AgentTabBar
	app          *App
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
	agentContent := orchpanel.NewAgentContent()
	agentTabBar := orchpanel.NewAgentTabBar()
	inp := tui.NewEditor()
	header := tui.NewHeader("goa", "test")
	footer := tui.NewFooter()

	engine.AddChild(header)
	engine.AddChild(chat)
	engine.AddChild(agentContent)
	engine.AddChild(agentTabBar)
	engine.AddChild(inp)
	engine.AddChild(footer)
	inp.SetTUI(engine)
	engine.SetFocus(inp)

	subs := testSubsystems()
	subs.tuiEngine = engine
	subs.chat = chat
	subs.agentContent = agentContent
	subs.agentTabBar = agentTabBar
	subs.inputEditor = inp

	return &orchViewScenario{
		tb: tb, engine: engine, chat: chat,
		agentContent: agentContent, agentTabBar: agentTabBar, app: New(subs),
	}
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
		})
	}
}

// TestOrchestratorViewForwarder_RendersTabbedView is the canonical UI
// validation: a full event sequence renders the tabbed view (AgentTabBar +
// AgentContent present, ChatViewport suppressed), with the Stats tab showing
// the CH column and the tab bar listing Stats + All.
func TestOrchestratorViewForwarder_RendersTabbedView(t *testing.T) {
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
	checkNodeText(t, frame, "orchestrator.AgentTabBar", []string{"Stats", "All", "coder", "[1/"})
	checkNodeText(t, frame, "orchestrator.AgentContent", []string{"CH", "(google)", "gemma"})
	checkAbsent(t, frame, "ChatViewport", "ChatViewport layer should be suppressed during orchestration")

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

// TestOrchestratorViewForwarder_CycleHotkeyChangesTab verifies the tab-cycle
// hotkey handler advances the active tab and updates the steering prompt.
func TestOrchestratorViewForwarder_CycleHotkeyChangesTab(t *testing.T) {
	sc := newOrchViewScenario(t, 100, 30)
	sc.app.attachOrchView(newFakeOrchSource())
	sc.flush()
	sc.applyOrchEvents(lifecycleEvents())

	inp := sc.app.subs.getInput()
	if got := inp.Title(); got != "steer all:" {
		t.Errorf("initial prompt = %q, want 'steer all:'", got)
	}

	sc.engine.ApplySync(func() { sc.app.cycleAgentTab(1) })
	if got := inp.Title(); !strings.Contains(got, "steer coder:") {
		t.Errorf("after cycle prompt = %q, want 'steer coder:'", got)
	}
	if tab, ok := sc.app.subs.agentView.ActiveTab(); !ok || tab.Key != "c-1" {
		t.Errorf("active tab = %+v, want c-1", tab)
	}
}

// TestOrchestratorViewForwarder_SteerPromptReflectsActiveTab verifies the input
// editor prompt follows the active tab (steer all: on Stats, steer coder: on
// the coder tab).
func TestOrchestratorViewForwarder_SteerPromptReflectsActiveTab(t *testing.T) {
	sc := newOrchViewScenario(t, 100, 30)
	sc.app.attachOrchView(newFakeOrchSource())
	sc.flush()
	sc.applyOrchEvents(lifecycleEvents())

	inp := sc.app.subs.getInput()
	if got := inp.Title(); got != "steer all:" {
		t.Errorf("stats-tab prompt = %q, want 'steer all:'", got)
	}
	if !sc.app.selectAgentTab("c-1") {
		t.Fatal("selectAgentTab(c-1) failed")
	}
	if got := inp.Title(); got != "steer coder:" {
		t.Errorf("coder-tab prompt = %q, want 'steer coder:'", got)
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
	var hasBar bool
	sc.engine.ApplySync(func() {
		finished = sc.app.subs.agentView != nil && sc.app.subs.agentView.Finished()
	})
	frame := sc.frame()
	hasBar = frame.FindNode("orchestrator.AgentTabBar") != nil
	if !finished {
		t.Error("view not finished after drain")
	}
	if !hasBar {
		t.Error("tab bar not rendered after drain")
	}
}
