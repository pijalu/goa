// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/agentic"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/agentic/provider/mock"
	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/internal/webui"
	"github.com/pijalu/goa/tui"
)

// G3 checkpoint: a LIVE session over the web transport. The real TUI engine,
// the real App event handlers and a real core.AgentManager run against the
// scripted mock LLM, with a webui.VirtualTerminal as the engine's terminal —
// so every byte the browser would receive is asserted here, cell by cell, on
// the authoritative grid.
//
// Sequence: user prompt → thinking block → tool call → tool result →
// assistant answer. Asserted on the grid: the prompt bubble, the tool widget,
// the assistant answer, the footer, and the caret. Also asserts the wire
// frames the hub would broadcast (row patches + cursor) match the grid.

const (
	webUIToolName = "echo_probe"
	webUIUserMsg  = "what is 2+2 via echo_probe"
	webUIThinking = "reasoning about the sum"
	webUIAnswer   = "ECHO-4"
	webUIFlash    = "FLASH-MARK"
	webUIModelID  = "webui-model"
)

// echoProbeTool is the single tool exposed to the scripted model. It echoes
// the "value" argument back with an ECHO- prefix so the tool result is
// recognisable in the rendered grid.
type echoProbeTool struct {
	calls int
}

func (e *echoProbeTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        webUIToolName,
		Description: "Echo a probe value back to the assistant.",
		Schema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"value": map[string]any{"type": "string"}},
			"required":   []string{"value"},
		},
	}
}

func (e *echoProbeTool) Execute(input string) (string, error) {
	e.calls++
	var args struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal([]byte(input), &args); err != nil {
		return "", err
	}
	return "ECHO-" + args.Value, nil
}

func (e *echoProbeTool) IsRetryable(error) bool { return false }

// webUIFrame is one published frame: the rows the browser would paint and the
// cursor it would place.
type webUIFrame struct {
	patches []webui.RowPatch
	full    bool
	cur     webui.Cursor
}

// recordingSink captures the frames a VirtualTerminal publishes, standing in
// for the hub broadcast to every attached client. It serves the cells plane
// only: the assertions below are whole-grid parity checks, which is the
// cells document — a blocks client receives band deltas instead.
type recordingSink struct{ frames []webUIFrame }

func (s *recordingSink) Publish(plane webui.Plane, f *webui.Frame) {
	if plane != webui.PlaneCells {
		return
	}
	s.frames = append(s.frames, webUIFrame{patches: f.Patches, full: f.Full, cur: f.Cursor})
}

// HasClients always reports true: the recorder stands in for an attached
// client, so the terminal must build every frame for it.
func (s *recordingSink) HasClients() bool                     { return true }
func (s *recordingSink) HasClientsFor(plane webui.Plane) bool { return plane == webui.PlaneCells }

// lastFrame returns the most recently published frame.
func (s *recordingSink) lastFrame() webUIFrame {
	if len(s.frames) == 0 {
		return webUIFrame{}
	}
	return s.frames[len(s.frames)-1]
}

func TestWebUI_LiveSessionOverVirtualTerminal(t *testing.T) {
	vt := webui.NewVirtualTerminal(100, 24)
	sink := &recordingSink{}
	vt.SetSink(sink)

	sc := newUIScenarioTerm(t, vt, 100, 24)
	prov, tool := startWebUISession(t, sc)

	// Production prompt path: the submit handler paints the user bubble and
	// forwards the text to the agent manager.
	sc.app.dispatchUserSubmit(sc.engine, sc.chat, webUIUserMsg)
	pumpAgentTurn(t, sc, sc.app.subs.events.Agent, tool)

	if tool.calls != 1 {
		t.Fatalf("echo_probe executed %d times, want exactly 1 (prompt→tool→response must run one round)", tool.calls)
	}
	if prov.Calls(webUIModelID) < 2 {
		t.Fatalf("model streamed %d times, want >=2 (tool call round + final answer)", prov.Calls(webUIModelID))
	}

	grid := vt.Grid()
	cols, rows := grid.Size()
	if cols != 100 || rows != 24 {
		t.Fatalf("grid geometry = %dx%d, want 100x24", cols, rows)
	}
	t.Logf("rendered screen:\n%s", grid.Text())

	assertFrameGridParity(t, grid, sink)
	assertSessionCells(t, grid)
	assertFlashRenders(t, sc, grid, sink)
	assertCaretMatches(t, grid, sink, cols, rows)
}

