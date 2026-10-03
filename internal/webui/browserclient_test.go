// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/dop251/goja"
)

// The browser client is the one part of the stack no Go test would otherwise
// execute: app.js paints rows, places the caret and decides whether to follow
// the tail. These tests run the REAL embedded script under a JavaScript engine
// against a minimal DOM stub, so a regression in the shipped client (not a
// copy of it) fails the build.

// domStub is the JS bootstrap that gives app.js just enough of a browser: the
// elements it looks up, element creation, event registration and a WebSocket
// whose onmessage the test drives. window.__text/window.__count/window.__scroll
// are read-only inspection helpers for the assertions.
const domStub = `
(function () {
  // In a browser "window" IS the global object; goja's is globalThis, so alias
  // it once and app.js sees the environment it expects.
  globalThis.window = globalThis;
  function El(tag) {
    this.tagName = tag;
    this.children = [];
    this.style = {};
    this.dataset = {};
    this.listeners = {};
    this.textContent = "";
    this.className = "";
    this.hidden = false;
    this.scrollTop = 0;
    this.scrollHeight = 1000;
    this.clientHeight = 100;
    this.clientWidth = 800;
  }
  El.prototype.appendChild = function (c) { this.children.push(c); return c; };
  El.prototype.removeChild = function (c) {
    var i = this.children.indexOf(c);
    if (i >= 0) this.children.splice(i, 1);
  };
  El.prototype.addEventListener = function (t, fn) {
    (this.listeners[t] = this.listeners[t] || []).push(fn);
  };
  El.prototype.getBoundingClientRect = function () {
    window.__measureCalls = (window.__measureCalls || 0) + 1;
    return { width: 8, height: 16 };
  };
  window.__measures = function () { return window.__measureCalls || 0; };
  El.prototype.focus = function () {};
  El.prototype.setAttribute = function () {};
  El.prototype.fire = function (t) {
    var fns = this.listeners[t] || [];
    for (var i = 0; i < fns.length; i++) fns[i]({});
  };

  // FormData stub: records the appended part so a test can assert the page
  // posted a file field the server can read.
  window.FormData = function () { this.parts = []; };
  window.FormData.prototype.append = function (name, blob, filename) {
    this.parts.push({ name: name, blob: blob, filename: filename });
  };

  // Minimal synchronous Promise, A+ shaped (then returns a NEW promise). goja
  // exposes no Promise global and the page's upload path is its only user; what
  // the assertions read is the resolution *order*, not real async timing.
  window.Promise = function (exec) {
    var state = 0, value, waiting = [];

    function settle(next, v) {
      if (state !== 0) return;
      state = next;
      value = v;
      var pending = waiting.slice();
      waiting.length = 0;
      for (var i = 0; i < pending.length; i++) dispatch(pending[i]);
    }

    // settleOn delivers a value either to this promise or to a derived one
    // (a promise has a single value; then() chains make new ones).
    function settleOn(next, v, target) {
      if (target) { target.__settle(next, v); return; }
      settle(next, v);
    }

    // adoptInto folds a handler result into a derived promise, flattening a
    // returned thenable (the Promise resolution procedure, synchronous flavour).
    function adoptInto(target, r) {
      if (r && typeof r.then === "function") {
        r.then(function (x) { settleOn(1, x, target); }, function (e) { settleOn(2, e, target); });
        return;
      }
      settleOn(1, r, target);
    }

    function dispatch(h) {
      if (state === 1) {
        if (!h.onOk) { settleOn(1, value, h.next); return; }
        var r;
        try { r = h.onOk(value); } catch (e) { settleOn(2, e, h.next); return; }
        adoptInto(h.next, r);
        return;
      }
      if (state === 2) {
        if (!h.onErr) { settleOn(2, value, h.next); return; }
        var e2;
        try { e2 = h.onErr(value); } catch (e3) { settleOn(2, e3, h.next); return; }
        adoptInto(h.next, e2);
        return;
      }
    }

    var p = {
      __settle: function (next, v) { settle(next, v); },
      then: function (onOk, onErr) {
        var next = new window.Promise(function () {});
        if (state === 0) waiting.push({ onOk: onOk, onErr: onErr, next: next });
        else dispatch({ onOk: onOk, onErr: onErr, next: next });
        return next;
      },
      catch: function (onErr) {
        return p.then(undefined, onErr);
      }
    };
    try {
      exec(function (v) {
        if (v && typeof v.then === "function") {
          v.then(function (x) { settle(1, x); }, function (e) { settle(2, e); });
          return;
        }
        settle(1, v);
      }, function (e) { settle(2, e); });
    } catch (e) {
      settle(2, e);
    }
    return p;
  };
  window.Promise.resolve = function (v) {
    return new window.Promise(function (res) { res(v); });
  };

  var els = {};
  window.__el = function (id) {
    if (!els[id]) els[id] = new El("div");
    return els[id];
  };
  var docListeners = {};
  window.document = {
    getElementById: function (id) { return window.__el(id); },
    createElement: function (tag) { return new El(tag); },
    addEventListener: function (t, fn) {
      (docListeners[t] = docListeners[t] || []).push(fn);
    },
    body: new El("div")
  };
  // fire dispatches a synthetic event through the document listeners, the way
  // a real keydown/paste reaches the page.
  window.__fire = function (t, ev) {
    var fns = docListeners[t] || [];
    var e = ev || {};
    e.preventDefault = e.preventDefault || function () { e.defaultPrevented = true; };
    for (var i = 0; i < fns.length; i++) fns[i](e);
    return e.defaultPrevented === true;
  };
  window.__listeners = function (t) { return (docListeners[t] || []).length; };
    var winListeners = {};
    window.addEventListener = function (t, fn) {
      (winListeners[t] = winListeners[t] || []).push(fn);
    };
    // __fireWindow dispatches a window-level event (resize, …) to app.js.
    window.__fireWindow = function (t) {
      var fns = winListeners[t] || [];
      for (var i = 0; i < fns.length; i++) fns[i]({});
      return fns.length;
    };

      // Timer stubs: app.js uses setTimeout/clearTimeout for the WS handshake
      // watchdog and reconnect backoff. Tests need to fire them deterministically,
      // so each timer is parked in a list with an id and __fireTimer runs one.
      var timers = [];
      var timerSeq = 0;
      window.setTimeout = function (fn, ms) {
        var id = ++timerSeq;
        timers.push({ id: id, fn: fn, ms: ms, cancelled: false });
        return id;
      };
      window.clearTimeout = function (id) {
        for (var i = 0; i < timers.length; i++) {
          if (timers[i].id === id) timers[i].cancelled = true;
        }
      };
      window.__timers = function () { return timers; };
      // __fireTimer runs the first live timer and removes it.
      window.__fireTimer = function () {
        for (var i = 0; i < timers.length; i++) {
          if (!timers[i].cancelled) {
            var t = timers.splice(i, 1)[0];
            t.fn();
            return true;
          }
        }
        return false;
      };

      window.GOA_SESSION = "sess-1";
      // location records a navigation (session rotation follows one) instead of
      // pretending the page can reload itself under the test.
      window.location = {
        protocol: "http:",
        host: "localhost:8080",
        replace: function (url) { window.__replacedUrl = url; }
      };
      window.__replaced = function () { return window.__replacedUrl || ""; };
      window.WebSocket = function (url) {
        this.url = url;
        this.readyState = 1;
        this.sent = [];
        this.send = function (data) { this.sent.push(data); };
        this.close = function () { this.readyState = 3; window.__closed = this; };
        window.__socket = this;
      };
      window.WebSocket.OPEN = 1;

      // EventSource stub for the SSE fallback. Records every instance (a resume
            // opens a second one) and delivers messages to the live stream's
            // onmessage, exactly as a browser would.
            window.EventSource = function (url) {
              this.url = url;
              this.onmessage = null;
              this.onopen = null;
              this.onerror = null;
              this.closed = false;
              this.close = function () { this.closed = true; };
              window.__streams = window.__streams || [];
              window.__streams.push(this);
              window.__events = this;
            };
            window.__emitEvent = function (doc) {
              var es = window.__events;
              if (es && es.onmessage) es.onmessage({ data: doc });
            };
            window.__hasEventSource = function () { return window.__events !== undefined; };
            // __streamError drives the live stream into its error state, which is what
            // a dropped connection looks like to the page.
            window.__streamError = function () {
              var es = window.__events;
              if (es && es.onerror) es.onerror();
            };
            window.__streamCount = function () { return (window.__streams || []).length; };

  // __sent returns every message the page pushed to the socket, parsed.
  window.__sent = function () {
    return (window.__socket.sent || []).map(function (raw) { return JSON.parse(raw); });
  };
  // __deliver pushes a server message into the page (socket.onmessage).
  window.__deliver = function (msg) {
    if (window.__socket.onmessage) window.__socket.onmessage({ data: JSON.stringify(msg) });
  };
  window.__opened = function () {
        if (window.__socket.onopen) window.__socket.onopen();
      };
      // __wsFailed simulates a handshake that never opens: the socket errors and
      // closes before onopen ever fired. This is what an upgrade-blocked proxy
      // looks like to the page, and it must trigger the SSE fallback.
      window.__wsFailed = function () {
        var s = window.__socket;
        if (s.onerror) s.onerror();
        s.readyState = 3;
        if (s.onclose) s.onclose();
      };
      // __wsDropped simulates a live socket dropping after it had opened: the page
      // must retry WS (not fall back), because the handshake itself did succeed.
      window.__wsDropped = function () {
        var s = window.__socket;
        s.readyState = 3;
        if (s.onclose) s.onclose();
      };

  window.__text = function (el) {
    var out = el.textContent || "";
    for (var i = 0; i < el.children.length; i++) out += window.__text(el.children[i]);
    return out;
  };
  window.__count = function (el) { return el.children.length; };
  window.__scroll = function (el, top) {
    el.scrollTop = top;
    el.fire("scroll");
  };
})();
`

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

