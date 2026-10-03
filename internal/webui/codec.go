// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import (
	"encoding/json"
	"fmt"

	"github.com/pijalu/goa/tui"
)

// Wire message types (spec §20). The "t" discriminator is present on every
// message so a client can switch without heuristics.
const (
	MsgFrame      = "frame"
	MsgControl    = "control"
	MsgScrollback = "scrollback"
)

// wireFrame is the compact on-the-wire shape. Runs are positional tuples
// [text, flags, fg, bg] — a JSON object per run roughly doubled the payload.
// Only the fields a frame needs are serialised; control messages reuse the
// same envelope with Kind/Text/Session filled in.
type wireFrame struct {
	T       string    `json:"t"`
	Seq     uint64    `json:"seq,omitempty"`
	Cols    int       `json:"cols,omitempty"`
	Rows    int       `json:"rows,omitempty"`
	Cur     Cursor    `json:"cur,omitempty"`
	Patches []wireRow `json:"patches,omitempty"`
	Scroll  []wireRow `json:"sb,omitempty"`
	Title   string    `json:"title,omitempty"`
	Full    bool      `json:"full,omitempty"`

	Kind    string `json:"kind,omitempty"`
	Session string `json:"session,omitempty"`
	Text    string `json:"text,omitempty"`
}

type wireRow struct {
	Row  int       `json:"row"`
	Runs []wireRun `json:"runs"`
}

// wireRun is [text, flags, fg, bg, link]; flags, colours and the link are
// 0/"" when default. The link stays a fifth slot rather than a fifth key so a
// run costs one array element, not another JSON object.
type wireRun [5]any

// FrameCodec encodes and decodes the JSON wire format. The zero value is ready
// to use.
type FrameCodec struct{}

// NewFrameCodec returns a codec (kept so callers do not have to rely on the
// zero value being valid).
func NewFrameCodec() *FrameCodec { return &FrameCodec{} }

// EncodeFrame renders a frame as one JSON document.
func (c *FrameCodec) EncodeFrame(f *Frame) ([]byte, error) {
	if f == nil {
		return nil, fmt.Errorf("webui: nil frame")
	}
	w := wireFrame{
		T:     MsgFrame,
		Seq:   f.Seq,
		Cols:  f.Cols,
		Rows:  f.Rows,
		Cur:   f.Cursor,
		Title: f.Title,
		Full:  f.Full,
	}
	for _, p := range f.Patches {
		w.Patches = append(w.Patches, wireRow{Row: p.Row, Runs: encodeRuns(p.Runs)})
	}
	return json.Marshal(w)
}

// DecodeFrame parses a frame document produced by EncodeFrame. Used by tests
// and by the golden-frame tooling; the browser is the only real client.
func (c *FrameCodec) DecodeFrame(data []byte) (*Frame, error) {
	var w wireFrame
	if err := json.Unmarshal(data, &w); err != nil {
		return nil, fmt.Errorf("webui: decode frame: %w", err)
	}
	if w.T != MsgFrame {
		return nil, fmt.Errorf("webui: unexpected message type %q", w.T)
	}
	f := &Frame{
		Seq:    w.Seq,
		Cols:   w.Cols,
		Rows:   w.Rows,
		Cursor: w.Cur,
		Title:  w.Title,
		Full:   w.Full,
	}
	for _, r := range w.Patches {
		f.Patches = append(f.Patches, RowPatch{Row: r.Row, Runs: decodeRuns(r.Runs)})
	}
	return f, nil
}

// EncodeScrollback renders the rows a frame scrolled off as their own message.
// It is a separate message type (spec §11.1) so a client can append to its
// transcript list without touching the live grid.
func (c *FrameCodec) EncodeScrollback(seq uint64, rows []RowPatch) ([]byte, error) {
	if len(rows) == 0 {
		return nil, fmt.Errorf("webui: empty scrollback batch")
	}
	w := wireFrame{T: MsgScrollback, Seq: seq}
	for _, p := range rows {
		w.Scroll = append(w.Scroll, wireRow{Row: p.Row, Runs: encodeRuns(p.Runs)})
	}
	return json.Marshal(w)
}

// DecodeScrollback parses a scrollback document.
func (c *FrameCodec) DecodeScrollback(data []byte) (uint64, []RowPatch, error) {
	var w wireFrame
	if err := json.Unmarshal(data, &w); err != nil {
		return 0, nil, fmt.Errorf("webui: decode scrollback: %w", err)
	}
	if w.T != MsgScrollback {
		return 0, nil, fmt.Errorf("webui: unexpected message type %q", w.T)
	}
	out := make([]RowPatch, 0, len(w.Scroll))
	for _, r := range w.Scroll {
		out = append(out, RowPatch{Row: r.Row, Runs: decodeRuns(r.Runs)})
	}
	return w.Seq, out, nil
}

// EncodeControl renders an out-of-band control message (session rotation,
// read-only notice, shutdown).
func (c *FrameCodec) EncodeControl(ctrl Control) ([]byte, error) {
	t := ctrl.Kind
	if t == "" {
		t = MsgControl
	}
	return json.Marshal(wireFrame{
		T:       t,
		Kind:    ctrl.Kind,
		Session: ctrl.Session,
		Text:    ctrl.Text,
	})
}

// DecodeControl parses a control message.
func (c *FrameCodec) DecodeControl(data []byte) (Control, error) {
	var w wireFrame
	if err := json.Unmarshal(data, &w); err != nil {
		return Control{}, fmt.Errorf("webui: decode control: %w", err)
	}
	return Control{Kind: w.Kind, Session: w.Session, Text: w.Text}, nil
}

func encodeRuns(runs []Run) []wireRun {
	out := make([]wireRun, 0, len(runs))
	for _, r := range runs {
		link := ""
		if r.IsLink() {
			link = r.Link
		}
		out = append(out, wireRun{r.Text, uint16(r.Flags), r.FG, r.BG, link})
	}
	return out
}

func decodeRuns(in []wireRun) []Run {
	out := make([]Run, 0, len(in))
	for _, w := range in {
		var r Run
		if len(w) > 0 {
			r.Text, _ = w[0].(string)
		}
		if len(w) > 1 {
			r.Flags = tuiFlags(w[1])
		}
		if len(w) > 2 {
			r.FG, _ = w[2].(string)
		}
		if len(w) > 3 {
			r.BG, _ = w[3].(string)
		}
		if len(w) > 4 {
			r.Link, _ = w[4].(string)
		}
		out = append(out, r)
	}
	return out
}

// tuiFlags converts a decoded JSON number to the emulator's flag type.
func tuiFlags(v any) tui.AttrFlags {
	switch n := v.(type) {
	case float64:
		return tui.AttrFlags(uint16(n))
	case int:
		return tui.AttrFlags(uint16(n))
	case uint16:
		return tui.AttrFlags(n)
	}
	return 0
}
