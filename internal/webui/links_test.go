// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/pijalu/goa/tui"
)

func TestRowRuns_SplitsOnLinkChange(t *testing.T) {
	cells := []tui.CellAttrs{
		{Text: "a", Flags: tui.AttrUnderline | tui.AttrLink, Link: "https://goa.dev"},
		{Text: "b", Flags: tui.AttrUnderline | tui.AttrLink, Link: "https://goa.dev"},
		{Text: "c", Flags: tui.AttrUnderline | tui.AttrLink, Link: "https://goa.dev/x"},
		{Text: "d"},
	}
	runs := RowRuns(cells)
	if len(runs) != 3 {
		t.Fatalf("runs = %+v, want one per link change", runs)
	}
	if runs[0].Text != "ab" || runs[0].Link != "https://goa.dev" {
		t.Errorf("run 0 = %+v", runs[0])
	}
	if !runs[0].IsLink() || !runs[1].IsLink() {
		t.Errorf("linked runs must report IsLink: %+v %+v", runs[0], runs[1])
	}
	if runs[2].IsLink() || runs[2].Link != "" {
		t.Errorf("plain run = %+v, want no link", runs[2])
	}
}

// The flag bit alone must never produce a clickable run: a client rendering a
// run with no URI would either link to nothing or to "undefined".
func TestRun_IsLinkRequiresBothBitAndURI(t *testing.T) {
	if (Run{Flags: tui.AttrLink}).IsLink() {
		t.Error("link bit without a URI must not count as a link")
	}
	if (Run{Link: "https://goa.dev"}).IsLink() {
		t.Error("a URI without the link bit must not count as a link")
	}
	if !(Run{Flags: tui.AttrLink, Link: "https://goa.dev"}).IsLink() {
		t.Error("bit + URI must count as a link")
	}
}

func TestCellGrid_HyperlinkReachesRuns(t *testing.T) {
	g := NewCellGrid(12, 2)
	g.Process("\x1b]8;;https://goa.dev\x07goa\x1b]8;;\x07!")

	runs := RowRuns(g.Cells(0))
	if len(runs) != 2 {
		t.Fatalf("runs = %+v, want link + plain", runs)
	}
	if runs[0].Link != "https://goa.dev" || !runs[0].IsLink() {
		t.Errorf("link run = %+v", runs[0])
	}
	if runs[1].IsLink() {
		t.Errorf("plain run = %+v", runs[1])
	}
}