// TestClientJS_FollowTailScroll pins the follow-tail contract (spec §7.3):
// while the client is at the bottom every frame pulls the view to the newest
// output; as soon as the user scrolls up, live streaming must NOT move the
// view; returning to the bottom re-arms following.
func TestClientJS_FollowTailScroll(t *testing.T) {
	h := newClientHarness(t)
	scroll := h.el(t, "scrollback")

	// Streaming while at the bottom: every frame pins the view to the newest
	// output.
	h.deliver(t, frameDoc(t, 3, "first line"))
	if got := h.scrollTop(scroll); got != 1000 {
		t.Fatalf("follow-tail did not scroll to the bottom while following: scrollTop=%v", got)
	}
	h.deliver(t, frameDoc(t, 4, "second line"))
	if got := h.scrollTop(scroll); got != 1000 {
		t.Fatalf("follow-tail lost the tail on the next frame: scrollTop=%v", got)
	}

	// The user scrolls up to read earlier output: streaming must not yank them
	// back down.
	h.userScrollTo(t, scroll, 0)
	h.deliver(t, frameDoc(t, 5, "third line"))
	if got := h.scrollTop(scroll); got != 0 {
		t.Errorf("streaming moved the view while the user had scrolled up: scrollTop=%v", got)
	}

	// Scrolling back to the bottom re-arms follow-tail.
	h.userScrollTo(t, scroll, 900)
	h.deliver(t, frameDoc(t, 6, "fourth line"))
	if got := h.scrollTop(scroll); got != 1000 {
		t.Errorf("follow-tail did not re-arm at the bottom: scrollTop=%v", got)
	}
}

