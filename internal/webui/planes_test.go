// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import (
	"testing"

	"github.com/pijalu/goa/tui"
)

// Per-client planes (specs/webui.md §22 extension): one session serves a
// cells client (native terminal) and a blocks client (HTML page) at once,
// each receiving only its own plane's document for the same screen.

// A hub fan-out is per plane: a cells frame never reaches a blocks client and
// vice versa, while a control broadcast reaches both.
func TestHub_PerPlaneFanOut(t *testing.T) {
	hub := NewHub(0)
	cells := &fakeClient{}
	blocks := &fakeClient{}
	detachC, _ := hub.Attach(cells, PlaneCells)
	defer detachC()
	detachB, _ := hub.Attach(blocks, PlaneBlocks)
	defer detachB()

	hub.Publish(PlaneCells, &Frame{Seq: 1})
	if got := len(blocks.frames); got != 0 {
		t.Errorf("blocks client received %d payloads for a cells frame, want 0", got)
	}
	if got := len(cells.frames); got != 1 {
		t.Fatalf("cells client received %d payloads for a cells frame, want 1", got)
	}
	hub.Publish(PlaneBlocks, &Frame{Seq: 2})
	if got := len(cells.frames); got != 1 {
		t.Errorf("cells client received a blocks frame, want none")
	}
	if got := len(blocks.frames); got != 1 {
		t.Fatalf("blocks client received %d payloads for a blocks frame, want 1", got)
	}

	hub.Broadcast(Control{Kind: CtrlReadOnly, Text: "viewer"})
	if len(cells.controls) != 1 || len(blocks.controls) != 1 {
		t.Errorf("controls are plane-agnostic: cells=%d blocks=%d, want 1/1",
			len(cells.controls), len(blocks.controls))
	}
}

// The driver cap spans planes: a driver is a driver whether it watches cells
// or blocks, so the cap cannot be doubled by mixing client kinds.
func TestHub_DriverCapSpansPlanes(t *testing.T) {
	hub := NewHub(1)
	first := &fakeClient{}
	if _, mode := hub.Attach(first, PlaneCells); mode != AttachDriver {
		t.Fatalf("first client = %s, want driver", mode)
	}
	second := &fakeClient{}
	if _, mode := hub.Attach(second, PlaneBlocks); mode != AttachViewer {
		t.Errorf("second client on another plane = %s, want viewer", mode)
	}
}

// ParsePlane resolves explicit names and defers everything else to the
// server's default.
func TestParsePlane(t *testing.T) {
	cases := []struct {
		name string
		def  Plane
		want Plane
	}{
		{"cells", PlaneBlocks, PlaneCells},
		{"blocks", PlaneCells, PlaneBlocks},
		{"", PlaneBlocks, PlaneBlocks},
		{"bogus", PlaneCells, PlaneCells},
	}
	for _, c := range cases {
		if got := ParsePlane(c.name, c.def); got != c.want {
			t.Errorf("ParsePlane(%q, %v) = %v, want %v", c.name, c.def, got, c.want)
		}
	}
}

// One publish serves both planes: each attached client receives its own
// document for the same screen, and both documents carry the same seq so a
// hello on either plane is answerable.
func TestVirtualTerminal_DualPlanePublish(t *testing.T) {
	cols, rows := 20, 4
	vt := NewVirtualTerminal(cols, rows)
	hub := NewHub(0)
	vt.SetSink(hub)
	cells, blocks := &fakeClient{}, &fakeClient{}
	hub.Attach(cells, PlaneCells)
	hub.Attach(blocks, PlaneBlocks)

	vt.ObserveScene(sceneOf(cols, rows, 2))
	// Fill every row with DISTINCT content so a write dirties the band the
	// blocks plane ships and the cells diff has rows to ship (early frames
	// legitimately carry no patches when nothing visibly moved).
	for i := 0; i < rows; i++ {
		vt.WriteString("hello " + string(rune('a'+i)) + "\r\n")
	}

	cf, bf := cells.lastFrame(), blocks.lastFrame()
	if cf == nil || bf == nil {
		t.Fatalf("publish reached one plane only: cells=%v blocks=%v", cf != nil, bf != nil)
	}
	if cf.Seq != bf.Seq {
		t.Errorf("plane seqs diverge: cells=%d blocks=%d, want one revision per publish", cf.Seq, bf.Seq)
	}
	if cf.Chrome != 0 || bf.Chrome == 0 {
		t.Errorf("planes shipped each other's document: cells.Chrome=%d blocks.Chrome=%d", cf.Chrome, bf.Chrome)
	}
	if len(cf.Patches) == 0 || len(cf.Patches) > rows {
		t.Errorf("cells frame patches = %d, want the written rows only", len(cf.Patches))
	}
	band := firstBlocksFrameWithPatches(blocks)
	if band == nil {
		t.Fatal("no blocks frame ever shipped a band patch")
	}
	if got := RunsText(band.Patches[0].Runs); got[:5] != "hello" {
		t.Errorf("blocks band row = %q", got)
	}
}

// firstBlocksFrameWithPatches returns the first payload whose frame carries
// row patches (every payload a cells+blocks hub hands this client is a
// blocks-plane document).
func firstBlocksFrameWithPatches(c *fakeClient) *Frame {
	for _, p := range c.frames {
		if len(p.Frame.Patches) > 0 {
			return p.Frame
		}
	}
	return nil
}

// sceneOf builds a minimal scene: chrome rows, no overlays, one block.
func sceneOf(width, height, chrome int) *tui.Scene {
	return &tui.Scene{
		TerminalW:    width,
		TerminalH:    height,
		ChromeHeight: chrome,
		Blocks:       []tui.SceneBlock{sceneBlock(1, tui.BlockUser, "hi")},
	}
}
