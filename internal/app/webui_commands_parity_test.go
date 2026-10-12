// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/agentic/provider/mock"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/internal/webui"
	"github.com/pijalu/goa/tui"
)

// G4 checkpoint: the browser drives the SAME pipeline the terminal does. Every
// keystroke below goes through webui.EncodeKey → VirtualTerminal.Input → the
// engine's focus stack, exactly as a WS "key" message does — nothing is
// short-circuited by calling the handlers directly. That is what makes the
// assertions meaningful: if a chord stopped being decoded, or the submit path
// diverged, these would fail.

// scriptedCommand is a registry command whose Run records it and reports back
// through the chat, mirroring what /mode:…, /model and friends do.
type scriptedCommand struct {
	name string
	help string
	ran  *bool
}

func (c *scriptedCommand) Name() string      { return c.name }
func (c *scriptedCommand) Aliases() []string { return nil }
func (c *scriptedCommand) ShortHelp() string { return c.help }
func (c *scriptedCommand) LongHelp() string  { return c.help + " (long)" }
func (c *scriptedCommand) Run(core.Context, []string) error {
	*c.ran = true
	return nil
}

// webCommandSession is a live web session with the production input wiring:
// editor submit → makeSubmitHandler → dispatchUserSubmit → handleSlashCommand,
// plus the real command completer (so the autocomplete popup is the production
// one).
type webCommandSession struct {
	sc     *uiScenario
	vt     *webui.VirtualTerminal
	ranCmd *bool
}

// newWebCommandSession wires the engine, editor and command router over a
// virtual terminal, then focuses the editor the way production does.
func newWebCommandSession(t *testing.T) *webCommandSession {
	t.Helper()
	return newWebCommandSessionSink(t, &recordingSink{}, 100, 30)
}

// newWebCommandSessionSink is newWebCommandSession with an explicit frame sink
// and geometry, so a scenario can observe exactly what a browser receives
// (transcript rows included).
func newWebCommandSessionSink(t *testing.T, sink webui.FrameSink, cols, rows int) *webCommandSession {
	t.Helper()
	vt := webui.NewVirtualTerminal(cols, rows)
	vt.SetSink(sink)

	sc := newUIScenarioTerm(t, vt, cols, rows)
	s := &webCommandSession{sc: sc, vt: vt, ranCmd: new(bool)}

	registry := core.NewCommandRegistry()
	for _, cmd := range []*scriptedCommand{
		{name: "mode", help: "cycle or set the working mode"},
		{name: "model", help: "choose the model"},
		{name: "config", help: "open the configuration"},
	} {
		cmd.ran = s.ranCmd
		if err := registry.Register(cmd); err != nil {
			t.Fatalf("register /%s: %v", cmd.name, err)
		}
	}
	sc.app.subs.registry = registry
	sc.app.subs.cmdRouter = core.NewCommandRouter(registry, core.NewDocEngine(registry))
	sc.app.subs.sessionStore = nil // no session persistence in this scenario

	// Production input wiring (internal/app/events.go): submit reaches the
	// routing pipeline, and the command completer powers the popup.
	sc.editor.SetOnSubmit(sc.app.makeSubmitHandler(sc.engine, sc.chat))
	sc.app.configureInputEditor(sc.editor, sc.engine)
	sc.engine.SetFocus(sc.editor)
	return s
}

// typeText feeds text as individual browser keystrokes.
func (s *webCommandSession) typeText(t *testing.T, text string) {
	t.Helper()
	for _, r := range text {
		s.press(t, webui.KeyEvent{Key: string(r)})
	}
}

// press encodes one browser key event and delivers it to the engine — the
// production server path, minus the socket.
func (s *webCommandSession) press(t *testing.T, ev webui.KeyEvent) {
	t.Helper()
	bytes := webui.EncodeKey(ev)
	if len(bytes) == 0 {
		t.Fatalf("key event %+v produced no terminal bytes", ev)
	}
	s.vt.Input(string(bytes))
}