// TestClientJS_ScrollbackRespectsFollowTail pins that the transcript list only
// yanks the view while the client is following.
func TestClientJS_ScrollbackRespectsFollowTail(t *testing.T) {
	h := newClientHarness(t)
	scroll := h.el(t, "scrollback")

	doc := func() string {
		b, err := NewFrameCodec().EncodeScrollback(1, []RowPatch{{Row: 0, Runs: []Run{{Text: "scrolled off"}}}})
		if err != nil {
			t.Fatalf("encode scrollback: %v", err)
		}
		return string(b)
	}

	h.deliver(t, doc())
	if got := h.scrollTop(scroll); got != 1000 {
		t.Errorf("scrollback while following did not keep the tail in view: scrollTop=%v", got)
	}
	if !strings.Contains(h.text(t, scroll), "scrolled off") {
		t.Error("scrollback row was not appended to the transcript list")
	}

	h.userScrollTo(t, scroll, 100)
	h.deliver(t, doc())
	if got := h.scrollTop(scroll); got != 100 {
		t.Errorf("scrollback moved the view while the user had scrolled up: scrollTop=%v", got)
	}
}

// TestClientJS_PaintsRowsAndCaret pins the render contract: a frame's runs
// land in the grid as text and the caret is placed at the cursor cell.
func TestClientJS_PaintsRowsAndCaret(t *testing.T) {
	h := newClientHarness(t)
	grid := h.el(t, "grid")
	caret := h.el(t, "caret")

	h.deliver(t, frameDoc(t, 2, "hello grid"))

	if n := h.count(t, grid); n == 0 {
		t.Fatal("frame produced no grid rows")
	}
	if got := h.text(t, grid); !strings.Contains(got, "hello grid") {
		t.Errorf("grid never painted %q; painted %q", "hello grid", strings.TrimSpace(got))
	}
	if caret.Get("hidden").ToBoolean() {
		t.Error("caret hidden although the frame reported a visible cursor")
	}
	// Cell metrics are 8x16 in the stub and the caret adds an 8px pad, so a
	// cursor at column 0 lands at left=8px, row 2 at top=8+2*16=40px.
	style := caret.Get("style").ToObject(h.vm)
	if left := style.Get("left").String(); left != "8px" {
		t.Errorf("caret left = %q, want 8px for a cursor at column 0", left)
	}
	if top := style.Get("top").String(); top != "40px" {
		t.Errorf("caret top = %q, want 40px for a cursor at row 2", top)
	}

	// A hidden cursor must hide the caret.
	h.deliver(t, hiddenCursorFrame(t))
	if !caret.Get("hidden").ToBoolean() {
		t.Error("caret still visible after a frame reported a hidden cursor")
	}
}

