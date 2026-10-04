// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands"
	"github.com/pijalu/goa/internal/webui"
	"github.com/pijalu/goa/tui"
)

// B2 — a long output must render every line exactly once.
//
// The browser is a transcript list (the rows that scrolled off the live screen)
// followed by the live grid. These scenarios model exactly that from the wire:
// the row patches are applied to a grid model and the transcript batches are
// appended, honoring the server's statement that a batch REPLACES the transcript
// (the compositor wipes its scrollback and re-emits the whole history at a new
// width — a terminal sees that as CSI 3J, a wipe, not an append).
//
// The corruption the reported screenshot shows: the lines already shipped as
// transcript came through a second time, so the page showed the same output two
// or three times over. Both directions are asserted here: a line that appears
// twice fails, and a line that never appears fails (the rows must not be lost
// either).

// b2Page is the browser's model: the transcript plus the live grid, rebuilt
// purely from the wire.
type b2Page struct {
	rows       int
	grid       []string
	transcript []string
}

func newB2Page(rows int) *b2Page {
	p := &b2Page{rows: rows}
	p.resetGrid(rows)
	return p
}

func (p *b2Page) resetGrid(rows int) {
	p.rows = rows
	p.grid = make([]string, rows)
	for i := range p.grid {
		p.grid[i] = " "
	}
}

// publish applies one frame the way app.js does.
func (p *b2Page) publish(f *webui.Frame) {
	if f.Full || f.Rows != p.rows {
		p.resetGrid(f.Rows)
	}
	for _, patch := range f.Patches {
		if patch.Row < 0 || patch.Row >= len(p.grid) {
			continue
		}
		p.grid[patch.Row] = webui.RunsText(patch.Runs)
	}
	if f.ScrollbackReplace {
		p.transcript = nil
	}
	for _, row := range f.Scrollback {
		p.transcript = append(p.transcript, webui.RunsText(row.Runs))
	}
}

// page returns the whole page top to bottom: the transcript, then the live grid.
func (p *b2Page) page() []string {
	out := make([]string, 0, len(p.transcript)+len(p.grid))
	out = append(out, p.transcript...)
	out = append(out, p.grid...)
	return out
}

// b2RecordingSink feeds every published frame to a page model.
type b2RecordingSink struct {
	page *b2Page
}

func (s *b2RecordingSink) Publish(f *webui.Frame) { s.page.publish(f) }
func (s *b2RecordingSink) HasClients() bool       { return true }

// b2PageOfSession wires a web session whose browser-side model records every
// frame, the way an attached page does.
func b2PageOfSession(t *testing.T, cols, rows int) (*webCommandSession, *b2Page) {
	t.Helper()
	page := newB2Page(rows)
	s := newWebCommandSessionSink(t, &b2RecordingSink{page: page}, cols, rows)
	return s, page
}

// b2CheckPage fails unless every marker occurs exactly once on the page, in
// ascending order — the invariant a long output has to satisfy. Rows are matched
// loosely (substring, so the box-wrapped help list still matches), and only the
// markers the scenario generated are asserted: the page legitimately repeats
// decoration (box borders) and its own header.
func b2CheckPage(t *testing.T, page []string, markers []string) {
	t.Helper()
	dump := func() string { return "\n--- page ---\n" + strings.Join(page, "\n") }
	seenAt, order := b2ScanPage(t, page, markers, dump)
	for _, m := range markers {
		if seenAt[m] == 0 {
			t.Errorf("marker %q is missing from the page%s", m, dump())
		}
	}
	b2CheckOrder(t, order, dump)
}

// b2ScanPage records how often each marker occurs and where, failing on the
// first repeat of one.
func b2ScanPage(t *testing.T, page, markers []string, dump func() string) (map[string]int, []int) {
	t.Helper()
	seenAt := map[string]int{}
	var order []int
	for i, row := range page {
		for _, m := range markers {
			if !strings.Contains(row, m) {
				continue
			}
			seenAt[m]++
			if seenAt[m] > 1 {
				t.Errorf("marker %q appears %d times on the page (row %d): every line must be exactly once%s",
					m, seenAt[m], i, dump())
			}
			order = append(order, i)
		}
	}
	return seenAt, order
}