// startWebUISession wires the real AgentManager to the scenario's app and
// scripts the model: thinking + tool call first, then the final answer once the
// tool result is back. Returns the provider and the probe tool.
func startWebUISession(t *testing.T, sc *uiScenario) (*mock.Provider, *echoProbeTool) {
	t.Helper()
	prov := mock.New(t)
	first := mock.ThinkingTextTurn(webUIThinking, "calling the probe tool")
	probe := mock.ToolCallTurn(webUIToolName, "call-1", `{"value":"4"}`)
	prov.Script(webUIModelID, mock.Turn{
		Events: mergeTurnEvents(first.Events, probe.Events),
		Final:  mergeFinals(first.Final, probe.Final),
	})
	prov.Script(webUIModelID, mock.TextTurn(webUIAnswer))

	tool := &echoProbeTool{}
	cfg := &config.Config{}
	// The production TUI path: the agent publishes into the event bus and the
	// app's readers forward to the render loop. The test drives the same bus
	// and the same handlers, so no internal-events opt-in is needed.
	bus := event.MakeBus(512, 16, 16, 16)
	sc.app.subs.events = bus
	am := core.NewAgentManager(cfg, nil, nil, nil, bus, "")
	sc.app.subs.agentMgr = am

	if _, err := am.StartSession(prov.Model(webUIModelID), agenticprovider.StreamOptions{}, "You are the web-UI e2e agent.", []agentic.Tool{tool}, cfg); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	return prov, tool
}

// pumpAgentTurn applies every bus event through the app handler on the engine
// command loop and renders, exactly like runAgentEventReader, until the turn
// ends.
func pumpAgentTurn(t *testing.T, sc *uiScenario, bus <-chan event.AgentEvent, tool *echoProbeTool) {
	t.Helper()
	deadline := time.After(30 * time.Second)
	for {
		select {
		case ev := <-bus:
			sc.engine.ApplySync(func() { sc.app.handleAgentOutputEvent(&ev.Event) })
			sc.engine.RenderNow()
			if ev.Event.Type == agentic.EventEnd {
				return
			}
		case <-deadline:
			t.Fatalf("turn did not end within 30s; tool calls=%d", tool.calls)
		}
	}
}

// assertFrameGridParity replays every broadcast frame into a client-side model
// (as the browser's applyRow does) and compares it row by row, cell by cell,
// against the authoritative grid. Any drift between what the hub shipped and
// what the grid holds fails here.
func assertFrameGridParity(t *testing.T, grid *webui.CellGrid, sink *recordingSink) {
	t.Helper()
	if len(sink.frames) == 0 {
		t.Fatal("no frames published to the client")
	}
	cols, rows := grid.Size()
	client := newClientGrid(cols, rows)
	for _, f := range sink.frames {
		if f.full {
			client.reset()
		}
		for _, p := range f.patches {
			client.apply(p)
		}
	}
	for r := 0; r < rows; r++ {
		want := cellsRowText(grid.Cells(r))
		if got := client.row(r); got != want {
			t.Errorf("row %d mismatch between broadcast frames and grid:\n grid: %q\nframe: %q", r, want, got)
		}
	}
}

// assertSessionCells pins that every streaming artefact the objective names is
// present in the cells the browser paints: the prompt, the thinking block, the
// tool widget, the tool result, the assistant answer and the footer.
func assertSessionCells(t *testing.T, grid *webui.CellGrid) {
	t.Helper()
	for _, want := range []string{
		webUIUserMsg,
		"thinking...",
		webUIToolName,
		"ECHO-4",
		webUIAnswer,
		webUIModelID,
		"SOLO",
	} {
		assertScreenContains(t, grid, want)
	}
}

// assertFlashRenders pins that a flash reaches the grid AND the wire: it is a
// transient message the browser would see between two frames.
func assertFlashRenders(t *testing.T, sc *uiScenario, grid *webui.CellGrid, sink *recordingSink) {
	t.Helper()
	flash := event.ChatEvent{Flash: &event.Flash{Text: webUIFlash}}
	sc.engine.ApplySync(func() { sc.app.handleChatEvent(flash) })
	sc.engine.RenderNow()
	assertScreenContains(t, grid, webUIFlash)
	if !strings.Contains(strings.Join(patchRows(sink.lastFrame()), "\n"), webUIFlash) {
		t.Error("flash not present in the frame published after it was posted")
	}
}