// hiddenCursorFrame is a frame whose cursor is not visible.
func hiddenCursorFrame(t *testing.T) string {
	t.Helper()
	b, err := NewFrameCodec().EncodeFrame(&Frame{
		Cols:    80,
		Rows:    24,
		Cursor:  Cursor{Row: 1, Col: 3},
		Patches: []RowPatch{{Row: 1, Runs: []Run{{Text: strings.Repeat(" ", 79)}}}},
	})
	if err != nil {
		t.Fatalf("encode frame: %v", err)
	}
	return string(b)
}

// TestClientJS_KeydownSendsDescriptor pins the input contract: the page reports
// WHAT was pressed and lets the server encode it (spec §7.5). It must never
// compute bytes itself, or the byte table would exist twice and drift.
func TestClientJS_KeydownSendsDescriptor(t *testing.T) {
	h := newClientHarness(t)
	h.open(t)

	h.keydown(t, map[string]any{"key": "a", "code": "KeyA"})
	h.keydown(t, map[string]any{"key": "w", "code": "KeyW", "ctrlKey": true})
	h.keydown(t, map[string]any{"key": "ArrowLeft", "code": "ArrowLeft", "altKey": true})

	sent := h.sent(t)
	if len(sent) != 3 {
		t.Fatalf("sent %d messages, want 3", len(sent))
	}
	for i, m := range sent {
		if m["t"] != "key" {
			t.Errorf("message %d type = %v, want \"key\"", i, m["t"])
		}
	}
	first := sent[0]["key"].(map[string]any)
	if first["key"] != "a" || first["code"] != "KeyA" {
		t.Errorf("descriptor = %v, want the raw key and code", first)
	}
	if _, ok := first["data"]; ok {
		t.Errorf("descriptor carries pre-encoded bytes: %v", first)
	}
	ctrl := sent[1]["key"].(map[string]any)
	if ctrl["ctrl"] != true {
		t.Errorf("ctrl flag lost: %v", ctrl)
	}
	alt := sent[2]["key"].(map[string]any)
	if alt["alt"] != true {
		t.Errorf("alt flag lost: %v", alt)
	}
}

// A read-only notice must stop the page from typing: the server would drop the
// bytes anyway, and a viewer must never look like it is driving.
func TestClientJS_ReadOnlyStopsInput(t *testing.T) {
	h := newClientHarness(t)
	h.open(t)

	h.keydown(t, map[string]any{"key": "a", "code": "KeyA"})
	if got := len(h.sent(t)); got != 1 {
		t.Fatalf("before read-only: %d messages, want 1", got)
	}

	h.deliver(t, `{"t":"read_only","text":"server is read-only"}`)
	prevented := h.keydown(t, map[string]any{"key": "b", "code": "KeyB"})
	if prevented {
		t.Error("read-only client still preventDefault-ed the keystroke")
	}
	if got := len(h.sent(t)); got != 1 {
		t.Fatalf("read-only client sent %d messages, want still 1", got)
	}
	if status := h.textOf(t, "status"); !strings.Contains(status, "read-only") {
		t.Errorf("status = %q, does not show the viewer mode", status)
	}
}