// b2CheckOrder fails unless the markers come out in the order they were
// produced: the page is a log, not a set.
func b2CheckOrder(t *testing.T, order []int, dump func() string) {
	t.Helper()
	for i := 1; i < len(order); i++ {
		if order[i] < order[i-1] {
			t.Fatalf("page rows are out of order at %d < %d%s", order[i], order[i-1], dump())
		}
	}
}

// b2Markers builds the unique content markers the scenario looks for: each row
// it adds carries its own index, so a duplicate is impossible to confuse with a
// legitimate repeat.
func b2Markers(prefix string, n int) []string {
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, fmt.Sprintf("%s%03d", prefix, i))
	}
	return out
}

// TestWebUI_LongOutputRendersEachLineOnce is bugs.md B2. The browser attaches,
// reports its own geometry (the server's startup geometry differs, and that
// geometry change is what makes the compositor re-emit the transcript), and then
// a long command output arrives. Every line must appear exactly once — with the
// transcript holding the scrolled-off rows and the grid the live screen.
func TestWebUI_LongOutputRendersEachLineOnce(t *testing.T) {
	const serverCols, serverRows = 120, 40
	const browserCols, browserRows = 123, 43

	s, page := b2PageOfSession(t, serverCols, serverRows)

	// Earlier output: long enough that the transcript is non-empty and the
	// session is bottom-anchored, which is the state a real session is in.
	const historyRows = 60
	for i := 0; i < historyRows; i++ {
		s.sc.chat.AddSystemMessage(fmt.Sprintf("history row %03d", i))
	}
	s.render()

	// The page reports its window geometry. This is a WIDTH change, so the
	// compositor wipes the terminal scrollback and re-emits the whole transcript
	// at the new width.
	s.vt.Resize(browserCols, browserRows)
	s.render()

	// The long output, typed through the production key path.
	for i := 0; i < 60; i++ {
		cmd := &scriptedCommand{
			name: fmt.Sprintf("cmd%02d", i),
			help: fmt.Sprintf("synthetic command %02d with a description long enough to wrap", i),
			ran:  s.ranCmd,
		}
		if err := s.sc.app.subs.registry.Register(cmd); err != nil {
			t.Fatalf("register /%s: %v", cmd.name, err)
		}
	}
	s.submit(t, "/help")
	s.render()

	markers := append(b2Markers("history row ", historyRows), helpMarkers(60)...)
	b2CheckPage(t, page.page(), markers)
}

// TestWebUI_LongOutputDuringStreamRendersEachLineOnce is the reported scenario
// itself: /quota (a long output) typed while the assistant block is still being
// written. The stream keeps mutating a block ABOVE the appended table, which is
// the one layout that makes the compositor re-emit its whole transcript — and a
// browser that appends that re-emit paints the session twice.
func TestWebUI_LongOutputDuringStreamRendersEachLineOnce(t *testing.T) {
	const cols, rows = 120, 40

	s, page := b2PageOfSession(t, cols, rows)
	chat := s.sc.chat

	// Earlier conversation: unique lines, so a repeat cannot be mistaken for a
	// legitimate one.
	const historyRows = 30
	for i := 0; i < historyRows; i++ {
		chat.AddSystemMessage(fmt.Sprintf("history row %03d", i))
	}
	s.render()

	// A streaming assistant block: the production UpdateLastMessage path.
	chat.AddAssistantMessage("")
	stream := []string{}
	chunks := func(n int) {
		for i := 0; i < n; i++ {
			stream = append(stream, fmt.Sprintf("%d. stream%03d", len(stream)+1, len(stream)))
			chat.UpdateLastMessage(strings.Join(stream, "\n"), tui.ConsoleAssistantMessage)
			s.render()
		}
	}
	chunks(6)

	// The long output lands mid-stream, typed through the production key path.
	for i := 0; i < 40; i++ {
		cmd := &scriptedCommand{
			name: fmt.Sprintf("q%02d", i),
			help: fmt.Sprintf("quota row %02d for a provider with a fairly long name", i),
			ran:  s.ranCmd,
		}
		if err := s.sc.app.subs.registry.Register(cmd); err != nil {
			t.Fatalf("register /%s: %v", cmd.name, err)
		}
	}
	s.submit(t, "/help")

	// The stream resumes after the table, then settles.
	chunks(8)
	s.render()

	markers := append(b2Markers("history row ", historyRows), quotaMarkers(40)...)
	markers = append(markers, fmt.Sprintf("stream%03d", len(stream)-1))
	b2CheckPage(t, page.page(), markers)
}

