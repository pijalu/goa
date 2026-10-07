// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package attach

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/webui"
	"github.com/pijalu/goa/tui"
)

// The late-arriving transcript batch: a live append scrolls the physical
// screen and repaints the rows the last frame patched.
func TestScreenScrollbackLiveAppend(t *testing.T) {
	var w bufWriter
	s := NewScreen(&w, 20, 4)
	s.Start()
	s.Frame(&webui.Frame{
		Seq: 1, Cols: 20, Rows: 4, Full: true,
		Cursor: webui.Cursor{Row: 3, Col: 4, Visible: true},
		Patches: []webui.RowPatch{
			{Row: 3, Runs: []webui.Run{{Text: "last"}}},
		},
	}, nil)
	w.b.Reset()

	// One row scrolled off: line feeds push it into native scrollback, then
	// the moved rows are repainted from the frame's patches.
	s.Scrollback(webui.TranscriptBatch{Rows: []webui.RowPatch{
		{Row: 0, Runs: []webui.Run{{Text: "scrolled"}}},
	}})
	out := w.b.String()
	if !strings.Contains(out, "\x1b[r\x1b[4;1H\n") {
		t.Errorf("live append did not scroll from the bottom row: %q", out)
	}
	if !strings.Contains(out, "\x1b[4;1Hlast") {
		t.Errorf("live append did not repaint the last frame's patches: %q", out)
	}
}

// A transcript REPLACEMENT wipes the native scrollback, stamps the new rows
// in, and repaints the whole screen from the model.
func TestScreenScrollbackReplace(t *testing.T) {
	var w bufWriter
	s := NewScreen(&w, 10, 4)
	s.Start()
	s.Frame(&webui.Frame{
		Seq: 1, Cols: 10, Rows: 4, Full: true,
		Cursor: webui.Cursor{Row: 0, Col: 0, Visible: true},
		Patches: []webui.RowPatch{
			{Row: 0, Runs: []webui.Run{{Text: "screen"}}},
		},
	}, nil)
	w.b.Reset()

	s.Scrollback(webui.TranscriptBatch{
		Replace: true,
		Rows: []webui.RowPatch{
			{Row: 0, Runs: []webui.Run{{Text: "new history"}}},
		},
	})
	out := w.b.String()
	if !strings.Contains(out, eraseScrol) {
		t.Error("replacement did not wipe native scrollback")
	}
	if !strings.Contains(out, "new history") {
		t.Error("replacement rows were not stamped")
	}
	if !strings.Contains(out, "screen") {
		t.Error("replacement did not repaint the screen from the model")
	}
}

// An empty batch is a no-op; an oversized one takes the stamp path.
func TestScreenScrollbackEdges(t *testing.T) {
	var w bufWriter
	s := NewScreen(&w, 10, 2)
	s.Start()
	w.b.Reset()

	s.Scrollback(webui.TranscriptBatch{})
	if w.b.String() != "" {
		t.Errorf("empty batch produced output: %q", w.b.String())
	}

	w.b.Reset()
	s.Scrollback(webui.TranscriptBatch{Rows: []webui.RowPatch{
		{Row: 0, Runs: []webui.Run{{Text: "a"}}},
		{Row: 1, Runs: []webui.Run{{Text: "b"}}},
		{Row: 2, Runs: []webui.Run{{Text: "c"}}},
	}})
	out := w.b.String()
	// An oversized batch without Replace stamps the rows in without wiping:
	// nothing says the transcript the terminal holds is void.
	if strings.Contains(out, eraseScrol) {
		t.Error("oversized append must not wipe native scrollback")
	}
	for _, want := range []string{"a", "b", "c"} {
		if !strings.Contains(out, want) {
			t.Errorf("oversized batch lost row %q", want)
		}
	}
}

// Hyperlink runs are bracketed by OSC 8 open/close pairs.
func TestScreenRendersLinks(t *testing.T) {
	var w bufWriter
	s := NewScreen(&w, 20, 2)
	s.Start()
	s.Frame(&webui.Frame{
		Seq: 1, Cols: 20, Rows: 2, Full: true,
		Cursor: webui.Cursor{Row: 0, Col: 0, Visible: true},
		Patches: []webui.RowPatch{
			{Row: 0, Runs: []webui.Run{
				{Text: "see ", Flags: tui.AttrLink, Link: "https://goa.dev"},
				{Text: "docs"},
			}},
		},
	}, nil)
	out := w.b.String()
	// The OSC open precedes the run's SGR (the pen emits its colours after
	// the hyperlink envelope); both must be present, in that order.
	open := strings.Index(out, "\x1b]8;;https://goa.dev\x1b\\")
	if open < 0 {
		t.Fatalf("link open missing: %q", out)
	}
	if !strings.Contains(out[open:], "see ") {
		t.Errorf("link text missing after the open: %q", out)
	}
	if !strings.Contains(out, "\x1b]8;;\x1b\\") {
		t.Errorf("link close missing: %q", out)
	}
}