// Clipboard text is inserted as-is (one raw input event), so the editor applies
// its own paste handling exactly as for a terminal paste.
func TestClientJS_PasteSendsRawText(t *testing.T) {
	h := newClientHarness(t)
	h.open(t)

	pasteEvent := h.paste(t, "two\nlines\n")
	if !pasteEvent {
		t.Error("text paste did not preventDefault")
	}
	sent := h.sent(t)
	if len(sent) != 1 || sent[0]["t"] != "input" || sent[0]["data"] != "two\nlines\n" {
		t.Fatalf("paste produced %v, want one raw input message", sent)
	}
}

// An image has no textual form: the page uploads it and inserts the stored path,
// which is what the terminal editor inserts for a clipboard image.
func TestClientJS_ImagePasteUploadsThenInsertsPath(t *testing.T) {
	h := newClientHarness(t)
	h.open(t)
	h.stubUpload(t, "/tmp/goa-upload-123.png")

	prevented := h.pasteImage(t, "image/png")
	if !prevented {
		t.Error("image paste did not preventDefault")
	}
	if got := h.uploadCalls(t); got != 1 {
		t.Fatalf("upload calls = %d, want 1", got)
	}
	sent := h.sent(t)
	if len(sent) != 1 || sent[0]["t"] != "input" || sent[0]["data"] != "/tmp/goa-upload-123.png" {
		t.Fatalf("image paste produced %v, want the uploaded path as input", sent)
	}
}

// A failed upload must be reported, not silently swallowed: the user pasted an
// image and nothing arrived at the agent.
func TestClientJS_ImagePasteFailureIsReported(t *testing.T) {
	h := newClientHarness(t)
	h.open(t)
	h.stubUploadFailure(t, "input rejected: read-only mode")

	h.pasteImage(t, "image/png")
	if got := h.uploadCalls(t); got != 1 {
		t.Fatalf("upload calls = %d, want 1", got)
	}
	if status := h.textOf(t, "status"); !strings.Contains(status, "image upload failed") {
		t.Errorf("status = %q, want the upload failure", status)
	}
	if sent := h.sent(t); len(sent) != 0 {
		t.Errorf("failed upload still sent %v", sent)
	}
}

// ---------------------------------------------------------------- input helpers

// open fires the socket onopen handler, which is where the page sends its first
// resize.
func (h *clientHarness) open(t *testing.T) {
	t.Helper()
	h.call(t, "__opened")
	h.clearSent(t)
}

// clearSent drops the messages recorded so far, so a test can assert on what a
// single action produced.
func (h *clientHarness) clearSent(t *testing.T) {
	t.Helper()
	socket := h.vm.Get("__socket").ToObject(h.vm)
	_ = socket.Set("sent", h.vm.NewArray())
}

// sent returns the parsed messages the page pushed to the socket.
func (h *clientHarness) sent(t *testing.T) []map[string]any {
	t.Helper()
	raw := h.call(t, "__sent").ToObject(h.vm)
	n := len(raw.Keys())
	out := make([]map[string]any, 0, n)
	for _, k := range raw.Keys() {
		var m map[string]any
		if err := h.vm.ExportTo(raw.Get(k), &m); err != nil {
			t.Fatalf("export sent message: %v", err)
		}
		out = append(out, m)
	}
	return out
}

// keydown dispatches a synthetic keydown and reports whether the page claimed
// it (preventDefault).
func (h *clientHarness) keydown(t *testing.T, ev map[string]any) bool {
	t.Helper()
	return h.call(t, "__fire", "keydown", h.toValue(ev)).ToBoolean()
}

// paste dispatches a synthetic text paste.
func (h *clientHarness) paste(t *testing.T, text string) bool {
	t.Helper()
	ev := map[string]any{
		"clipboardData": map[string]any{
			"getData": func(kind string) string {
				if kind == "text" {
					return text
				}
				return ""
			},
			"items": []any{},
		},
	}
	return h.call(t, "__fire", "paste", h.toValue(ev)).ToBoolean()
}

