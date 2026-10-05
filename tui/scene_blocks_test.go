// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"
)

// newBlockTestTUI builds a minimal engine (header + chat viewport + editor +
// footer) whose conversation holds entries the block exporter describes. It
// is the fixture every export test drives; Start wires the render loop the
// same way production does.
func newBlockTestTUI(t *testing.T, w, h int) (*TUI, *ChatViewport) {
	t.Helper()
	term := &fakeTerminal{w: w, h: h}
	engine := NewTUI(term)
	cv := NewChatViewport()
	cv.SetAllocatedHeight(h - 6)
	engine.AddChild(NewHeader("goa", "test"))
	engine.AddChild(cv)
	ed := NewEditor()
	ed.SetTUI(engine)
	ed.SetFocused(true)
	engine.AddChild(ed)
	engine.AddChild(NewFooter())
	engine.SetFocus(ed)
	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(engine.Stop)
	return engine, cv
}

// TestExportBlocks_KindsAndOrder walks one entry of every kind through the
// exporter and asserts kind, order, and the width-independent text.
func TestExportBlocks_KindsAndOrder(t *testing.T) {
	_, cv := newBlockTestTUI(t, 80, 24)
	cv.AddUserMessage("hello")
	cv.AddAssistantMessage("**markdown** source")
	cv.AddThinkingBlock("reasoning", true)
	cv.AddSystemMessage("notice")
	cv.AddInfoMessage("fyi")
	cv.AddAgentMessage("sub", "agent text")

	scene := cv.ExportBlocks()
	if len(scene) != 6 {
		t.Fatalf("ExportBlocks = %d blocks, want 6: %+v", len(scene), scene)
	}
	want := []struct {
		kind SceneBlockKind
		text string
	}{
		{BlockUser, "hello"},
		{BlockAssistant, "**markdown** source"},
		{BlockThinking, "reasoning"},
		{BlockSystem, "notice"},
		{BlockInfo, "fyi"},
		{BlockAgent, "agent text"},
	}
	for i, w := range want {
		if scene[i].Kind != w.kind {
			t.Errorf("block %d kind = %q, want %q", i, scene[i].Kind, w.kind)
		}
		if scene[i].Text != w.text {
			t.Errorf("block %d text = %q, want %q", i, scene[i].Text, w.text)
		}
		if scene[i].ID == HeaderBlockID {
			t.Errorf("block %d carries the reserved header id %d", i, HeaderBlockID)
		}
	}
	if scene[2].Meta == nil || scene[2].Meta["expanded"] != "1" {
		t.Errorf("thinking block meta = %+v, want expanded=1", scene[2].Meta)
	}
}

// TestExportBlocks_ToolMeta drives a tool widget through updates and asserts
// the view-derived metadata (name, status, duration) follows the widget, not
// just the model data.
func TestExportBlocks_ToolMeta(t *testing.T) {
	_, cv := newBlockTestTUI(t, 80, 24)
	tc := cv.AddToolExecution("bash", `{"command":"ls"}`)
	blocks := cv.ExportBlocks()
	if len(blocks) != 1 {
		t.Fatalf("blocks = %d, want 1", len(blocks))
	}
	meta := blocks[0].Meta
	if meta["tool"] != "bash" {
		t.Errorf("meta.tool = %q, want bash", meta["tool"])
	}
	if meta["status"] != "pending" && meta["status"] != "running" {
		t.Errorf("meta.status = %q, want pending|running", meta["status"])
	}
	tc.SetStatus(ToolSuccess)
	tc.SetDuration("1.2s")
	tc.SetOutput("file1\nfile2")
	blocks = cv.ExportBlocks()
	meta = blocks[0].Meta
	if meta["status"] != "success" {
		t.Errorf("meta.status = %q, want success", meta["status"])
	}
	if meta["duration"] != "1.2s" {
		t.Errorf("meta.duration = %q, want 1.2s", meta["duration"])
	}
	if blocks[0].Text != "file1\nfile2" {
		t.Errorf("tool text = %q, want the output", blocks[0].Text)
	}
}

// TestScene_CarriesBlocksAndHeader asserts buildScene attaches the header art
// block (id 0) ahead of the conversation blocks.
func TestScene_CarriesBlocksAndHeader(t *testing.T) {
	engine, cv := newBlockTestTUI(t, 80, 24)
	cv.AddAssistantMessage("answer")
	scene := engine.buildScene(80, 24)
	if len(scene.Blocks) < 2 {
		t.Fatalf("scene blocks = %d, want >= 2 (header + message)", len(scene.Blocks))
	}
	if scene.Blocks[0].ID != HeaderBlockID || scene.Blocks[0].Kind != BlockHeader {
		t.Fatalf("first block = %+v, want header id %d", scene.Blocks[0], HeaderBlockID)
	}
	if len(scene.Blocks[0].Lines) == 0 {
		t.Error("header block carries no art lines")
	}
	found := false
	for _, b := range scene.Blocks[1:] {
		if b.Kind == BlockAssistant && strings.Contains(b.Text, "answer") {
			found = true
		}
	}
	if !found {
		t.Error("assistant block missing from scene")
	}
	if scene.BlockWidth != 80 {
		t.Errorf("BlockWidth = %d, want 80", scene.BlockWidth)
	}
}

// TestScene_OverlayCapturesInputFlags asserts the overlay layers carry the
// per-layer capture flag and Scene.OverlayCapturesInput reflects any
// capturing overlay.
func TestScene_OverlayCapturesInputFlags(t *testing.T) {
	engine, _ := newBlockTestTUI(t, 80, 24)
	engine.ShowOverlay(newOverlayTestComponent(3), OverlayOptions{
		CaptureInput: true,
	})
	scene := engine.buildScene(80, 24)
	if !scene.OverlayCapturesInput {
		t.Error("OverlayCapturesInput = false, want true for a capturing overlay")
	}
	captured := false
	for _, l := range scene.Layers {
		if l.Kind == LayerOverlay && l.CapturesInput {
			captured = true
		}
	}
	if !captured {
		t.Error("no overlay layer flagged CapturesInput")
	}
}

// overlayTestComponent is a tiny fixed-height overlay body.
type overlayTestComponent struct{ lines int }

func newOverlayTestComponent(lines int) *overlayTestComponent { return &overlayTestComponent{lines: lines} }

func (o *overlayTestComponent) Render(width int) []string {
	out := make([]string, o.lines)
	for i := range out {
		out[i] = "overlay"
	}
	return out
}

func (o *overlayTestComponent) HandleInput(string) {}
func (o *overlayTestComponent) Invalidate()        {}
