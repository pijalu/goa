// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import (
	"strings"
	"testing"

	"github.com/dop251/goja"
)

// clientHarness boots the real embedded app.js against the DOM stub.
type clientHarness struct {
	vm *goja.Runtime
}

// newClientHarness loads app.js and fails the test if it throws at startup.
func newClientHarness(t *testing.T) *clientHarness {
	t.Helper()
	src, err := assets.ReadFile("assets/app.js")
	if err != nil {
		t.Fatalf("read embedded app.js: %v", err)
	}
	vm := goja.New()
	if _, err := vm.RunString(domStub); err != nil {
		t.Fatalf("dom stub: %v", err)
	}
	if _, err := vm.RunString(string(src)); err != nil {
		t.Fatalf("app.js threw at load: %v", err)
	}
	if vm.Get("__socket").ToObject(vm) == nil {
		t.Fatal("app.js did not open a WebSocket")
	}
	return &clientHarness{vm: vm}
}

// el resolves the stub element with the given id.
func (h *clientHarness) el(t *testing.T, id string) *goja.Object {
	t.Helper()
	return h.call(t, "__el", id).ToObject(h.vm)
}

// call invokes one of the stub's inspection helpers.
func (h *clientHarness) call(t *testing.T, name string, args ...any) goja.Value {
	t.Helper()
	fn, ok := goja.AssertFunction(h.vm.Get(name))
	if !ok {
		t.Fatalf("dom stub has no %s helper", name)
	}
	vals := make([]goja.Value, 0, len(args))
	for _, a := range args {
		vals = append(vals, h.vm.ToValue(a))
	}
	v, err := fn(goja.Undefined(), vals...)
	if err != nil {
		t.Fatalf("%s(...): %v", name, err)
	}
	return v
}

// text returns the rendered text of an element subtree.
func (h *clientHarness) text(t *testing.T, el *goja.Object) string {
	t.Helper()
	return h.call(t, "__text", el).String()
}

// count returns the number of child nodes.
func (h *clientHarness) count(t *testing.T, el *goja.Object) int {
	t.Helper()
	return int(h.call(t, "__count", el).ToInteger())
}

// scrollTop reads an element's scroll offset.
func (h *clientHarness) scrollTop(el *goja.Object) float64 { return el.Get("scrollTop").ToFloat() }

// userScrollTo moves the scrollbar and fires the scroll event, like a user
// dragging it.
func (h *clientHarness) userScrollTo(t *testing.T, el *goja.Object, top float64) {
	t.Helper()
	h.call(t, "__scroll", el, top)
}

// fireFrame flushes the queued animation frames, which is when the client's
// coalesced follow-tail scroll happens. It returns how many callbacks ran.
func (h *clientHarness) fireFrame(t *testing.T) int {
	t.Helper()
	return int(h.call(t, "__fireFrame").ToInteger())
}

// transcript returns the transcript list's element.
func (h *clientHarness) transcript(t *testing.T) *goja.Object { return h.el(t, "scrollback") }

// bottomOf is the scroll offset at the bottom of an element's range — where
// follow-tail must land.
func (h *clientHarness) bottomOf(el *goja.Object) float64 {
	return el.Get("scrollHeight").ToFloat() - el.Get("clientHeight").ToFloat()
}

// setScrollHeight gives the scroll container a scroll range that describes the
// rows that actually exist (the stub's default is a fixed 1000px).
func (h *clientHarness) setScrollHeight(t *testing.T, el *goja.Object, px int) {
	t.Helper()
	h.call(t, "__setScrollHeight", el, px)
}

// deliver feeds one wire document into the client's onmessage handler,
// exactly as a browser would on a WebSocket data frame.
func (h *clientHarness) deliver(t *testing.T, doc string) {
	t.Helper()
	socket := h.vm.Get("__socket").ToObject(h.vm)
	onmsg, ok := goja.AssertFunction(socket.Get("onmessage"))
	if !ok {
		t.Fatal("client has no onmessage handler")
	}
	msg := h.vm.NewObject()
	if err := msg.Set("data", doc); err != nil {
		t.Fatalf("build message: %v", err)
	}
	if _, err := onmsg(goja.Undefined(), msg); err != nil {
		t.Fatalf("client onmessage panicked: %v", err)
	}
}

// frameDoc builds a one-row frame document in the real wire format.
func frameDoc(t *testing.T, row int, text string) string {
	t.Helper()
	return frameDocSeq(t, row, text, 0)
}

// frameDocSeq is frameDoc with an explicit frame sequence number, so a test can
// pin what the client reports as "the last frame I saw".
func frameDocSeq(t *testing.T, row int, text string, seq uint64) string {
	t.Helper()
	b, err := NewFrameCodec().EncodeFrame(&Frame{
		Seq:    seq,
		Cols:   80,
		Rows:   24,
		Cursor: Cursor{Row: row, Col: 0, Visible: true},
		Patches: []RowPatch{{
			Row:  row,
			Runs: []Run{{Text: text}, {Text: strings.Repeat(" ", 79)}},
		}},
	})
	if err != nil {
		t.Fatalf("encode frame: %v", err)
	}
	return string(b)
}

// frameDocRows builds a frame for a screen of `rows` rows, with one styled row
// and Seq omitted (so the client's seq gate always accepts it). It is how a
// resize test drives a geometry change.
func frameDocRows(t *testing.T, rows int, text string) string {
	t.Helper()
	return frameDocCursor(t, rows, 0, 0, text)
}

// frameDocCursor is frameDocRows with the cursor placed on row/col and the text
// painted ON that row: the clipboard chords point at the input line the cursor is
// on, and the cut path measures the selection against that row's box.
func frameDocCursor(t *testing.T, rows, row, col int, text string) string {
	t.Helper()
	b, err := NewFrameCodec().EncodeFrame(&Frame{
		Cols:   80,
		Rows:   rows,
		Cursor: Cursor{Row: row, Col: col, Visible: true},
		Patches: []RowPatch{{
			Row:  row,
			Runs: []Run{{Text: text}},
		}},
	})
	if err != nil {
		t.Fatalf("encode frame: %v", err)
	}
	return string(b)
}