// pasteImage dispatches a synthetic image paste (no text flavour).
func (h *clientHarness) pasteImage(t *testing.T, mime string) bool {
	t.Helper()
	file := map[string]any{"name": "pasted", "type": mime, "size": 3}
	ev := map[string]any{
		"clipboardData": map[string]any{
			"getData": func(string) string { return "" },
			"items": []any{map[string]any{
				"kind":      "file",
				"type":      mime,
				"getAsFile": func() any { return file },
			}},
		},
	}
	return h.call(t, "__fire", "paste", h.toValue(ev)).ToBoolean()
}

// textOf returns the rendered text of a stub element by id.
func (h *clientHarness) textOf(t *testing.T, id string) string {
	t.Helper()
	return h.text(t, h.el(t, id))
}

// stubUpload replaces fetch with a resolver returning this path, and records the
// call count.
func (h *clientHarness) stubUpload(t *testing.T, path string) {
	t.Helper()
	h.stubFetch(t, map[string]any{"ok": true, "path": path})
}

// stubUploadFailure replaces fetch with a failing response.
func (h *clientHarness) stubUploadFailure(t *testing.T, msg string) {
	t.Helper()
	h.stubFetch(t, map[string]any{"ok": false, "body": msg})
}

// stubFetch installs a JS fetch stub that counts calls and resolves to outcome.
// It is written in JS because the promise chain the page builds must run in the
// same VM — a Go-side stub cannot hand back thenables.
func (h *clientHarness) stubFetch(t *testing.T, outcome map[string]any) {
	t.Helper()
	src := `
(function (ok, body) {
  var path = body || "";
  window.__uploads = 0;
  window.fetch = function (url, opts) {
    window.__uploads++;
    return window.Promise.resolve({
      ok: ok,
      json: function () {
        return window.Promise.resolve(ok ? { path: path } : {});
      },
      text: function () { return window.Promise.resolve(body || ""); }
    });
  };
  window.__uploadCalls = function () { return window.__uploads; };
})(%v, %v);
`
	ok, _ := outcome["ok"].(bool)
	msg, _ := outcome["body"].(string)
	path, _ := outcome["path"].(string)
	if _, err := h.vm.RunString(fmt.Sprintf(src, ok, strconv.Quote(msg+path))); err != nil {
		t.Fatalf("install fetch stub: %v", err)
	}
}

// uploadCalls reports how many times the stubbed fetch was invoked.
func (h *clientHarness) uploadCalls(t *testing.T) int {
	t.Helper()
	return int(h.call(t, "__uploadCalls").ToInteger())
}

// toValue converts a Go value into a JS value for the stub call.
func (h *clientHarness) toValue(v any) goja.Value { return h.vm.ToValue(v) }

// ------------------------------------------------------- transport fallback

// stubPosts installs a fetch stub that records every POST the page makes while
// the SSE fallback is active. It is distinct from stubFetch (which serves the
// upload flow) because these tests need to inspect url + body, not a resolved
// payload.
func (h *clientHarness) stubPosts(t *testing.T) {
	t.Helper()
	src := `
  (function () {
      window.__posts = [];
      window.fetch = function (url, opts) {
        window.__posts.push({ url: url, body: (opts && opts.body) || "" });
        return window.Promise.resolve({ ok: true, json: function () { return window.Promise.resolve({}); } });
      };
      window.__postedPosts = function () { return window.__posts; };
    })();
    `
	if _, err := h.vm.RunString(src); err != nil {
		t.Fatalf("install post stub: %v", err)
	}
}

// posts returns the POSTs the page made, decoded.
func (h *clientHarness) posts(t *testing.T) []map[string]any {
	t.Helper()
	raw := h.call(t, "__postedPosts").ToObject(h.vm)
	out := make([]map[string]any, 0, len(raw.Keys()))
	for _, k := range raw.Keys() {
		var m map[string]any
		if err := h.vm.ExportTo(raw.Get(k), &m); err != nil {
			t.Fatalf("export post: %v", err)
		}
		out = append(out, m)
	}
	return out
}

// hasEventSource reports whether the page opened an SSE stream.
func (h *clientHarness) hasEventSource(t *testing.T) bool {
	t.Helper()
	return h.call(t, "__hasEventSource").ToBoolean()
}

