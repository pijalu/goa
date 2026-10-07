// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package attach connects a real terminal to a `goa server` session over
// WebSocket: the mirror image of the web UI. The browser is the terminal, so
// the terminal can be the browser — a `goa attach` client renders the same
// cell frames the page renders, and forwards raw local keystrokes to the very
// input path a TTY feeds. One engine, one screen model, one more surface.
//
// The client is deliberately dumb: it holds no agent state, decodes no keys
// (bytes go to the server verbatim) and re-renders nothing semantic. What it
// owns is the frame→ANSI direction: row patches of styled runs become SGR
// byte streams, scrolled-off transcript rows are pushed into the local
// terminal's native scrollback, and the server's cursor becomes the local
// caret.
package attach

import (
	"strconv"
	"strings"

	"github.com/pijalu/goa/internal/webui"
	"github.com/pijalu/goa/tui"
)

// ANSI control sequences the renderer emits. The set is deliberately tiny and
// universally supported: CUP, EL, ED, DECSTBM reset, SU-by-LF scrolling,
// DECTCEM, SGR and OSC 0/8. No alternate screen — the transcript must land in
// the terminal's native scrollback, which the alternate screen does not have.
const (
	sgrReset    = "\x1b[0m"
	eraseBelow  = "\x1b[K"
	clearScreen = "\x1b[2J\x1b[H"
	cursorHome  = "\x1b[H"
	hideCursor  = "\x1b[?25l"
	showCursor  = "\x1b[?25h"
	autoWrapOff = "\x1b[?7l"
	autoWrapOn  = "\x1b[?7h"
	regionReset = "\x1b[r"
	eraseScrol  = "\x1b[3J" // CSI 3 J — wipe scrollback, the terminal twin of TranscriptBatch.Replace
)

// oscTitle wraps a window title in OSC 0.
func oscTitle(title string) string { return "\x1b]0;" + title + "\x07" }

// oscLinkOpen / oscLinkClose bracket a hyperlink run (OSC 8).
func oscLinkOpen(url string) string { return "\x1b]8;;" + url + "\x1b\\" }
func oscLinkClose() string          { return "\x1b]8;;\x1b\\" }

// cup positions the cursor (1-based, terminal convention).
func cup(row, col int) string {
	return "\x1b[" + strconv.Itoa(row) + ";" + strconv.Itoa(col) + "H"
}

// pen is the SGR state the renderer's "pen" is currently in. Frames carry
// runs with absolute styles; emitting an SGR for every run would bloat the
// stream, so the renderer diffs this state run against run and writes only
// the change.
type pen struct {
	flags    tui.AttrFlags
	fg, bg   string
	link     string
	linkOpen bool
}

// sgrAttrs maps the emulator's attribute bits to their SGR codes, in the
// classic emission order.
var sgrAttrs = []struct {
	flag tui.AttrFlags
	code string
}{
	{tui.AttrBold, "1"},
	{tui.AttrDim, "2"},
	{tui.AttrItalic, "3"},
	{tui.AttrUnderline, "4"},
	{tui.AttrInverse, "7"},
	{tui.AttrStrike, "9"},
}

// sgr renders the escape sequence that puts the terminal in exactly p's
// attribute state. The sequence is absolute (SGR reset semantics per
// attribute) rather than incremental: a run carries its full style, and an
// absolute sequence cannot drift. The one state needing no sequence at all is
// the default pen.
func (p pen) sgr() string {
	if p.flags == 0 && p.fg == "" && p.bg == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("\x1b[")
	sep := false
	emit := func(s string) {
		if sep {
			b.WriteByte(';')
		}
		b.WriteString(s)
		sep = true
	}
	for _, a := range sgrAttrs {
		if p.flags&a.flag != 0 {
			emit(a.code)
		}
	}
	if p.fg == "" {
		emit("39")
	} else {
		emit("38;2;" + rgb(p.fg))
	}
	if p.bg == "" {
		emit("49")
	} else {
		emit("48;2;" + rgb(p.bg))
	}
	b.WriteByte('m')
	return b.String()
}

