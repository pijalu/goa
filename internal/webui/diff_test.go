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

func TestFrameCodec_RoundTrip(t *testing.T) {
	in := &Frame{
		Seq:    128,
		Cols:   120,
		Rows:   40,
		Cursor: Cursor{Row: 31, Col: 12, Visible: true},
		Title:  "goa",
		Full:   false,
		Patches: []RowPatch{
			{Row: 29, Runs: []Run{
				{Text: "  goa "},
				{Text: "v1.0.0", Flags: tui.AttrBold, FG: "#646c84"},
			}},
			{Row: 31, Runs: []Run{{Text: "> ", FG: "#7c5cfc"}, {Text: "go"}}},
		},
	}
	codec := NewFrameCodec()
	data, err := codec.EncodeFrame(in)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !strings.Contains(string(data), `"t":"frame"`) {
		t.Errorf("wire type missing: %s", data)
	}
	out, err := codec.DecodeFrame(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Seq != in.Seq || out.Cols != in.Cols || out.Rows != in.Rows ||
		out.Cursor != in.Cursor || out.Title != in.Title || out.Full != in.Full {
		t.Errorf("header round-trip mismatch: %+v vs %+v", out, in)
	}
	if len(out.Patches) != len(in.Patches) {
		t.Fatalf("patches = %d, want %d", len(out.Patches), len(in.Patches))
	}
	assertPatchesEqual(t, out.Patches, in.Patches)
}

// assertPatchesEqual compares decoded patches against the originals, row by
// row and run by run.
func assertPatchesEqual(t *testing.T, got, want []RowPatch) {
	t.Helper()
	for i, p := range got {
		if p.Row != want[i].Row {
			t.Errorf("patch %d row = %d, want %d", i, p.Row, want[i].Row)
		}
		if len(p.Runs) != len(want[i].Runs) {
			t.Errorf("patch %d runs = %d, want %d", i, len(p.Runs), len(want[i].Runs))
			continue
		}
		for j, r := range p.Runs {
			if r != want[i].Runs[j] {
				t.Errorf("run %d/%d = %+v, want %+v", i, j, r, want[i].Runs[j])
			}
		}
	}
}

func TestFrameCodec_RejectsGarbage(t *testing.T) {
	codec := NewFrameCodec()
	if _, err := codec.DecodeFrame([]byte("{")); err == nil {
		t.Error("expected an error for truncated JSON")
	}
	if _, err := codec.DecodeFrame([]byte(`{"t":"control"}`)); err == nil {
		t.Error("expected an error for the wrong message type")
	}
	if _, err := codec.EncodeFrame(nil); err == nil {
		t.Error("expected an error for a nil frame")
	}
}

func TestFrameCodec_ControlRoundTrip(t *testing.T) {
	codec := NewFrameCodec()
	data, err := codec.EncodeControl(Control{Kind: CtrlSessionRotated, Session: "b71e0c94"})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if wire["t"] != CtrlSessionRotated || wire["session"] != "b71e0c94" {
		t.Errorf("wire = %v", wire)
	}
	ctrl, err := codec.DecodeControl(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ctrl.Kind != CtrlSessionRotated || ctrl.Session != "b71e0c94" {
		t.Errorf("control = %+v", ctrl)
	}
}

// A row diff must ship only rows that moved, and must collapse runs.
func TestCellDiff_OnlyChangedRows(t *testing.T) {
	prev := [][]tui.CellAttrs{
		cellsOf("aaa", tui.AttrBold, ""),
		cellsOf("bbb", 0, ""),
		cellsOf("ccc", 0, ""),
	}
	cur := [][]tui.CellAttrs{
		cellsOf("aaa", tui.AttrBold, ""),
		cellsOf("BBB", 0, ""),
		cellsOf("ccc", 0, ""),
	}
	d := NewCellDiff(prev, cur)

	if d.RowChanged(0) || d.RowChanged(2) {
		t.Error("unchanged rows reported as changed")
	}
	if !d.RowChanged(1) {
		t.Error("changed row not detected")
	}
	patches := d.Patches([]int{0, 1, 2})
	if len(patches) != 1 || patches[0].Row != 1 {
		t.Fatalf("patches = %+v, want only row 1", patches)
	}
	if got := RunsText(patches[0].Runs); got != "BBB" {
		t.Errorf("patch text = %q", got)
	}
	// Out-of-range rows are not patches.
	if d.RowChanged(9) || d.RowChanged(-1) {
		t.Error("out-of-range row reported as changed")
	}
}

func TestCellDiff_StyleChangeCountsAsChange(t *testing.T) {
	prev := [][]tui.CellAttrs{cellsOf("a", 0, "")}
	cur := [][]tui.CellAttrs{cellsOf("a", tui.AttrBold, "")}
	if !NewCellDiff(prev, cur).RowChanged(0) {
		t.Error("a style-only change must count as a change")
	}
	// Different length also counts.
	short := [][]tui.CellAttrs{{}}
	if !NewCellDiff(prev, short).RowChanged(0) {
		t.Error("a length change must count as a change")
	}
	// A row that did not exist before is a change.
	if !NewCellDiff(nil, cur).RowChanged(0) {
		t.Error("a new row must count as a change")
	}
}

func TestRowRuns_CollapsesMaximalSpans(t *testing.T) {
	row := []tui.CellAttrs{
		{Text: "a", Flags: tui.AttrBold},
		{Text: "b", Flags: tui.AttrBold},
		{Text: "c"},
		{Text: "d", Flags: tui.AttrBold},
	}
	runs := RowRuns(row)
	if len(runs) != 3 {
		t.Fatalf("runs = %+v, want 3", runs)
	}
	if runs[0].Text != "ab" {
		t.Errorf("run 0 = %q, want \"ab\"", runs[0].Text)
	}
	if got := RunsText(runs); got != "abcd" {
		t.Errorf("round-tripped text = %q", got)
	}
	// An untouched cell renders as one space so columns stay aligned.
	blank := RowRuns([]tui.CellAttrs{{}})
	if len(blank) != 1 || blank[0].Text != " " {
		t.Errorf("blank cell = %+v", blank)
	}
	if RowRuns(nil) != nil {
		t.Error("empty row should produce no runs")
	}
}

func TestFrame_PatchedRow(t *testing.T) {
	f := &Frame{Patches: []RowPatch{{Row: 4}}}
	if _, ok := f.PatchedRow(4); !ok {
		t.Error("row 4 should be present")
	}
	if _, ok := f.PatchedRow(5); ok {
		t.Error("row 5 should be absent")
	}
}

func cellsOf(text string, flags tui.AttrFlags, fg string) []tui.CellAttrs {
	out := make([]tui.CellAttrs, len(text))
	for i, r := range text {
		out[i] = tui.CellAttrs{Text: string(r), Flags: flags, FG: fg}
	}
	return out
}