// Start/Stop put the terminal into and out of the renderer's assumed state.
func TestScreenStartStop(t *testing.T) {
	var w bufWriter
	s := NewScreen(&w, 20, 4)
	cols, rows := s.Size()
	if cols != 20 || rows != 4 {
		t.Fatalf("Size = %dx%d", cols, rows)
	}
	s.Start()
	if !strings.HasPrefix(w.b.String(), autoWrapOff+clearScreen+hideCursor) {
		t.Errorf("Start output = %q", w.b.String())
	}
	w.b.Reset()
	s.Stop()
	out := w.b.String()
	for _, want := range []string{regionReset, sgrReset, showCursor, autoWrapOn} {
		if !strings.Contains(out, want) {
			t.Errorf("Stop output missing %q: %q", want, out)
		}
	}
}

// A cursor outside the screen is clamped, not lost.
func TestScreenCursorClamped(t *testing.T) {
	var w bufWriter
	s := NewScreen(&w, 20, 4)
	s.Start()
	s.Frame(&webui.Frame{
		Seq: 1, Cols: 20, Rows: 4,
		Cursor: webui.Cursor{Row: 99, Col: -3, Visible: true},
	}, nil)
	if !strings.Contains(w.b.String(), "\x1b[4;1H") {
		t.Errorf("cursor not clamped to the bottom row: %q", w.b.String())
	}
}

// Malformed colours degrade to black instead of injecting junk into the
// stream.
func TestParseHex2Malformed(t *testing.T) {
	if got := parseHex2("#gggggg"); got != 0 {
		t.Errorf("parseHex2(garbage) = %d, want 0", got)
	}
	if got := rgb("#12"); got != "0;0;0" {
		t.Errorf("rgb(short) = %q", got)
	}
}

// The geometry-change path repaints every model row, including rows the new
// frame did not patch (the model carries them from earlier frames).
func TestScreenGeometryKeepsModel(t *testing.T) {
	var w bufWriter
	s := NewScreen(&w, 20, 4)
	s.Start()
	s.Frame(&webui.Frame{
		Seq: 1, Cols: 20, Rows: 4, Full: true,
		Cursor: webui.Cursor{Row: 0, Col: 0, Visible: true},
		Patches: []webui.RowPatch{
			{Row: 2, Runs: []webui.Run{{Text: "carried"}}},
		},
	}, nil)

	w.b.Reset()
	s.Frame(&webui.Frame{
		Seq: 2, Cols: 20, Rows: 4, Full: true,
		Cursor: webui.Cursor{Row: 0, Col: 0, Visible: true},
		Patches: []webui.RowPatch{
			{Row: 0, Runs: []webui.Run{{Text: "fresh"}}},
		},
	}, nil)
	// The full repaint walks the model: "carried" (row 2) must be painted
	// again even though the new frame did not mention it.
	if strings.Count(w.b.String(), "carried") < 1 {
		t.Errorf("full repaint dropped model rows: %q", w.b.String())
	}
}

// A transcript batch that rode the ATTACH frame is stamped before the live
// screen is painted (the Screen.Attach path).
func TestScreenAttachWithTranscript(t *testing.T) {
	var w bufWriter
	s := NewScreen(&w, 20, 4)
	batch := webui.TranscriptBatch{Rows: []webui.RowPatch{
		{Row: 0, Runs: []webui.Run{{Text: "old line"}}},
	}}
	s.Attach(&webui.Frame{
		Seq: 1, Cols: 20, Rows: 4, Full: true,
		Cursor: webui.Cursor{Row: 0, Col: 0, Visible: true},
		Patches: []webui.RowPatch{
			{Row: 0, Runs: []webui.Run{{Text: "live"}}},
		},
	}, &batch)
	out := w.b.String()
	if !strings.Contains(out, "old line") || !strings.Contains(out, "live") {
		t.Errorf("attach output = %q", out)
	}
}