// rgb converts the server's CSS hex colour ("#1a2b3c") to the three decimal
// components a truecolor SGR parameter needs. The value came from the
// server's own SGR parse, so a malformed colour here means a server bug;
// black is the least surprising degradation.
func rgb(hex string) string {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return "0;0;0"
	}
	r := parseHex2(hex[0:2])
	g := parseHex2(hex[2:4])
	b := parseHex2(hex[4:6])
	return strconv.Itoa(r) + ";" + strconv.Itoa(g) + ";" + strconv.Itoa(b)
}

func parseHex2(s string) int {
	v := 0
	for i := 0; i < 2; i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			v = v*16 + int(c-'0')
		case c >= 'a' && c <= 'f':
			v = v*16 + int(c-'a') + 10
		case c >= 'A' && c <= 'F':
			v = v*16 + int(c-'A') + 10
		default:
			return 0
		}
	}
	return v
}

// Screen renders a session's frames onto a real terminal. It keeps the model
// of the session screen the frames describe — patches are applied into the
// model, and the model is what gets painted — so a transcript replacement or
// a geometry change can always fall back to "repaint everything I hold"
// instead of trusting a delta that no longer has a base.
//
// All methods must be called from one goroutine (the connection's read loop):
// the terminal byte stream is sequential by nature.
type Screen struct {
	out        writer
	cols, rows int

	// model is the screen the frames describe, as row runs.
	model [][]webui.Run
	// cursor is the caret state last drawn.
	cursor    webui.Cursor
	cursorSet bool
	// title is the window title last sent.
	title string
	// lastPatches are the row patches of the most recent frame: a late
	// transcript batch (the wire ships it as the NEXT message) scrolls the
	// physical screen and these rows are exactly what must be repainted.
	lastPatches []webui.RowPatch
}

// writer is the byte sink (usually the TTY). An interface rather than
// io.Writer because the renderer writes strings.
type writer interface {
	WriteString(string)
}

// NewScreen creates a screen for a cols×rows terminal.
func NewScreen(out writer, cols, rows int) *Screen {
	return &Screen{out: out, cols: cols, rows: rows}
}

// Size reports the terminal geometry the screen is painting for.
func (s *Screen) Size() (int, int) { return s.cols, s.rows }

// Start puts the terminal into the state the renderer assumes: no autowrap
// (the compositor positions every row absolutely, and a filled last column
// must not wrap), cleared, caret hidden until the first frame says where it
// is.
func (s *Screen) Start() {
	s.out.WriteString(autoWrapOff)
	s.out.WriteString(clearScreen)
	s.out.WriteString(hideCursor)
	s.cursor = webui.Cursor{}
	s.cursorSet = false
}

// Stop restores the terminal for the shell: attributes off, caret on,
// autowrap back on, scroll region reset. Bracketed paste / keyboard-protocol
// teardown is the local terminal adapter's job (it owns those modes).
func (s *Screen) Stop() {
	s.out.WriteString(regionReset)
	s.out.WriteString(sgrReset)
	s.out.WriteString(showCursor)
	s.out.WriteString(autoWrapOn)
}

// Attach renders the authoritative snapshot a new connection receives: the
// transcript the server kept for us, then the live screen.
func (s *Screen) Attach(f *webui.Frame, batch *webui.TranscriptBatch) {
	s.Start()
	if batch != nil {
		s.writeTranscript(*batch)
	}
	s.applyFrame(f, nil)
}

// Frame renders one delta (or full) frame. batch, when present, is the
// transcript batch that rode the same publish: rows that scrolled off the
// live screen and must be pushed into the terminal's native scrollback
// BEFORE the patches repaint what replaced them.
func (s *Screen) Frame(f *webui.Frame, batch *webui.TranscriptBatch) {
	s.applyFrame(f, batch)
}