// TestWebUI_RealCommandHelpAppearsOncePerRun repeats the reported flow with the
// registry the shipped binary actually registers — the real command list, typed
// as keys, with the autocomplete popup open while typing — twice, with a geometry
// change in between. Every list line must appear exactly twice: once per run.
// Fewer means rows were lost, more means a re-emit was appended instead of
// replacing.
func TestWebUI_RealCommandHelpAppearsOncePerRun(t *testing.T) {
	page := newB2Page(43)
	vt := webui.NewVirtualTerminal(123, 43)
	vt.SetSink(&b2RecordingSink{page: page})
	sc := newUIScenarioTerm(t, vt, 123, 43)
	s := &webCommandSession{sc: sc, vt: vt, ranCmd: new(bool)}

	registry := core.NewCommandRegistry()
	if err := commands.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}
	sc.app.subs.registry = registry
	sc.app.subs.cmdRouter = core.NewCommandRouter(registry, core.NewDocEngine(registry))
	sc.app.subs.sessionStore = nil
	sc.editor.SetOnSubmit(sc.app.makeSubmitHandler(sc.engine, sc.chat))
	sc.app.configureInputEditor(sc.editor, sc.engine)
	sc.engine.SetFocus(sc.editor)

	s.submit(t, "/help")
	s.render()
	first := len(b2ListCounts(page.page()))
	if first == 0 {
		t.Fatal("/help printed no command list")
	}

	// The page reports a new geometry: a WIDTH change, so the compositor wipes
	// its scrollback and re-emits the whole transcript.
	vt.Resize(137, 43)
	s.render()

	s.submit(t, "/help")
	s.render()

	missing, extra := []string{}, []string{}
	for name, n := range b2ListCounts(page.page()) {
		switch {
		case n < 2:
			missing = append(missing, fmt.Sprintf("%s x%d", name, n))
		case n > 2:
			extra = append(extra, fmt.Sprintf("%s x%d", name, n))
		}
	}
	if len(missing) > 0 || len(extra) > 0 {
		t.Fatalf("two /help runs must put every list line on the page twice: missing=%v extra=%v", missing, extra)
	}
}

// b2ListCounts counts how often each /help list line appears on the page.
func b2ListCounts(rows []string) map[string]int {
	out := map[string]int{}
	for _, r := range rows {
		if !strings.Contains(r, "• /") {
			continue
		}
		part := strings.SplitN(r, "• ", 2)[1]
		name := strings.TrimSpace(strings.SplitN(part, " —", 2)[0])
		out[name]++
	}
	return out
}

// helpMarkers names the rows /help prints for the registered commands.
func helpMarkers(n int) []string {
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, fmt.Sprintf("/cmd%02d", i))
	}
	return out
}

// quotaMarkers names the rows the synthetic /quota-like command list prints.
func quotaMarkers(n int) []string {
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, fmt.Sprintf("/q%02d", i))
	}
	return out
}