func TestFrameCodec_LinkRoundTrip(t *testing.T) {
	f := &Frame{Seq: 7, Cols: 4, Rows: 1, Patches: []RowPatch{{Row: 0, Runs: []Run{
		{Text: "goa", Flags: tui.AttrUnderline | tui.AttrLink, Link: "https://goa.dev"},
		{Text: "!", Flags: tui.AttrUnderline, Link: "https://goa.dev"},
		{Text: " "},
	}}}}
	data, err := NewFrameCodec().EncodeFrame(f)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	back, err := NewFrameCodec().DecodeFrame(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := back.Patches[0].Runs
	if got[0].Link != "https://goa.dev" {
		t.Errorf("linked run = %+v", got[0])
	}
	// A run that carries a URI but lost the link bit must not be published as
	// a link: the encoder drops the URI.
	if got[1].Link != "" {
		t.Errorf("unflagged run kept its URI: %+v", got[1])
	}
}

func TestFrameCodec_ScrollbackRoundTrip(t *testing.T) {
	rows := []RowPatch{{Row: 0, Runs: []Run{{Text: "old line"}}}}
	data, err := NewFrameCodec().EncodeScrollback(3, rows)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !strings.Contains(string(data), `"t":"scrollback"`) {
		t.Errorf("message type missing: %s", data)
	}
	seq, back, err := NewFrameCodec().DecodeScrollback(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if seq != 3 || len(back) != 1 || RunsText(back[0].Runs) != "old line" {
		t.Errorf("round trip = %d %+v", seq, back)
	}
	if _, err := NewFrameCodec().EncodeScrollback(1, nil); err == nil {
		t.Error("empty scrollback batch must be rejected")
	}
	if _, _, err := NewFrameCodec().DecodeScrollback([]byte(`{"t":"frame"}`)); err == nil {
		t.Error("decoding a frame as scrollback must fail")
	}
}

// Scrolled-off rows are shipped exactly once, in order — the client appends
// them to a transcript list.
func TestCellGrid_TakeScrollbackShipsEachRowOnce(t *testing.T) {
	g := NewCellGrid(6, 2)
	g.Process("\x1b[1;1Hone")
	g.Process("\x1b[2;1Htwo")
	g.Process("\x1b[3;1Hthree\n") // scrolls "one" into the scrollback

	first := g.TakeScrollback()
	if len(first) != 1 || first[0].Row != 0 {
		t.Fatalf("first take = %+v, want row 0", first)
	}
	if got := RunsText(first[0].Runs); got[:3] != "one" {
		t.Errorf("scrollback row = %q", got)
	}
	if again := g.TakeScrollback(); len(again) != 0 {
		t.Errorf("second take = %+v, want nothing (already shipped)", again)
	}

	g.Process("\x1b[3;1Hfour\n") // scrolls "three"
	second := g.TakeScrollback()
	if len(second) != 1 || second[0].Row != 1 {
		t.Fatalf("second take = %+v, want row 1", second)
	}
}

func TestCellGrid_TakeScrollbackResetsOnClear(t *testing.T) {
	g := NewCellGrid(6, 2)
	g.Process("\x1b[1;1Hone\x1b[2;1Htwo\x1b[3;1Hthree\n")
	if got := g.TakeScrollback(); len(got) != 1 {
		t.Fatalf("take = %+v, want 1 row", got)
	}
	g.Clear()
	if got := g.TakeScrollback(); len(got) != 0 {
		t.Errorf("take after clear = %+v, want nothing", got)
	}
}

type recordingSink struct{ frames []*Frame }

func (r *recordingSink) Publish(f *Frame) { r.frames = append(r.frames, f) }

// HasClients always reports true: a recorder is a test's stand-in for an
// attached browser, and a test that installs one wants every frame built.
func (r *recordingSink) HasClients() bool { return true }

// The virtual terminal must attach scrolled-off rows to the frame that caused
// the scroll, so the transport can emit them as their own message.
func TestVirtualTerminal_FrameCarriesScrollback(t *testing.T) {
	vt := NewVirtualTerminal(8, 2)
	sink := &recordingSink{}
	vt.SetSink(sink)

	vt.WriteString("\x1b[1;1Hone")
	vt.WriteString("\x1b[2;1Htwo")
	vt.WriteString("\x1b[3;1Hthree\n") // scroll

	last := sink.frames[len(sink.frames)-1]
	if len(last.Scrollback) != 1 {
		t.Fatalf("frame scrollback = %+v, want 1 row", last.Scrollback)
	}
	if got := RunsText(last.Scrollback[0].Runs); got[:3] != "one" {
		t.Errorf("scrollback row = %q", got)
	}
	// The next frame must not repeat it.
	vt.WriteString("x")
	if next := sink.frames[len(sink.frames)-1]; len(next.Scrollback) != 0 {
		t.Errorf("next frame repeated the scrollback: %+v", next.Scrollback)
	}
}

func TestVirtualTerminal_FullFrameCarriesScrollback(t *testing.T) {
	vt := NewVirtualTerminal(8, 2)
	vt.WriteString("\x1b[1;1Hone")
	vt.WriteString("\x1b[2;1Htwo")
	vt.WriteString("\x1b[3;1Hthree\n")

	f := vt.FullFrame()
	if len(f.Scrollback) != 1 || f.Scrollback[0].Row != 0 {
		t.Errorf("full frame scrollback = %+v", f.Scrollback)
	}
}

// The browser client is the contract: it reads msg.sb, so the wire key must
// stay "sb" until app.js changes.
func TestFrameCodec_ScrollbackWireKey(t *testing.T) {
	data, err := NewFrameCodec().EncodeScrollback(1, []RowPatch{{Row: 0, Runs: []Run{{Text: "x"}}}})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := raw["sb"]; !ok {
		t.Errorf("wire document has no \"sb\" key: %s", data)
	}
}
