// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/tooltracker"
	"github.com/pijalu/goa/tui"
)

// TestSessionReplay_MascotNeverRedrawn replays a REAL recorded session's
// event stream through the production render path (header + chat viewport +
// tooltracker + compositor) and asserts that after the header/mascot has
// scrolled off screen, no emitted write EVER paints mascot bytes into the
// visible window again — the Mascot/logo redraw:regression
// (mascot + empty screen flashing mid-session during tool calls).
//
// The replay mirrors App.handleAgentOutputEvent's semantics: content events
// become chat messages, tool_call/progress/result events flow through the
// same tooltracker.Tracker the app uses (widgets attached to the chat
// viewport), and every frame is rendered through the real compositor into a
// recording terminal.
//
// The default fixture is internal/app/testdata/export/events.jsonl. Point
// GOA_REPLAY_EVENTS at a full session export (e.g. the 91K-event frigolite
// dump) for the deep pass.
func TestSessionReplay_MascotNeverRedrawn(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow integration replay in -short mode")
	}
	// The replay renders every one of 8000 events through the full compositor;
	// under -race the synchronized renderer makes this exceed the 30s unit-test
	// timeout. The test still runs in normal (non-race) and CI long modes.
	if isRaceDetector() {
		t.Skip("skipping under -race: 8000-event full-render replay exceeds 30s timeout (takes ~43s)")
	}
	path := os.Getenv("GOA_REPLAY_EVENTS")
	if path == "" {
		path = "testdata/export/events.jsonl"
	}
	f, err := os.Open(path)
	if err != nil {
		t.Skipf("no replay fixture at %s: %v", path, err)
	}
	defer f.Close()

	const w, h = 100, 24
	term := &testTerminal{w: w, h: h}
	engine := tui.NewTUI(term)
	header := tui.NewHeader("goa", "test")
	chat := tui.NewChatViewport()
	status := tui.NewStatusMsg()
	inp := tui.NewEditor()
	footer := tui.NewFooter()

	engine.AddChild(header)
	engine.AddChild(chat)
	engine.AddChild(status)
	engine.AddChild(inp)
	engine.AddChild(footer)
	engine.SetFocus(inp)
	status.SetTUI(engine)
	inp.SetTUI(engine)

	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer engine.Stop()
	engine.RenderNow()

	// The tracker mirrors App.toolTracker(): widgets attach to the chat.
	tracker := tooltracker.New(func(name, input string) *tui.ToolExecutionComponent {
		return chat.AddToolExecution(name, input)
	})

	line, headerOff := replaySession(t, f, engine, chat, tracker, term, w, h)
	if !headerOff {
		t.Fatalf("fixture never scrolled the header off screen (chat height %d <= %d) — replay cannot validate mascot redraw", chat.TotalHeight(), h)
	}
	t.Logf("replayed %d events, %d writes, header scrolled off cleanly, mascot never repainted", line, len(term.writes))
}

type replayRunner struct {
	engine        *tui.TUI
	chat          *tui.ChatViewport
	tracker       *tooltracker.Tracker
	term          *testTerminal
	width, height int
	headerOff     bool
	resized       int
	state         replayState
}

func replaySession(t *testing.T, file *os.File, engine *tui.TUI, chat *tui.ChatViewport, tracker *tooltracker.Tracker, term *testTerminal, width, height int) (int, bool) {
	runner := &replayRunner{engine: engine, chat: chat, tracker: tracker, term: term, width: width, height: height}
	runner.state = replayState{chat: chat, tracker: tracker}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4<<20), 4<<20)
	line := 0
	for scanner.Scan() {
		line++
		if runner.apply(scanner.Bytes()) {
			runner.render(line)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return line, runner.headerOff
}

func (r *replayRunner) apply(data []byte) bool {
	var event agentic.OutputEvent
	if json.Unmarshal(data, &event) != nil || !r.state.apply(&event) {
		return false
	}

	return true
}

func (r *replayRunner) render(line int) {
	r.engine.RenderNow()
	if r.resized == 0 && r.headerOff && r.state.running != nil {
		r.resized++
		r.term.h = r.height + 6
		r.engine.RenderNow()
	}
	if r.resized == 1 && r.state.running == nil {
		r.resized++
		r.term.h = r.height
		r.engine.RenderNow()
	}
	if !r.headerOff && r.chat.TotalHeight() > r.height {
		r.headerOff = true
	}
	if r.headerOff {
		r.assertMascotAbsent(line)
	}
}

func (r *replayRunner) assertMascotAbsent(line int) {
	write := r.term.writes[len(r.term.writes)-1]
	if strings.Contains(write, "⬡⬡⬡⬡") {
		panic(fmt.Sprintf("line %d: mascot bytes repainted", line))
	}
}

type replayState struct {
	chat          *tui.ChatViewport
	tracker       *tooltracker.Tracker
	assistantOpen bool
	running       *tui.ToolExecutionComponent
}

func (s *replayState) apply(ev *agentic.OutputEvent) bool {
	switch ev.Type {
	case agentic.EventContent:
		return s.content(ev)
	case agentic.EventToolCall:
		tc, _ := s.tracker.OnCall(ev)
		if !ev.IsDelta && tc != nil {
			tc.SetStatus(tui.ToolRunning)
			s.running = tc
		}
		s.assistantOpen = false
	case agentic.EventToolProgress:
		s.tracker.OnProgress(ev)
	case agentic.EventToolResult:
		if ev.Text == "" {
			ev.Text = ev.ToolResult
		}
		s.tracker.OnResult(ev)
		s.running = nil
		s.assistantOpen = false
	default:
		return false
	}
	return true
}

func (s *replayState) content(ev *agentic.OutputEvent) bool {
	switch ev.Role {
	case agentic.User:
		s.chat.AddUserMessage(ev.Text)
		s.assistantOpen = false
	case agentic.System:
		s.chat.AddSystemMessage(ev.Text)
		s.assistantOpen = false
	case agentic.Assistant:
		if !s.assistantOpen {
			s.chat.AddAssistantMessage("")
			s.assistantOpen = true
		}
		s.chat.UpdateLastMessage(ev.Text, tui.ConsoleAssistantMessage)
	default:
		return false
	}
	return true
}
