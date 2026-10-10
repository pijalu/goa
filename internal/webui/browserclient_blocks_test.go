// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import (
	"strconv"
	"strings"
	"testing"

	"github.com/dop251/goja"
)

// The blocks plane is the page's conversation DOM: every block op becomes an
// element, and the collapsible ones (thinking, tool) carry the collapse state the
// TUI's own widget has. These tests run the REAL app.js in that plane.

// blockEl returns the block wrapper #blocks holds at index i.
func (h *clientHarness) blockEl(t *testing.T, i int) *goja.Object {
	t.Helper()
	kids := h.el(t, "blocks").Get("children").ToObject(h.vm)
	el := kids.Get(strconv.Itoa(i))
	if el == nil || goja.IsUndefined(el) {
		t.Fatalf("no block at index %d", i)
	}
	return el.ToObject(h.vm)
}

// detailsOf returns the <details> a collapsible block builds as its first child.
func (h *clientHarness) detailsOf(t *testing.T, i int) *goja.Object {
	t.Helper()
	kids := h.blockEl(t, i).Get("children").ToObject(h.vm)
	det := kids.Get("0")
	if det == nil || goja.IsUndefined(det) {
		t.Fatalf("block %d has no collapsible element", i)
	}
	return det.ToObject(h.vm)
}

// childAt returns a descendant by index.
func (h *clientHarness) childAt(t *testing.T, el *goja.Object, i int) *goja.Object {
	t.Helper()
	kids := el.Get("children").ToObject(h.vm)
	c := kids.Get(strconv.Itoa(i))
	if c == nil || goja.IsUndefined(c) {
		t.Fatalf("no child at index %d", i)
	}
	return c.ToObject(h.vm)
}

// toggle simulates the reader clicking a <details>: the browser flips `open` and
// then fires `toggle`, which is what the page listens to.
func (h *clientHarness) toggle(t *testing.T, det *goja.Object, open bool) {
	t.Helper()
	if err := det.Set("open", open); err != nil {
		t.Fatalf("set open: %v", err)
	}
	fire, ok := goja.AssertFunction(det.Get("fire"))
	if !ok {
		t.Fatal("stub element has no fire()")
	}
	if _, err := fire(det, h.vm.ToValue("toggle")); err != nil {
		t.Fatalf("fire toggle: %v", err)
	}
}

// TestClientJS_ThinkingBlockHonoursServerExpanded is B22's core: the server ships
// the TUI's collapse state (a thinking block starts expanded) and the page must
// render it. It used to create EVERY collapsible closed, because the previous
// open state of a brand-new block was `null` — which is not `undefined` — so the
// metadata was never consulted and each block had to be clicked open.
func TestClientJS_ThinkingBlockHonoursServerExpanded(t *testing.T) {
	h := newClientHarnessIn(t, "blocks")
	h.deliver(t, blocksFrameDoc(t,
		BlockOp{Op: "set", ID: 1, Kind: "thinking", Text: "why", Meta: map[string]string{"expanded": "1"}},
		BlockOp{Op: "set", ID: 2, Kind: "thinking", Text: "no", Meta: map[string]string{"expanded": "0"}},
	))

	if !h.detailsOf(t, 0).Get("open").ToBoolean() {
		t.Error("a thinking block the server ships expanded was rendered collapsed")
	}
	if h.detailsOf(t, 1).Get("open").ToBoolean() {
		t.Error("a thinking block the server ships collapsed was rendered expanded")
	}
}

// TestClientJS_CollapsePreferenceIsStickyPerKind pins the reported rule: "if a
// collapse is open (or closed) subsequent item should follow the same logic" —
// per kind, so a reader's thinking choice does not drag the tool cards with it.
func TestClientJS_CollapsePreferenceIsStickyPerKind(t *testing.T) {
	h := newClientHarnessIn(t, "blocks")
	h.deliver(t, blocksFrameDoc(t,
		BlockOp{Op: "set", ID: 1, Kind: "thinking", Text: "first", Meta: map[string]string{"expanded": "1"}},
	))
	first := h.detailsOf(t, 0)
	if !first.Get("open").ToBoolean() {
		t.Fatal("the first thinking block should start expanded")
	}

	// The reader closes it.
	h.toggle(t, first, false)

	// The next thinking block follows the reader, the next tool card does not.
	h.deliver(t, blocksFrameDoc(t,
		BlockOp{Op: "set", ID: 2, Kind: "thinking", Text: "second", Meta: map[string]string{"expanded": "1"}},
		BlockOp{Op: "set", ID: 3, Kind: "tool", Text: "out", Meta: map[string]string{"tool": "bash", "expanded": "1"}},
	))
	if h.detailsOf(t, 1).Get("open").ToBoolean() {
		t.Error("a thinking block ignored the reader's collapse choice for its kind")
	}
	if !h.detailsOf(t, 2).Get("open").ToBoolean() {
		t.Error("the reader's thinking choice leaked into the tool cards")
	}
}

// TestClientJS_ToolCardPreviewsTheTailWithThePolicyLineCount is B23: a collapsed
// card previews as many lines as the TUI's widget (the server ships the policy
// value in meta.preview) and shows the TAIL, like the terminal's collapsed body
// does — not the head, and not the whole output.
func TestClientJS_ToolCardPreviewsTheTailWithThePolicyLineCount(t *testing.T) {
	h := newClientHarnessIn(t, "blocks")
	text := "l1\nl2\nl3\nl4\nl5"
	h.deliver(t, blocksFrameDoc(t,
		BlockOp{Op: "set", ID: 1, Kind: "tool", Text: text, Meta: map[string]string{
			"tool": "bash", "args": "ls", "status": "success", "preview": "2", "expanded": "0",
		}},
	))

	det := h.detailsOf(t, 0)
	if det.Get("open").ToBoolean() {
		t.Error("a tool card the server ships collapsed was rendered expanded")
	}
	preview := h.childAt(t, det, 1).Get("textContent").String()
	if !strings.Contains(preview, "… 3 earlier lines") {
		t.Errorf("preview = %q, want it to report the 3 lines it left out", preview)
	}
	if !strings.Contains(preview, "l4\nl5") {
		t.Errorf("preview = %q, want the LAST 2 lines", preview)
	}
	if strings.Contains(preview, "l1") {
		t.Errorf("preview = %q, want the tail, not the head", preview)
	}
	// The full output still rides the open body.
	if out := h.childAt(t, det, 2).Get("textContent").String(); out != text {
		t.Errorf("open body = %q, want the whole output", out)
	}
}

// TestClientJS_ToolCardPreviewFallsBackToTheTUIDefault covers a frame without
// meta.preview (an older server, or a meta update that carried no preview key):
// the card still previews the TUI's default line count instead of the whole
// output.
func TestClientJS_ToolCardPreviewFallsBackToTheTUIDefault(t *testing.T) {
	h := newClientHarnessIn(t, "blocks")
	var lines []string
	for i := 1; i <= 12; i++ {
		lines = append(lines, "line"+strconv.Itoa(i))
	}
	h.deliver(t, blocksFrameDoc(t,
		BlockOp{Op: "set", ID: 1, Kind: "tool", Text: strings.Join(lines, "\n"), Meta: map[string]string{
			"tool": "bash", "status": "success", "expanded": "0",
		}},
	))

	preview := h.childAt(t, h.detailsOf(t, 0), 1).Get("textContent").String()
	if !strings.Contains(preview, "… 2 earlier lines") {
		t.Errorf("preview = %q, want the TUI default of 10 lines shown (2 left out)", preview)
	}
}
