// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"bufio"
	"encoding/json"
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
// visible window again — the bugs.md "Mascot/logo redraw" regression
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

	// Marker bytes unique to the mascot/logo art. The logo is pure ⬡ block
	// art; a long run of the hexagon glyph cannot appear in chat content.
	const mascotMarker = "⬡⬡⬡⬡"

	// headerOff flips true once the header has scrolled out of the visible
	// window (enough content emitted that canvasLen > h).
	headerOff := false
	assistantOpen := false
	resized := 0
	var tcRunning *tui.ToolExecutionComponent // last widget that entered Running

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 4<<20), 4<<20)
	line := 0
	for sc.Scan() {
		line++
		var ev agentic.OutputEvent
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			continue
		}
		switch ev.Type {
		case agentic.EventContent:
			switch {
			case ev.Role == agentic.User:
				chat.AddUserMessage(ev.Text)
				assistantOpen = false
			case ev.Role == agentic.System:
				chat.AddSystemMessage(ev.Text)
				assistantOpen = false
			case ev.Role == agentic.Assistant:
				if !assistantOpen {
					chat.AddAssistantMessage("")
					assistantOpen = true
				}
				chat.UpdateLastMessage(ev.Text, tui.ConsoleAssistantMessage)
			}
		case agentic.EventToolCall:
			tc, _ := tracker.OnCall(&ev)
			// Mirror App.handleToolCall: a FINAL (non-delta) call transitions
			// its widget to Running (stats.go:574-576) — the state change that
			// drives the live repaint ticker during tool calls.
			if !ev.IsDelta && tc != nil {
				tc.SetStatus(tui.ToolRunning)
				tcRunning = tc
			}
			assistantOpen = false
		case agentic.EventToolProgress:
			tracker.OnProgress(&ev)
		case agentic.EventToolResult:
			if ev.Text == "" {
				ev.Text = ev.ToolResult
			}
			// The tracker's onResult applies the terminal status (Success/
			// Error from the text heuristic) — the second state change.
			tracker.OnResult(&ev)
			tcRunning = nil
			assistantOpen = false
		default:
			continue // stats/progress/context events don't touch the chat
		}

		engine.RenderNow()

		// Inject a resize right after a tool call enters Running — the
		// tab-switch-during-tool-call trigger from the bug report that the
		// event stream itself never contains. The resize frame goes through
		// the full-repaint path (resized || hasOverlay), the historical
		// mascot-flash route. Height-only so the scrollback stays valid.
		if resized == 0 && headerOff && tcRunning != nil {
			resized++
			term.h = h + 6 // grow; shrink back on the next tool transition
			engine.RenderNow()
		} else if resized == 1 && tcRunning == nil {
			resized++
			term.h = h
			engine.RenderNow()
		}

		// Detect when the header has left the visible window: once the
		// chat's transcript alone exceeds the screen, the header (rows 0..n)
		// is in scrollback and must never be repainted.
		if !headerOff && chat.TotalHeight() > h {
			headerOff = true
		}
		if headerOff {
			wr := term.writes[len(term.writes)-1]
			if strings.Contains(wr, mascotMarker) {
				t.Fatalf("line %d (%s %s): mascot bytes repainted after the header scrolled off (resized=%d).\nwrite: %q",
					line, ev.Type, ev.ToolName, resized, wr[:minInt(400, len(wr))])
			}
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !headerOff {
		t.Fatalf("fixture never scrolled the header off screen (chat height %d <= %d) — replay cannot validate mascot redraw", chat.TotalHeight(), h)
	}
	t.Logf("replayed %d events, %d writes, header scrolled off cleanly, mascot never repainted", line, len(term.writes))
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
