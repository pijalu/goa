// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/attach"
	"github.com/pijalu/goa/internal/webui"
	"github.com/pijalu/goa/tui"
)

// The attach path, validated at the UI-STATE level (the filmstrip approach):
// a real component tree renders a scripted agent turn into the virtual
// terminal; every frame the hub ships goes through the REAL attach renderer
// (the `goa attach` client's screen) and is replayed into a terminal
// emulator. After every filmstrip step the reconstructed terminal must show
// exactly what the server's own screen shows, and every UI state the
// filmstrip records — the status spinner, the tool widget, the answer — must
// be visible on the wire side too. Asserted as text and DOM-level facts, not
// escape bytes.

// wireCapture is a cells-plane hub client that records payloads in ship
// order, standing in for the attach client's socket.
type wireCapture struct {
	payloads []*webui.Payload
	closed   bool
}

func (c *wireCapture) Send(p *webui.Payload) bool {
	c.payloads = append(c.payloads, p)
	return true
}

func (c *wireCapture) SendControl(ctrl webui.Control) error { return nil }
func (c *wireCapture) Close() error {
	c.closed = true
	return nil
}

// drain hands over everything shipped so far and resets the buffer, exactly
// what a client's read loop consumes between two polls.
func (c *wireCapture) drain() []*webui.Payload {
	out := c.payloads
	c.payloads = nil
	return out
}

// wireMirror is the attach client's receiving side, reduced to what the test
// asserts on: the renderer (Screen) writing ANSI, and the terminal emulator
// those bytes land in. Its screen text is the wire-side view of the session.
type wireMirror struct {
	screen *attach.Screen
	emu    *tui.TermEmulator
	rows   int
	ansi   strings.Builder
	replay int // bytes already fed to the emulator
	codec  *webui.FrameCodec
}

func newWireMirror(cols, rows int) *wireMirror {
	m := &wireMirror{
		emu:  tui.NewTermEmulator(rows, cols),
		rows: rows,
	}
	m.screen = attach.NewScreen(&ansiWriter{&m.ansi}, cols, rows)
	m.screen.Start()
	return m
}

// ansiWriter adapts *strings.Builder to the renderer's writer interface.
type ansiWriter struct{ b *strings.Builder }

func (w *ansiWriter) WriteString(s string) { w.b.WriteString(s) }

// consume applies newly shipped payloads exactly as the client's read loop
// does — a frame renders the moment it arrives; its transcript batch (the
// next wire message) scrolls and repaints — then replays the fresh bytes
// into the emulator.
func (m *wireMirror) consume(payloads []*webui.Payload) {
	for _, p := range payloads {
		if p.Frame != nil {
			m.screen.Frame(p.Frame, nil)
		}
		if len(p.Scrollback) > 0 {
			if _, batch, err := m.codec.DecodeScrollback(p.Scrollback); err == nil {
				m.screen.Scrollback(batch)
			}
		}
	}
	fresh := m.ansi.String()[m.replay:]
	if fresh == "" {
		return
	}
	m.replay += len(fresh)
	m.emu.Process(fresh)
}

// visibleText is the emulator's screen as plain text, one trimmed row per
// line — the same shape as the server grid's Text() mirror.
func (m *wireMirror) visibleText() string {
	var b strings.Builder
	for r := 0; r < m.rows; r++ {
		b.WriteString(strings.TrimRight(m.emu.Visible(r), " "))
		b.WriteByte('\n')
	}
	return b.String()
}

// The filmstrip step list for a complete scripted turn, in the order the
// agent SDK emits it.
func scriptedTurnEvents() []*agentic.OutputEvent {
	return []*agentic.OutputEvent{
		{Type: agentic.EventStateChange, State: agentic.StateThinking},
		{Type: agentic.EventContent, Role: agentic.Assistant, State: agentic.StateThinking, Text: "reading the file first", IsDelta: true},
		{Type: agentic.EventToolCall, State: agentic.StateToolCall, ToolName: "read", ToolInput: `{"path":"notes.txt"}`, ToolCallID: "c1"},
		{Type: agentic.EventToolResult, State: agentic.StateToolResult, ToolName: "read", ToolCallID: "c1", Text: "the notes body"},
		{Type: agentic.EventContent, Role: agentic.Assistant, State: agentic.StateContent, Text: "The notes say hello.", IsDelta: true},
		// A long answer: forces the server grid to scroll, so the turn also
		// exercises the transcript batches through the wire (native
		// scrollback on the attached terminal).
		{Type: agentic.EventContent, Role: agentic.Assistant, State: agentic.StateContent, Text: longAnswer(), IsDelta: true},
		{Type: agentic.EventEnd},
	}
}

// longAnswer is 40 lines: on a 30-row screen the turn must scroll.
func longAnswer() string {
	var b strings.Builder
	for i := 1; i <= 40; i++ {
		b.WriteString("answer line ")
		b.WriteString(strings.Repeat("x", i%7))
		b.WriteByte('\n')
	}
	return b.String()
}