// submit types text and presses Enter.
func (s *webCommandSession) submit(t *testing.T, text string) {
	t.Helper()
	s.typeText(t, text)
	s.press(t, webui.KeyEvent{Key: "Enter"})
	s.render()
}

// render flushes the engine so the grid holds the state a browser would see.
func (s *webCommandSession) render() {
	s.sc.engine.ApplySync(func() {})
	s.sc.engine.RenderNow()
}

// screen returns the current virtual screen.
func (s *webCommandSession) screen() string { return s.vt.Grid().Text() }

// mustContain fails when the screen lacks want (trimmed of trailing spaces).
func (s *webCommandSession) mustContain(t *testing.T, want string) {
	t.Helper()
	screen := s.screen()
	if !strings.Contains(screen, want) {
		t.Fatalf("screen does not show %q:\n%s", want, screen)
	}
}

// /help typed in the browser must reach handleSlashCommand and render the
// command list — the checkpoint's first command.
func TestWebUI_SlashHelpRendersFromBrowserKeystrokes(t *testing.T) {
	s := newWebCommandSession(t)

	s.submit(t, "/help")

	s.mustContain(t, "# Goa Commands")
	s.mustContain(t, "/mode")
	s.mustContain(t, "/model")
	s.mustContain(t, "/config")
}

// A command routed through core.CommandRouter runs and its echo lands in the
// chat: the same path a terminal takes.
func TestWebUI_SlashCommandRunsThroughRouter(t *testing.T) {
	s := newWebCommandSession(t)

	s.submit(t, "/mode")

	if !*s.ranCmd {
		t.Fatal("/mode did not reach the command router")
	}
	s.mustContain(t, "/mode")
}

// The autocomplete popup is an ordinary scene layer: pressing Tab in the
// browser must paint the candidates into the grid the browser paints.
func TestWebUI_AutocompletePopupRenders(t *testing.T) {
	s := newWebCommandSession(t)

	s.typeText(t, "/mo")
	s.press(t, webui.KeyEvent{Key: "Tab"})
	s.render()

	if !s.sc.editor.AutoCompActive() {
		t.Fatalf("autocomplete did not open for %q:\n%s", s.sc.editor.Text(), s.screen())
	}
	s.mustContain(t, "mode")
}

// Shift+Tab (the reverse chord) must also survive the trip: it is the encoded
// "\x1b[Z" the decoder turns into shift+tab.
func TestWebUI_ShiftTabIsDecoded(t *testing.T) {
	s := newWebCommandSession(t)

	s.typeText(t, "/mo")
	s.press(t, webui.KeyEvent{Key: "Tab"})
	s.render()
	if !s.sc.editor.AutoCompActive() {
		t.Fatalf("autocomplete did not open:\n%s", s.screen())
	}
	s.press(t, webui.KeyEvent{Key: "Tab", Shift: true})
	s.render()
}

// History recall (Up) fills the input line from the per-session history: the
// keys the browser sends are the same ones a terminal sends.
func TestWebUI_HistoryRecallFromBrowserKeys(t *testing.T) {
	s := newWebCommandSession(t)

	s.submit(t, "/help")
	if got := s.sc.editor.Text(); got != "" {
		t.Fatalf("editor not cleared after submit: %q", got)
	}
	s.press(t, webui.KeyEvent{Key: "ArrowUp"})
	s.render()
	if got := s.sc.editor.Text(); got != "/help" {
		t.Fatalf("history recall = %q, want %q:\n%s", got, "/help", s.screen())
	}
}

