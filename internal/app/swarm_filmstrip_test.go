// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/internal/spinner"
	"github.com/pijalu/goa/tui"
	goaltui "github.com/pijalu/goa/tui/goal"
)

// swarmUIScenario is a minimal filmstrip harness for chat events, similar to
// uiScenario but for event.ChatEvent rather than agentic.OutputEvent.
type swarmUIScenario struct {
	tb     testing.TB
	engine *tui.TUI
	chat   *tui.ChatViewport
	status *tui.StatusMsg
	footer *tui.Footer
	app    *App
	film   *tui.Filmstrip
}

func newSwarmUIScenario(tb testing.TB, w, h int) *swarmUIScenario {
	tb.Helper()
	_, def := spinner.Default()
	tui.SetSpinner(def)
	tb.Cleanup(func() { tui.SetSpinner(spinner.Definition{}) })

	term := &testTerminal{w: w, h: h}
	engine := tui.NewTUI(term)
	if err := engine.Start(); err != nil {
		tb.Fatalf("engine Start: %v", err)
	}
	tb.Cleanup(func() { engine.Stop() })

	header := tui.NewHeader("goa", "test")
	chat := tui.NewChatViewport()
	pending := tui.NewStatusMsg()
	statusBar := tui.NewStatusMsg()
	goal := goaltui.NewBubble()
	inp := tui.NewEditor()
	footer := tui.NewFooter()

	engine.AddChild(header)
	engine.AddChild(chat)
	engine.AddChild(pending)
	engine.AddChild(statusBar)
	engine.AddChild(goal)
	engine.AddChild(inp)
	engine.AddChild(footer)
	engine.SetFocus(inp)

	pending.SetTUI(engine)
	statusBar.SetTUI(engine)
	statusBar.SetOnFrameChange(func() { chat.InvalidateRunningToolWidgets() })
	inp.SetTUI(engine)

	subs := testSubsystems()
	subs.tuiEngine = engine
	subs.chat = chat
	subs.statusMsg = statusBar
	subs.footer = footer
	subs.goalBubble = goal

	app := New(subs)
	return &swarmUIScenario{
		tb:     tb,
		engine: engine,
		chat:   chat,
		status: statusBar,
		footer: footer,
		app:    app,
		film:   tui.NewFilmstrip(),
	}
}

func (s *swarmUIScenario) applyChat(ev event.ChatEvent) tui.Snapshot {
	s.tb.Helper()
	s.engine.ApplySync(func() {
		s.app.handleChatEvent(ev)
	})
	s.engine.RenderNow()
	label := "chat"
	switch {
	case ev.InterAgent != nil:
		label = "interagent/" + ev.InterAgent.From
	case ev.TaskUpdate != nil:
		label = "taskupdate/" + ev.TaskUpdate.Status
	}
	frame := s.engine.AgentFrame()
	return s.film.Capture(label, frame, s.status.Text())
}

func (s *swarmUIScenario) rendered() string {
	return strings.Join(s.chat.Render(120), "\n")
}

// TestSwarmActivityShowsInChatHistory validates that the swarm emitter's
// InterAgent messages render as agent messages in the chat viewport, so the
// user can follow sub-agent activity in the conversation history.
func TestSwarmActivityShowsInChatHistory(t *testing.T) {
	sc := newSwarmUIScenario(t, 120, 24)

	sc.applyChat(event.ChatEvent{InterAgent: &event.InterAgent{
		From:    "swarm-Explore-items-abc",
		To:      "user",
		Content: "🐝 sub-agent started: item-a",
	}})
	sc.applyChat(event.ChatEvent{InterAgent: &event.InterAgent{
		From:    "swarm-Explore-items-abc",
		To:      "user",
		Content: "✅ sub-agent completed: item-a\nExplored item-a result",
	}})
	sc.applyChat(event.ChatEvent{InterAgent: &event.InterAgent{
		From:    "swarm-Explore-items-def",
		To:      "user",
		Content: "🐝 sub-agent started: item-b",
	}})

	rendered := sc.rendered()
	if !strings.Contains(rendered, "🐝 sub-agent started: item-a") {
		t.Errorf("expected chat to show swarm start for item-a; rendered:\n%s", rendered)
	}
	if !strings.Contains(rendered, "✅ sub-agent completed: item-a") {
		t.Errorf("expected chat to show swarm completion for item-a; rendered:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Explored item-a result") {
		t.Errorf("expected chat to show sub-agent result; rendered:\n%s", rendered)
	}
	if !strings.Contains(rendered, "🐝 sub-agent started: item-b") {
		t.Errorf("expected chat to show swarm start for item-b; rendered:\n%s", rendered)
	}

	// Filmstrip sanity: there should be at least one frame per chat event.
	frames := sc.film.Frames()
	if len(frames) < 3 {
		t.Errorf("expected at least 3 filmstrip frames, got %d", len(frames))
	}
	for i, f := range frames {
		if f.Label == "" {
			t.Errorf("frame %d has empty label", i)
		}
	}
}

// TestSwarmProgressUpdater_UpdatesRunningToolWidget validates that the app's
// progress updater writes live sub-agent status into the running agent_swarm
// tool widget so the user can see per-item progress without leaving the tool
// block.
func TestSwarmProgressUpdater_UpdatesRunningToolWidget(t *testing.T) {
	sc := newSwarmUIScenario(t, 120, 24)

	// Simulate the tool widget created by the agent when agent_swarm is called.
	tc := sc.chat.AddToolExecution("agent_swarm", `{"task":"Explore","items":["a","b"]}`)
	sc.engine.ApplySync(func() {
		tc.SetStatus(tui.ToolRunning)
	})
	sc.engine.RenderNow()

	updater := &swarmProgressUpdater{app: sc.app}
	updater.Update("🐝 completed: 0/2\n  a: running\n  b: pending")

	rendered := sc.rendered()
	if !strings.Contains(rendered, "🐝 completed: 0/2") {
		t.Errorf("expected tool widget to show swarm progress; rendered:\n%s", rendered)
	}
	if !strings.Contains(rendered, "a: running") {
		t.Errorf("expected tool widget to show item a status; rendered:\n%s", rendered)
	}
	if !strings.Contains(rendered, "b: pending") {
		t.Errorf("expected tool widget to show item b status; rendered:\n%s", rendered)
	}
}
