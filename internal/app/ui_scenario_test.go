// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/spinner"
	"github.com/pijalu/goa/tui"
	goaltui "github.com/pijalu/goa/tui/goal"
)

// uiScenario is the agent-testable harness for the app event layer.
//
// It exists because the TUI, although it has a protocol-free screen model
// (tui.AgentFrame) and a diff recorder (tui.Filmstrip), had no way for an
// agent (human or AI) to drive a realistic *event sequence* through the app's
// status/streaming handlers and observe the resulting UI evolution. Without
// it, the only way to "see" the spinner was to run goa against a live model
// in a real terminal — which is exactly why the "spinner disappears after the
// first tool call" bug cost hours of unproductive agent debugging.
//
// The harness wires the full production component tree to a fake terminal and
// records a tui.Filmstrip snapshot after each event, so an agent can inspect
// the complete series of widget states and diffs (status lifecycle, tool
// widgets, chat content) as data, with no real terminal involved.
type uiScenario struct {
	tb       testing.TB
	engine   *tui.TUI
	chat     *tui.ChatViewport
	status   *tui.StatusMsg
	footer   *tui.Footer
	editor   *tui.Editor
	app      *App
	film     *tui.Filmstrip
	term     *testTerminal
	stepName func(agentic.EventType, agentic.OutputState) string
}

// newUIScenario builds a fresh production component tree on a fake terminal
// and returns a harness ready to receive events via apply.
func newUIScenario(tb testing.TB, w, h int) *uiScenario {
	tb.Helper()
	// Deterministic animated spinner so frame/visibility assertions are stable.
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
	subs.cfg = &config.Config{
		TUI: config.TUIConfig{Transparency: config.TransparencyConfig{ShowThinking: true}},
	}
	subs.tuiEngine = engine
	subs.chat = chat
	subs.statusMsg = statusBar
	subs.footer = footer
	subs.goalBubble = goal

	app := New(subs)
	return &uiScenario{
		tb:     tb,
		engine: engine,
		chat:   chat,
		status: statusBar,
		footer: footer,
		editor: inp,
		app:    app,
		film:   tui.NewFilmstrip(),
		term:   term,
	}
}

// apply feeds one agent OutputEvent through the app's event handler, renders,
// and records a Filmstrip snapshot of the resulting UI. The label is derived
// from the event type/state (overridable via scenario.stepName).
//
// All work runs on the engine command loop (ApplySync) so component state is
// read by its sole owner, matching production semantics.
func (s *uiScenario) apply(ev *agentic.OutputEvent) tui.Snapshot {
	s.tb.Helper()
	s.engine.ApplySync(func() {
		s.app.handleAgentOutputEvent(ev)
	})
	// Render synchronously so the AgentFrame reflects the post-event layout.
	s.engine.RenderNow()
	label := defaultStepLabel(ev)
	if s.stepName != nil {
		label = s.stepName(ev.Type, ev.State)
	}
	frame := s.engine.AgentFrame()
	return s.film.Capture(label, frame, s.status.Text())
}

// statusVisible reports whether the status spinner currently shows any text.
func (s *uiScenario) statusVisible() bool { return s.status.IsVisible() }

// statusText returns the current status spinner text.
func (s *uiScenario) statusText() string { return s.status.Text() }

// filmstrip returns the recorded series of UI states.
func (s *uiScenario) filmstrip() *tui.Filmstrip { return s.film }

// defaultStepLabel produces a readable name for an event, used as the
// filmstrip step label unless the scenario overrides stepName.
func defaultStepLabel(ev *agentic.OutputEvent) string {
	switch ev.Type {
	case agentic.EventStateChange:
		return "state_change/" + stateName(ev.State)
	case agentic.EventContent:
		if ev.Role == agentic.Assistant {
			return "assistant_content"
		}
		return "content/" + string(ev.Role)
	case agentic.EventToolCall:
		return "tool_call/" + ev.ToolName
	case agentic.EventToolResult:
		return "tool_result/" + ev.ToolName
	case agentic.EventEnd:
		return "end"
	case agentic.EventProgress:
		return "progress"
	default:
		return string(ev.Type)
	}
}

func stateName(st agentic.OutputState) string {
	switch st {
	case agentic.StateThinking:
		return "thinking"
	case agentic.StateContent:
		return "content"
	case agentic.StateToolCall:
		return "tool_call"
	case agentic.StateToolResult:
		return "tool_result"
	case agentic.StateIdle:
		return "idle"
	default:
		return "unknown"
	}
}