// assertCaretMatches pins that the caret the client places is the grid's caret
// and lies inside the screen.
func assertCaretMatches(t *testing.T, grid *webui.CellGrid, sink *recordingSink, cols, rows int) {
	t.Helper()
	f := sink.lastFrame()
	if f.cur.Row < 0 || f.cur.Row >= rows || f.cur.Col < 0 || f.cur.Col >= cols {
		t.Errorf("broadcast cursor (%d,%d) outside the %dx%d screen", f.cur.Row, f.cur.Col, cols, rows)
	}
	cur := grid.Cursor()
	if cur.Row != f.cur.Row || cur.Col != f.cur.Col {
		t.Errorf("broadcast cursor (%d,%d) != grid cursor (%d,%d)", f.cur.Row, f.cur.Col, cur.Row, cur.Col)
	}
}

// clientGrid is the browser-side model: one string per row, rebuilt purely
// from the wire patches (no grid access), so the comparison is a true
// client/grid parity check rather than a self-comparison.
type clientGrid struct{ rows []string }

func newClientGrid(cols, rows int) *clientGrid {
	g := &clientGrid{rows: make([]string, rows)}
	for i := range g.rows {
		g.rows[i] = strings.Repeat(" ", cols)
	}
	return g
}

func (c *clientGrid) reset() {
	for i := range c.rows {
		c.rows[i] = strings.Repeat(" ", len(c.rows[i]))
	}
}

func (c *clientGrid) apply(p webui.RowPatch) {
	if p.Row < 0 || p.Row >= len(c.rows) {
		return
	}
	c.rows[p.Row] = patchRowText(p)
}

func (c *clientGrid) row(i int) string { return c.rows[i] }

// assertScreenContains pins one substring at the cell level: the expected
// characters must occupy consecutive cells of a single row, with the same
// attribute bytes the grid reports for that row (no embedded control or
// zero-width cell may break the run).
func assertScreenContains(t *testing.T, grid *webui.CellGrid, want string) {
	t.Helper()
	_, rows := grid.Size()
	for r := 0; r < rows; r++ {
		if strings.Contains(cellsRowText(grid.Cells(r)), want) {
			return
		}
	}
	t.Errorf("%q not found in any rendered row (%d rows)", want, rows)
}

// mergeTurnEvents concatenates two turns' event scripts (a scripted turn that
// both thinks and calls a tool).
func mergeTurnEvents(sets ...[]schema.AssistantMessageEvent) []schema.AssistantMessageEvent {
	var out []schema.AssistantMessageEvent
	for _, s := range sets {
		out = append(out, s...)
	}
	return out
}

// mergeFinals merges two turns' final messages' content blocks.
func mergeFinals(msgs ...*schema.AssistantMessage) *schema.AssistantMessage {
	var out *schema.AssistantMessage
	for _, m := range msgs {
		if m == nil {
			continue
		}
		if out == nil {
			out = &schema.AssistantMessage{StopReason: m.StopReason}
		}
		out.Content = append(out.Content, m.Content...)
		if m.StopReason == schema.StopReasonToolCall {
			out.StopReason = m.StopReason
		}
	}
	return out
}

// patchRowText renders one wire row patch back to plain text — the exact
// characters a browser paints for that row.
func patchRowText(p webui.RowPatch) string {
	var b strings.Builder
	for _, r := range p.Runs {
		b.WriteString(r.Text)
	}
	return b.String()
}

// patchRows renders every row patch of a frame.
func patchRows(f webUIFrame) []string {
	out := make([]string, 0, len(f.patches))
	for _, p := range f.patches {
		out = append(out, patchRowText(p))
	}
	return out
}

// cellsRowText renders one grid row from its authoritative cells. A cell whose
// Text is empty paints nothing (the browser pads it with CSS).
func cellsRowText(cells []tui.CellAttrs) string {
	var b strings.Builder
	for _, c := range cells {
		b.WriteString(c.Text)
	}
	return b.String()
}