// applyFrame updates the model from the frame and paints the result.
func (s *Screen) applyFrame(f *webui.Frame, batch *webui.TranscriptBatch) {
	geomChanged := f.Cols != s.cols || f.Rows != s.rows
	// Transcript first: its rows describe the screen BEFORE the patches
	// (they are what the patches replaced), and pushing them scrolls the
	// physical terminal so the patches land on the scrolled picture. A batch
	// that rebuilds the transcript stamps over the visible screen, so the
	// model is repainted in full afterwards.
	transcripted := s.applyBatch(batch)
	s.ensureModel(f.Cols, f.Rows)
	for _, p := range f.Patches {
		if p.Row < 0 || p.Row >= len(s.model) {
			continue
		}
		s.model[p.Row] = p.Runs
	}
	s.lastPatches = f.Patches
	switch {
	case geomChanged || transcripted || f.Full:
		s.repaintAll(f.Title, f.Cursor)
	default:
		for _, p := range f.Patches {
			s.paintRow(p.Row)
		}
		s.drawCursor(f.Cursor)
		s.setTitle(f.Title)
	}
}

// applyBatch handles the transcript batch that rode a frame: nothing, a
// physical scroll, or (when the batch REPLACES the transcript) a stamp-and-
// wipe whose caller must repaint the whole screen. It reports that repaint.
func (s *Screen) applyBatch(batch *webui.TranscriptBatch) bool {
	if batch == nil {
		return false
	}
	if batch.Replace || len(batch.Rows) > s.rows {
		s.writeTranscript(*batch)
		return true
	}
	if len(batch.Rows) > 0 {
		s.scrollUp(len(batch.Rows))
	}
	return false
}

// ensureModel resizes the model to the frame's geometry.
func (s *Screen) ensureModel(cols, rows int) {
	if cols <= 0 || rows <= 0 {
		return
	}
	s.cols, s.rows = cols, rows
	if len(s.model) == rows {
		return
	}
	m := make([][]webui.Run, rows)
	copy(m, s.model)
	s.model = m
}

// Scrollback applies a transcript batch that arrived as its own wire
// message, AFTER the frame it belongs to. The screen already painted that
// frame, so the rows cannot be "pushed before the patches" — instead the
// physical screen is scrolled here and the moved rows are repainted from
// the frame's patches, which the server ships complete for exactly this
// reason (every row a scroll moved is marked dirty and patched).
func (s *Screen) Scrollback(batch webui.TranscriptBatch) {
	if len(batch.Rows) == 0 && !batch.Replace {
		return
	}
	if batch.Replace || len(batch.Rows) > s.rows {
		// The transcript the terminal holds is void (wiped or rebuilt):
		// stamp the new rows in, then repaint the screen from the model.
		s.writeTranscript(batch)
		s.repaintAll(s.title, s.cursor)
		return
	}
	s.scrollUp(len(batch.Rows))
	for _, p := range s.lastPatches {
		if p.Row >= 0 && p.Row < s.rows {
			s.paintRow(p.Row)
		}
	}
	s.drawCursor(s.cursor)
}

// writeTranscript pushes batch rows into the terminal's native scrollback.
// Each row is stamped on the (disposable) top screen line and scrolled off
// with a line feed at the bottom of the screen — the one portable way to
// write a chosen line into scrollback. The visible screen is destroyed in
// the process; the caller must repaint afterwards.
func (s *Screen) writeTranscript(batch webui.TranscriptBatch) {
	if batch.Replace {
		s.out.WriteString(eraseScrol)
	}
	for _, rp := range batch.Rows {
		s.out.WriteString(cup(1, 1))
		s.writeRuns(rp.Runs, false)
		s.out.WriteString(cup(s.rows, 1))
		s.out.WriteString("\n")
	}
	s.out.WriteString(sgrReset)
}