func TestAttachFilmstrip_WireRendersServerStates(t *testing.T) {
	const cols, rows = 100, 30
	vt := webui.NewVirtualTerminal(cols, rows)
	hub := webui.NewHub(0)
	vt.SetSink(hub)
	client := &wireCapture{}
	hub.Attach(client, webui.PlaneCells)

	// The production component tree, rendering into the virtual terminal:
	// what the engine draws is what the wire ships.
	sc := newUIScenarioTerm(t, vt, cols, rows)
	mirror := newWireMirror(cols, rows)
	trace := driveScriptedTurn(t, sc, mirror, client, vt, scriptedTurnEvents())

	assertSpinnerLifecycle(t, trace)
	assertWireContent(t, mirror)
}

// driveScriptedTurn applies each event, drains the wire, and asserts — at
// every filmstrip step — that the reconstructed terminal matches the server
// screen and shows the step's own UI state. Returns the spinner trace.
func driveScriptedTurn(t *testing.T, sc *uiScenario, mirror *wireMirror, client *wireCapture, vt *webui.VirtualTerminal, events []*agentic.OutputEvent) []string {
	t.Helper()
	trace := make([]string, 0, len(events))
	for i, ev := range events {
		snap := sc.apply(ev)
		trace = append(trace, snap.Diff.StatusText)
		mirror.consume(client.drain())

		// Per-step semantic parity: the reconstructed terminal's visible
		// text equals the server grid's own text mirror. Compared line by
		// line so a failure names the row.
		assertWireMatchesGrid(t, i, snap.Label, mirror, vt)

		// Every line the filmstrip says this step ADDED to the screen must
		// be visible through the attach renderer: the filmstrip's view of
		// the UI state is the wire's view too.
		assertAddedLinesOnWire(t, i, snap, mirror)
	}
	return trace
}

// assertSpinnerLifecycle is the canonical turn invariant: the spinner is
// alive at every mid-turn step and cleared at the true end, with each phase
// of the turn represented.
func assertSpinnerLifecycle(t *testing.T, trace []string) {
	t.Helper()
	if last := trace[len(trace)-1]; last != "" {
		t.Errorf("final status = %q, want the spinner cleared after EventEnd", last)
	}
	for i, s := range trace[:len(trace)-1] {
		if s == "" {
			t.Errorf("step %d: spinner went dark mid-turn; trace=%v", i, trace)
		}
	}
	for _, want := range []string{"Thinking...", "Tool calling", "Answering..."} {
		if !traceContains(trace, want) {
			t.Errorf("status trace %v never showed %q", trace, want)
		}
	}
}

func traceContains(trace []string, want string) bool {
	for _, s := range trace {
		if strings.Contains(s, want) {
			return true
		}
	}
	return false
}

// assertWireContent pins the turn's semantics on the wire: the answer tail
// on the visible screen, earlier content in NATIVE scrollback.
func assertWireContent(t *testing.T, mirror *wireMirror) {
	t.Helper()
	if final := mirror.visibleText(); !strings.Contains(final, "answer line") {
		t.Errorf("final wire screen missing the answer tail; screen:\n%s", final)
	}
	var scrollText strings.Builder
	for _, row := range mirror.emu.Scrollback() {
		scrollText.WriteString(strings.TrimRight(row, " "))
		scrollText.WriteByte('\n')
	}
	for _, want := range []string{"The notes say hello.", "notes.txt"} {
		if !strings.Contains(scrollText.String(), want) {
			t.Errorf("wire scrollback missing %q; scrollback:\n%s", want, scrollText.String())
		}
	}
	if len(mirror.emu.Scrollback()) == 0 {
		t.Error("wire terminal accumulated no native scrollback during the turn")
	}
}

// assertAddedLinesOnWire checks the filmstrip step's own additions.
func assertAddedLinesOnWire(t *testing.T, step int, snap tui.Snapshot, mirror *wireMirror) {
	t.Helper()
	screen := mirror.visibleText()
	for _, line := range snap.Diff.AddedLines {
		line = strings.TrimSpace(line)
		if line == "" || isChromeLine(line) {
			continue
		}
		if !strings.Contains(screen, line) {
			t.Errorf("step %d (%s): filmstrip added %q but the wire does not show it", step, snap.Label, line)
		}
	}
}

// isChromeLine skips the structural lines every frame carries (the logo's
// block art, the footer's box drawing): they are parity-checked line by line
// by assertWireMatchesGrid anyway.
func isChromeLine(line string) bool {
	for _, r := range line {
		if r == '▄' || r == '█' || r == '▀' || r == '─' || r == '│' || r == '╭' || r == '╰' {
			return true
		}
	}
	return false
}

// assertWireMatchesGrid compares the reconstructed terminal against the
// server grid line by line (trimmed right), with a readable two-sided dump
// on failure.
func assertWireMatchesGrid(t *testing.T, step int, label string, mirror *wireMirror, vt *webui.VirtualTerminal) {
	t.Helper()
	want := vt.Grid().Text()
	got := mirror.visibleText()
	wantLines := strings.Split(strings.TrimRight(want, "\n"), "\n")
	gotLines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(wantLines) != len(gotLines) {
		t.Fatalf("step %d (%s): wire shows %d rows, server has %d\n--- server ---\n%s\n--- wire ---\n%s",
			step, label, len(gotLines), len(wantLines), want, got)
	}
	for r := range wantLines {
		if strings.TrimRight(wantLines[r], " ") != strings.TrimRight(gotLines[r], " ") {
			t.Fatalf("step %d (%s): row %d differs\nserver: %q\nwire:   %q", step, label, r, wantLines[r], gotLines[r])
		}
	}
}