// A selector overlay (the /model, /mode menu shape) captures input and paints
// into the same grid, so the browser sees the menu exactly as the terminal does.
func TestWebUI_SelectorOverlayRendersAndCapturesInput(t *testing.T) {
	s := newWebCommandSession(t)

	var chosen <-chan string
	s.sc.engine.ApplySync(func() {
		chosen = s.sc.engine.ShowSelector("Select model", []tui.SelectorItem{
			{Value: "sonnet", Label: "Sonnet"},
			{Value: "opus", Label: "Opus"},
		}, "sonnet")
	})
	s.render()
	s.mustContain(t, "Select model")
	s.mustContain(t, "Sonnet")
	s.mustContain(t, "Opus")

	// Input goes to the overlay, not the editor underneath it.
	s.press(t, webui.KeyEvent{Key: "ArrowDown"})
	s.press(t, webui.KeyEvent{Key: "Enter"})
	s.render()

	select {
	case got := <-chosen:
		if got != "opus" {
			t.Fatalf("selector returned %q, want %q", got, "opus")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("selector never returned")
	}
	if got := s.sc.editor.Text(); got != "" {
		t.Errorf("editor received overlay input: %q", got)
	}
}

// A confirm card is the third overlay shape (tool confirmations, /new, …).
func TestWebUI_ConfirmOverlayRendersAndAnswers(t *testing.T) {
	s := newWebCommandSession(t)

	var answer <-chan string
	s.sc.engine.ApplySync(func() {
		answer, _ = s.sc.engine.ShowConfirm("Reset session?", "This clears the transcript.",
			[]tui.ConfirmOption{{ID: "y", Label: "Yes"}, {ID: "n", Label: "No"}}, "n", true)
	})
	s.render()
	s.mustContain(t, "Reset session?")
	s.mustContain(t, "Yes")

	// The default row is "No"; move up onto "Yes" and confirm with Enter.
	s.press(t, webui.KeyEvent{Key: "ArrowUp"})
	s.press(t, webui.KeyEvent{Key: "Enter"})
	s.render()

	select {
	case got := <-answer:
		if got != "y" {
			t.Fatalf("confirm returned %q, want %q", got, "y")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("confirm never returned")
	}
}

// Escape must cancel a capturing overlay rather than reaching the editor —
// the browser's bare ESC must not close a menu by typing into it.
func TestWebUI_EscapeCancelsOverlay(t *testing.T) {
	s := newWebCommandSession(t)

	var answer <-chan string
	s.sc.engine.ApplySync(func() {
		answer, _ = s.sc.engine.ShowConfirm("Proceed?", "About to run a tool.",
			[]tui.ConfirmOption{{ID: "y", Label: "Yes"}, {ID: "n", Label: "No"}}, "n", true)
	})
	s.render()

	s.press(t, webui.KeyEvent{Key: "Escape"})
	s.render()

	select {
	case got := <-answer:
		if got != "" {
			t.Fatalf("confirm returned %q on escape, want the cancellation", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("confirm never returned after escape")
	}
}

// The !bang branch runs before steering and before the command router: text
// submitted from the browser executes as a shell command and its output lands
// in the input line, exactly as it does in a terminal.
//
// This scenario runs the engine's command loop (production shape) rather than the
// single-goroutine test default: the shell executes on its own goroutine and
// hands its result back through engine.Apply, so the loop is what serializes
// those mutations against the frames this test renders. Without the loop,
// Apply would run inline on the shell's goroutine and race the render.
func TestWebUI_BangCommandRunsShellFromBrowserKeys(t *testing.T) {
	s := newWebCommandSession(t)
	s.sc.engine.RunLoops()
	t.Cleanup(s.sc.engine.Stop)

	s.typeText(t, "!echo webui-bang-ok")
	s.press(t, webui.KeyEvent{Key: "Enter"})

	// Read the editor through the command loop: Editor.Text is safe to call
	// only from the goroutine that owns the editor, which with loops running is
	// the loop itself.
	var out string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s.sc.engine.ApplySync(func() { out = s.sc.editor.Text() })
		if strings.Contains(out, "webui-bang-ok") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	// The focus restore is the last mutation the shell makes, and ApplySync
	// above drained the queue in order, so by now the whole Apply has landed.
	s.render()
	if !strings.Contains(out, "webui-bang-ok") {
		t.Fatalf("bang output never reached the input line: %q", out)
	}
	if s.sc.engine.Focused() != s.sc.editor {
		t.Fatalf("editor not focused after bang output")
	}
}

// Steering is the other pre-router branch: while the agent is mid-turn, text
// typed in the browser joins the steering queue instead of starting a second
// turn. The model gate keeps the turn open, so the window is deterministic.
func TestWebUI_SteeringIsQueuedWhileAgentTurnRuns(t *testing.T) {
	s := newWebCommandSession(t)
	prov, gate := startSteeringSession(t, s.sc)
	defer close(gate)

	s.submit(t, "first question")
	s.waitForModelCall(t, prov)

	s.submit(t, "actually, use metric units")

	// The app-level steering branch is what paints the pending bubble; the
	// queue alone would also fill on the direct-send fallback, so the chrome is
	// the assertion that pins the routing.
	if chrome := s.sc.app.subs.steeringChrome; chrome == nil || !chrome.HasPending() {
		t.Fatalf("steering bubble not shown:\n%s", s.screen())
	}
	s.render()
	s.mustContain(t, "metric units")

	sq := s.sc.app.subs.agentMgr.SteeringQueue()
	if sq == nil || sq.Len() != 1 {
		t.Fatalf("steering queue length = %v, want 1", sq)
	}
	if got := sq.Flush(); len(got) != 1 || got[0] != "actually, use metric units" {
		t.Fatalf("steering payload = %v", got)
	}
	// One model call means the steering text did not open a second turn.
	if calls := prov.Calls(webUIModelID); calls != 1 {
		t.Fatalf("model streamed %d times, want 1 (steering must not start a turn)", calls)
	}
}

// startSteeringSession starts a real agent session whose first model turn is
// blocked on a gate, so the app reports "busy" for as long as the test needs.
func startSteeringSession(t *testing.T, sc *uiScenario) (*mock.Provider, chan struct{}) {
	t.Helper()
	prov := mock.New(t)
	gate := make(chan struct{})
	prov.Script(webUIModelID, mock.TextTurn("first answer"))
	prov.SetGate(webUIModelID, gate)

	cfg := &config.Config{}
	bus := event.MakeBus(256, 16, 16, 16)
	sc.app.subs.events = bus
	am := core.NewAgentManager(cfg, nil, nil, nil, bus, "")
	sc.app.subs.agentMgr = am
	if _, err := am.StartSession(prov.Model(webUIModelID), agenticprovider.StreamOptions{},
		"You are the web-UI steering agent.", nil, cfg); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	t.Cleanup(func() {
		if am.IsBusy() {
			// Release the gate so the worker can observe the stop.
			select {
			case <-gate:
			default:
				close(gate)
			}
		}
	})
	return prov, gate
}

// waitForModelCall blocks until the model has streamed at least once and the
// agent manager reports itself busy — the window in which steering applies.
func (s *webCommandSession) waitForModelCall(t *testing.T, prov *mock.Provider) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if prov.Calls(webUIModelID) > 0 && s.sc.app.subs.agentMgr.IsBusy() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the agent never became busy mid-turn")
}

// A clarify card is a chat card (options route through a selector overlay), so
// it must paint into the same grid the browser reads.
func TestWebUI_ClarifyCardRendersFromBrowserSession(t *testing.T) {
	s := newWebCommandSession(t)

	s.sc.chat.AddClarifyCard(tui.NewClarifyCard(
		"Which database?", "The task needs a store.", "Pick one:", []string{"postgres", "sqlite"}))
	s.render()

	s.mustContain(t, "Which database?")
	s.mustContain(t, "postgres")
	s.mustContain(t, "sqlite")
}