// scrollUp pushes the top k physical lines into the terminal's native
// scrollback by line-feeding at the bottom of the screen. This is the live
// path: the k rows the server reports scrolled off ARE the top k physical
// lines, so scrolling alone reproduces the server's picture (the patches
// that ride the same frame repaint the rows that changed).
func (s *Screen) scrollUp(k int) {
	s.out.WriteString(regionReset)
	s.out.WriteString(cup(s.rows, 1))
	for i := 0; i < k; i++ {
		s.out.WriteString("\n")
	}
}

// repaintAll repaints every model row (geometry change, transcript
// replacement, full frame).
func (s *Screen) repaintAll(title string, cur webui.Cursor) {
	s.out.WriteString(clearScreen)
	for r := range s.model {
		s.paintRow(r)
	}
	s.drawCursor(cur)
	s.setTitle(title)
}

// paintRow writes one model row at its absolute position: runs with SGR
// diffing, then reset + erase-to-end-of-line so a row that underfills the
// width still blanks its tail and every row starts with a clean pen.
func (s *Screen) paintRow(row int) {
	s.out.WriteString(cup(row+1, 1))
	s.writeRuns(s.model[row], true)
}

// writeRuns emits runs through the pen-diffing SGR writer. tail clears the
// remainder of the row (live rows; transcript stamps run at the top line,
// where the following line feed scrolls the row away untouched).
func (s *Screen) writeRuns(runs []webui.Run, tail bool) {
	p := pen{}
	for _, r := range runs {
		next := pen{flags: r.Flags, fg: r.FG, bg: r.BG, link: r.Link, linkOpen: p.linkOpen}
		next = s.transitionPen(p, next)
		s.out.WriteString(r.Text)
		p = next
	}
	if p.linkOpen {
		s.out.WriteString(oscLinkClose())
	}
	if tail {
		s.out.WriteString(sgrReset)
		s.out.WriteString(eraseBelow)
	}
}

// transitionPen emits whatever separates the run the pen just wrote from the
// next one: an OSC-8 close/open when the hyperlink target changes, and the
// new pen's absolute SGR otherwise. It returns the settled pen (the link
// state is part of it).
func (s *Screen) transitionPen(p, next pen) pen {
	if next.link != p.link {
		if p.linkOpen {
			s.out.WriteString(oscLinkClose())
		}
		if next.link != "" {
			s.out.WriteString(oscLinkOpen(next.link))
			next.linkOpen = true
		} else {
			next.linkOpen = false
		}
	}
	if next != p {
		// A transition to the default pen must still emit a sequence: the
		// terminal keeps the previous run's attributes until told otherwise,
		// and sgr() renders no sequence for defaults.
		if seq := next.sgr(); seq != "" {
			s.out.WriteString(seq)
		} else {
			s.out.WriteString(sgrReset)
		}
	}
	return next
}

// drawCursor moves the caret to the frame's position and matches its
// visibility (DECTCEM). Both are written only on change: the terminal has no
// use for a caret refresh, and every stray byte is a chance to flicker.
func (s *Screen) drawCursor(cur webui.Cursor) {
	if !s.cursorSet || s.cursor.Row != cur.Row || s.cursor.Col != cur.Col {
		row, col := cur.Row, cur.Col
		if row >= s.rows {
			row = s.rows - 1
		}
		if row < 0 {
			row = 0
		}
		if col < 0 {
			col = 0
		}
		s.out.WriteString(cup(row+1, col+1))
		s.cursor.Row, s.cursor.Col = row, col
		s.cursorSet = true
	}
	if !s.cursorSet || s.cursor.Visible != cur.Visible {
		if cur.Visible {
			s.out.WriteString(showCursor)
		} else {
			s.out.WriteString(hideCursor)
		}
		s.cursor.Visible = cur.Visible
	}
}

// setTitle sends the window title when it changed.
func (s *Screen) setTitle(title string) {
	if title == "" || title == s.title {
		return
	}
	s.title = title
	s.out.WriteString(oscTitle(title))
}