// failHandshake drives the stub socket through a failed upgrade (error + close
// before open) — what an upgrade-blocking proxy looks like to the page.
func (h *clientHarness) failHandshake(t *testing.T) {
	t.Helper()
	h.call(t, "__wsFailed")
}

// An upgrade-blocked connection must fall back to SSE for the rest of the page
// load, and the badge must say the transport is degraded (spec §11.2).
func TestClientJS_WebSocketFailureFallsBackToSSE(t *testing.T) {
	h := newClientHarness(t)
	if h.hasEventSource(t) {
		t.Fatal("page opened an event stream before the socket failed")
	}

	h.failHandshake(t)

	if !h.hasEventSource(t) {
		t.Fatal("failed handshake did not open the SSE stream")
	}
	es := h.vm.Get("__events").ToObject(h.vm)
	if url := es.Get("url").String(); !strings.HasPrefix(url, "/events?s=") {
		t.Errorf("EventSource url = %q, want the /events fallback", url)
	}
	if status := h.textOf(t, "status"); !strings.Contains(status, "degraded") {
		t.Errorf("status = %q, want a degraded badge", status)
	}
}

// Once the socket had opened and then dropped, the page must retry the
// WebSocket — the handshake itself worked, so SSE is the wrong answer.
func TestClientJS_LiveSocketDropRetriesWebSocketNotSSE(t *testing.T) {
	h := newClientHarness(t)
	h.open(t)

	h.call(t, "__wsDropped")
	if h.hasEventSource(t) {
		t.Error("a dropped-but-established socket must not trigger the SSE fallback")
	}
	if !h.call(t, "__fireTimer").ToBoolean() {
		t.Fatal("dropping a live socket did not schedule a reconnect")
	}
	if h.vm.Get("__socket").ToObject(h.vm).Get("readyState").ToInteger() != 1 {
		t.Error("reconnect did not open a fresh WebSocket")
	}
}

// Frames delivered over the SSE stream must paint exactly like WebSocket frames:
// one wire format, one render path (spec §11.2).
func TestClientJS_SSEFramesPaintTheGrid(t *testing.T) {
	h := newClientHarness(t)
	h.failHandshake(t)

	h.call(t, "__emitEvent", frameDoc(t, 2, "degraded line"))

	grid := h.el(t, "grid")
	if got := h.text(t, grid); !strings.Contains(got, "degraded line") {
		t.Fatalf("grid = %q, want the SSE-delivered text", got)
	}
}

// With the fallback active, a keystroke must be POSTed as a *descriptor* to
// /key, not as empty raw bytes: the server still owns the byte table, so losing
// the socket cannot silently degrade typing into nothing.
func TestClientJS_SSEKeydownGoesOutAsKeyPost(t *testing.T) {
	h := newClientHarness(t)
	h.failHandshake(t)
	h.stubPosts(t)

	h.keydown(t, map[string]any{"key": "a", "code": "KeyA"})

	posts := h.posts(t)
	if len(posts) != 1 {
		t.Fatalf("posted %d times, want 1", len(posts))
	}
	if url := posts[0]["url"]; url != "/key" {
		t.Errorf("posted to %v, want /key", url)
	}
	if sent := h.sent(t); len(sent) != 0 {
		t.Errorf("page also wrote to the dead socket: %v", sent)
	}
}

// Paste over the fallback is one POSTed payload, exactly as over the socket.
func TestClientJS_SSEPasteGoesOutAsPost(t *testing.T) {
	h := newClientHarness(t)
	h.failHandshake(t)
	h.stubPosts(t)

	h.paste(t, "pasted\n")

	posts := h.posts(t)
	if len(posts) != 1 || posts[0]["url"] != "/input" {
		t.Fatalf("paste produced %v, want one POST to /input", posts)
	}
}

// A resize over the fallback must go to /resize, not /input: they are different
// endpoints with different payloads.
func TestClientJS_SSEResizeGoesToResizeEndpoint(t *testing.T) {
	h := newClientHarness(t)
	h.failHandshake(t)
	h.stubPosts(t)

	if got := int(h.call(t, "__fireWindow", "resize").ToInteger()); got == 0 {
		t.Fatal("page registered no resize listener")
	}
	posts := h.posts(t)
	if len(posts) == 0 || posts[0]["url"] != "/resize" {
		t.Fatalf("resize produced %v, want a POST to /resize", posts)
	}
}

// The reconnect handshake must tell the server the last frame seq it saw, so a
// dropped socket resumes where it left off (spec §8).
func TestClientJS_ReconnectSendsHelloWithLastSeq(t *testing.T) {
	h := newClientHarness(t)
	h.deliver(t, frameDocSeq(t, 1, "before drop", 42))
	h.call(t, "__opened")

	sent := h.sent(t)
	var hello map[string]any
	for _, m := range sent {
		if m["t"] == "hello" {
			hello = m
		}
	}
	if hello == nil {
		t.Fatalf("reconnect sent no hello: %v", sent)
	}
	if since := numOf(hello["since"]); since != 42 {
		t.Errorf("hello reported since=%v, want the last frame's seq 42", hello["since"])
	}
}

// The first connect must also send a hello (since 0 on a fresh page) so the
// handshake is uniform from the very first socket.
func TestClientJS_FirstConnectSendsHello(t *testing.T) {
	h := newClientHarness(t)
	h.call(t, "__opened")

	sent := h.sent(t)
	if len(sent) == 0 || sent[0]["t"] != "hello" {
		t.Fatalf("first connect sent %v, want a hello first", sent)
	}
	if since := numOf(sent[0]["since"]); since != 0 {
		t.Errorf("fresh page reported since=%v, want 0", sent[0]["since"])
	}
}

// numOf reads a JSON number out of an exported message. goja hands back a
// whole number as int64 and a fractional one as float64, so a test must not
// assume either.
func numOf(v any) float64 {
	switch n := v.(type) {
	case int64:
		return float64(n)
	case int:
		return float64(n)
	case float64:
		return n
	}
	return -1
}

// A read-only notice arriving over the fallback must still stop the page from
// typing — the badge is the only cue the viewer gets.
func TestClientJS_SSEReadOnlyStopsInput(t *testing.T) {
	h := newClientHarness(t)
	h.failHandshake(t)
	h.stubPosts(t)

	h.call(t, "__emitEvent", `{"t":"read_only","text":"server is read-only"}`)
	h.keydown(t, map[string]any{"key": "a", "code": "KeyA"})

	if posts := h.posts(t); len(posts) != 0 {
		t.Errorf("read-only viewer still posted %v", posts)
	}
	if status := h.textOf(t, "status"); !strings.Contains(status, "read-only") {
		t.Errorf("status = %q, want the viewer mode announced", status)
	}
}

// The POST bodies the page emits over the fallback must be exactly what the
// server's handlers parse — the wire contract is shared, not duplicated.
func TestClientJS_SSEPostBodiesMatchTheServerSchema(t *testing.T) {
	for _, tc := range []struct {
		name string
		want string
		act  func(h *clientHarness)
		// check asserts the decoded clientMsg the server would see.
		check func(t *testing.T, msg clientMsg)
	}{
		{
			name: "key",
			want: "/key",
			act:  func(h *clientHarness) { h.keydown(t, map[string]any{"key": "a", "code": "KeyA", "ctrlKey": true}) },
			check: func(t *testing.T, msg clientMsg) {
				if msg.Key.Code != "KeyA" {
					t.Errorf("key descriptor lost its code: %+v", msg.Key)
				}
			},
		},
		{
			name: "paste",
			want: "/input",
			act:  func(h *clientHarness) { h.paste(t, "pasted bytes") },
			check: func(t *testing.T, msg clientMsg) {
				if msg.Data != "pasted bytes" {
					t.Errorf("paste payload = %q", msg.Data)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newClientHarness(t)
			h.failHandshake(t)
			h.stubPosts(t)
			tc.act(h)

			posts := h.posts(t)
			if len(posts) != 1 {
				t.Fatalf("posted %d times, want 1", len(posts))
			}
			if posts[0]["url"] != tc.want {
				t.Errorf("posted to %v, want %v", posts[0]["url"], tc.want)
			}
			raw, _ := posts[0]["body"].(string)
			var msg clientMsg
			if err := json.Unmarshal([]byte(raw), &msg); err != nil {
				t.Fatalf("server could not parse %q: %v", raw, err)
			}
			tc.check(t, msg)
		})
	}
}
