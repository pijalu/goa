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
    this._text = "";
    this.className = "";
    this.hidden = false;
    this.scrollHeight = 1000;
    this.clientHeight = 100;
    this.clientWidth = 800;
    this._scrollTop = 0;
  }
  // textContent is a real accessor, not a plain field: assigning to it REPLACES
  // the element's children (DOM spec), which is how app.js clears a row or the
  // whole transcript. A stub that kept the children would let a page that clears
  // by textContent look like one that appends — the exact difference between
  // painting a line once and painting it twice.
  //
  // Reading it back concatenates the descendants' text, exactly as the DOM does:
  // the page reads a row's text to decide whether the line holds a two-cell
  // glyph, and a getter that only echoed the last assignment would report every
  // row as empty.
  Object.defineProperty(El.prototype, "textContent", {
    get: function () {
      var out = this._text || "";
      for (var i = 0; i < this.children.length; i++) out += this.children[i].textContent || "";
      return out;
    },
    set: function (v) {
      this._text = v === undefined || v === null ? "" : String(v);
      this.children.length = 0;
    }
  });
  // scrollTop is clamped to the scrollable range, like the real property: a page
  // that assigns scrollHeight lands ON the bottom rather than past it, so "is
  // the view at the bottom" is a question the stub answers honestly.
  Object.defineProperty(El.prototype, "scrollTop", {
    get: function () { return this._scrollTop; },
    set: function (v) {
      var max = this.scrollHeight - this.clientHeight;
      if (max < 0) max = 0;
      this._scrollTop = Math.max(0, Math.min(v, max));
    }
  });
  El.prototype.appendChild = function (c) { this.children.push(c); return c; };
  El.prototype.removeChild = function (c) {
    var i = this.children.indexOf(c);
    if (i >= 0) this.children.splice(i, 1);
  };
  // insertBefore(node, ref) mirrors the DOM: a null/absent ref appends. app.js
  // uses it to keep the transcript spacer ahead of the windowed rows.
  El.prototype.insertBefore = function (c, ref) {
    var i = ref ? this.children.indexOf(ref) : -1;
    if (i < 0) { this.children.push(c); return c; }
    this.children.splice(i, 0, c);
    return c;
  };
  El.prototype.addEventListener = function (t, fn) {
    (this.listeners[t] = this.listeners[t] || []).push(fn);
  };
  El.prototype.getBoundingClientRect = function () {
    window.__measureCalls = (window.__measureCalls || 0) + 1;
    // A cell is 8x16 in this stub, and the box starts at the origin: the cut
    // path compares a selection's client rects against the cursor row's box, so
    // the stub has to publish the same coordinate space the rects use.
    return { left: 0, top: 0, right: 8, bottom: 16, width: 8, height: 16 };
  };
  window.__measures = function () { return window.__measureCalls || 0; };
  El.prototype.focus = function () { document.activeElement = this; };
  El.prototype.select = function () { this.selected = true; };
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

  // __clipItem builds a clipboard item the way the async clipboard reports one:
  // the MIME types it carries plus getType(type) -> Blob, the Blob carrying
  // text(). The image path reads a Blob the same way and uploads it.
  window.__clipItem = function (type, body) {
    return {
      types: [type],
      getType: function () {
        return new window.Promise(function (res) {
          res({ text: function () { return new window.Promise(function (r2) { r2(body); }); } });
        });
      }
    };
  };

  // The async clipboard: the copy/cut/paste chords go through it where it
  // exists, and a refused write falls back to execCommand. The stub records
  // every call and lets a test decide what the clipboard holds, whether a write
  // lands and whether a read is refused — the three things that pick a path.
  window.__clipboard = { text: "", writes: [], reads: 0, items: null, denyWrites: false, denyReads: false };
  window.navigator = {
    clipboard: {
      writeText: function (t) {
        window.__clipboard.writes.push(t);
        if (window.__clipboard.denyWrites) {
          return new window.Promise(function (_, rej) { rej(new Error("denied")); });
        }
        window.__clipboard.text = t;
        return new window.Promise(function (res) { res(); });
      },
      readText: function () {
        window.__clipboard.reads++;
        if (window.__clipboard.denyReads) {
          return new window.Promise(function (_, rej) { rej(new Error("denied")); });
        }
        return new window.Promise(function (res) { res(window.__clipboard.text); });
      },
      read: function () {
        window.__clipboard.reads++;
        if (window.__clipboard.denyReads) {
          return new window.Promise(function (_, rej) { rej(new Error("denied")); });
        }
        var items = window.__clipboard.items;
        if (!items) items = window.__clipboard.text ? [window.__clipItem("text/plain", window.__clipboard.text)] : [];
        return new window.Promise(function (res) { res(items); });
      }
    }
  };
  window.__setClipboard = function (text) { window.__clipboard.text = text; window.__clipboard.items = null; };
  window.__setClipboardItems = function (items) { window.__clipboard.items = items; };
  window.__denyClipboardWrites = function (deny) { window.__clipboard.denyWrites = !!deny; };
  window.__dropClipboardAPI = function () { delete window.navigator.clipboard; };
  window.__clipboardWrites = function () { return window.__clipboard.writes; };
  window.__clipboardText = function () { return window.__clipboard.text; };
  window.__clipboardReads = function () { return window.__clipboard.reads; };
  // __execCommand is the legacy write path: it records the command and reports
  // whatever a test configured (a browser refuses it when it has no clipboard).
  window.__execResult = true;
  window.__execCommands = [];
  window.__setExecResult = function (ok) { window.__execResult = !!ok; };

  var els = {};
  // PARENT mirrors index.html's nesting, so a stub element is reachable from
  // its container the way the real DOM nests them (a test reading #grid's text
  // must see the rows inside #rows).
  var PARENT = { rows: "grid", caret: "grid", grid: "screen", scrollback: "screen" };
  window.__el = function (id) {
    if (!els[id]) {
      els[id] = new El("div");
      // index.html gives every element its id; focus() and the clipboard chords
      // read it back to know which element holds the keyboard.
      els[id].id = id;
      var parent = PARENT[id];
      if (parent) window.__el(parent).appendChild(els[id]);
    }
    return els[id];
  };
  var docListeners = {};
  window.document = {
    getElementById: function (id) { return window.__el(id); },
    createElement: function (tag) { return new El(tag); },
    // execCommand is the fallback copy path; the stub records it so a test can
    // tell a refusal-and-fallback from a successful async write.
    execCommand: function (cmd) {
      window.__execCommands.push(cmd);
      return window.__execResult;
    },
    addEventListener: function (t, fn) {
      (docListeners[t] = docListeners[t] || []).push(fn);
    },
    // documentElement.style.setProperty is where app.js publishes the measured
    // cell metrics; a bare sink is enough to keep it from throwing.
    documentElement: {
      style: { setProperty: function () {}, getPropertyValue: function () { return ""; } }
    },
    // activeElement tracks focus(), which the paste path uses to hand the
    // clipboard to the browser's own paste (the hidden textarea) and to put the
    // keyboard back on the grid afterwards.
    activeElement: null,
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
      // __fireTimerOf runs the first live timer scheduled with this delay, so a
      // test can fire the paste window without depending on timer order.
      window.__fireTimerOf = function (ms) {
        for (var i = 0; i < timers.length; i++) {
          if (!timers[i].cancelled && timers[i].ms === ms) {
            var t = timers.splice(i, 1)[0];
            t.fn();
            return true;
          }
        }
        return false;
      };

      // Animation-frame stub. app.js coalesces follow-tail scrolling into one
      // frame, so the scroll happens when the test flushes the queue with
      // __fireFrame — which is what makes "one scroll per burst" assertable.
      var frames = [];
      window.requestAnimationFrame = function (fn) { frames.push(fn); return frames.length; };
      window.__fireFrame = function () {
        var fns = frames.splice(0, frames.length);
        for (var i = 0; i < fns.length; i++) fns[i](0);
        return fns.length;
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
      // getSelection is what decides whether Ctrl+C is a copy or the terminal's
      // interrupt, and the range it carries is what a cut deletes. The default is
      // "nothing selected".
      window.__selection = "";
      window.__range = null;
      window.getSelection = function () {
        return {
          toString: function () { return window.__selection; },
          rangeCount: window.__range ? 1 : 0,
          getRangeAt: function () { return window.__range; }
        };
      };
      window.__setSelection = function (s) { window.__selection = s; window.__range = null; };
      // __setSelectionRange installs a range whose client rects cover the cells
      // [x0, x1) of the cursor's row (a cell is 8px wide in this stub), which is
      // what the cut path measures the selection's columns from.
      window.__setSelectionRange = function (text, x0, x1) {
        window.__selection = text;
        window.__range = {
          toString: function () { return text; },
          getClientRects: function () { return [{ left: x0, right: x1, top: 0, bottom: 16 }]; }
        };
      };
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
    // textContent already concatenates the descendants, so recursion here would
    // count a nested row's text twice.
    return el.textContent || "";
  };
  // __activeElement is the stub's focused element id, or "" when nothing is
  // focused — the paste path's focus hand-off is asserted through it.
  window.__activeElement = function () { return (document.activeElement && document.activeElement.id) || ""; };
  // __count returns the number of child nodes.
  window.__count = function (el) { return el.children.length; };
  // __setScrollHeight gives an element a realistic scroll range: the stub's
  // default (1000px) is a fixed number, but the transcript tests need
  // scrollHeight to describe the rows that actually exist, or "is the view at
  // the bottom" cannot be asserted.
  window.__setScrollHeight = function (el, h) { el.scrollHeight = h; return h; };
  // __listenersOn reports how many handlers of a type are attached to an
  // element — used to pin WHICH element owns a listener (the scroll container,
  // not the transcript list inside it).
  window.__listenersOn = function (el, t) { return (el.listeners[t] || []).length; };
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

// TestClientJS_FollowTailScroll pins the follow-tail contract (spec §7.3):
// while the client is at the bottom every frame pulls the view to the newest
// output; as soon as the user scrolls up, live streaming must NOT move the
// view; returning to the bottom re-arms following.
func TestClientJS_FollowTailScroll(t *testing.T) {
	h := newClientHarness(t)
	// The scroll container is the ONE element that scrolls (transcript + live
	// grid); following the tail means scrolling IT, not the transcript list
	// inside it (which is not a scroll container at all).
	scroll := h.el(t, "screen")

	// Streaming while at the bottom: every frame pins the view to the newest
	// output. The scroll is coalesced into one animation frame, so it lands when
	// the browser paints rather than per message.
	h.deliver(t, frameDoc(t, 3, "first line"))
	h.fireFrame(t)
	if got := h.scrollTop(scroll); got != h.bottomOf(scroll) {
		t.Fatalf("follow-tail did not scroll to the bottom while following: scrollTop=%v", got)
	}
	h.deliver(t, frameDoc(t, 4, "second line"))
	h.fireFrame(t)
	if got := h.scrollTop(scroll); got != h.bottomOf(scroll) {
		t.Fatalf("follow-tail lost the tail on the next frame: scrollTop=%v", got)
	}

	// The user scrolls up to read earlier output: streaming must not yank them
	// back down.
	h.userScrollTo(t, scroll, 0)
	h.deliver(t, frameDoc(t, 5, "third line"))
	h.fireFrame(t)
	if got := h.scrollTop(scroll); got != 0 {
		t.Errorf("streaming moved the view while the user had scrolled up: scrollTop=%v", got)
	}

	// Scrolling back to the bottom re-arms follow-tail.
	h.userScrollTo(t, scroll, h.bottomOf(scroll))
	h.deliver(t, frameDoc(t, 6, "fourth line"))
	h.fireFrame(t)
	if got := h.scrollTop(scroll); got != h.bottomOf(scroll) {
		t.Errorf("follow-tail did not re-arm at the bottom: scrollTop=%v", got)
	}
}

// TestClientJS_FollowTailIsCoalescedPerFrame pins that a burst of frames costs
// one scroll, not one per message: the scrollHeight read behind it forces a
// layout, and the transcript layout is what the client pays for.
func TestClientJS_FollowTailIsCoalescedPerFrame(t *testing.T) {
	h := newClientHarness(t)
	scroll := h.el(t, "screen")

	for i := 0; i < 5; i++ {
		h.deliver(t, frameDoc(t, 10+i, "streaming line"))
	}
	// All five messages must share one queued frame.
	if got := h.fireFrame(t); got != 1 {
		t.Errorf("a burst of 5 frames queued %d animation frames, want 1", got)
	}
	if got := h.scrollTop(scroll); got != h.bottomOf(scroll) {
		t.Errorf("coalesced follow-tail did not reach the bottom: scrollTop=%v", got)
	}
}

// TestClientJS_ScrollbackRespectsFollowTail pins that the transcript list only
// yanks the view while the client is following.
func TestClientJS_ScrollbackRespectsFollowTail(t *testing.T) {
	h := newClientHarness(t)
	scroll := h.el(t, "screen")

	doc := func() string {
		b, err := NewFrameCodec().EncodeScrollback(1, TranscriptBatch{Rows: []RowPatch{{Row: 0, Runs: []Run{{Text: "scrolled off"}}}}})
		if err != nil {
			t.Fatalf("encode scrollback: %v", err)
		}
		return string(b)
	}

	h.deliver(t, doc())
	h.fireFrame(t)
	if got := h.scrollTop(scroll); got != h.bottomOf(scroll) {
		t.Errorf("scrollback while following did not keep the tail in view: scrollTop=%v", got)
	}
	if !strings.Contains(h.text(t, scroll), "scrolled off") {
		t.Error("scrollback row was not appended to the transcript list")
	}

	h.userScrollTo(t, scroll, 100)
	h.deliver(t, doc())
	h.fireFrame(t)
	if got := h.scrollTop(scroll); got != 100 {
		t.Errorf("scrollback moved the view while the user had scrolled up: scrollTop=%v", got)
	}
}

// TestClientJS_PaintsRowsAndCaret pins the render contract: a frame's runs
// land in the grid as text and the caret is placed at the cursor cell.
func TestClientJS_PaintsRowsAndCaret(t *testing.T) {
	h := newClientHarness(t)
	grid := h.el(t, "grid")
	rowsBox := h.el(t, "rows")
	caret := h.el(t, "caret")

	h.deliver(t, frameDoc(t, 2, "hello grid"))

	if n := h.count(t, rowsBox); n == 0 {
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

// reset drops every message sent so far, so a test can assert on one gesture
// (the paste that inserted, the keys a cut sent) without the earlier ones.
func (h *clientHarness) reset(t *testing.T) {
	t.Helper()
	h.vm.RunString("window.__socket.sent.length = 0")
}

// activeElement is the stub's focused element id, or "" when nothing is focused.
func (h *clientHarness) activeElement(t *testing.T) string {
	t.Helper()
	return h.call(t, "__activeElement").String()
}

// fireTimerOf runs the first live timer scheduled with this delay. The paste
// fallback is a timer, and firing it by delay keeps the test independent of
// which other timers the page has parked.
func (h *clientHarness) fireTimerOf(t *testing.T, ms int) bool {
	t.Helper()
	return h.call(t, "__fireTimerOf", ms).ToBoolean()
}

// sentInputs returns the payload of every {t:"input"} message the page sent.
func (h *clientHarness) sentInputs(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, m := range h.sent(t) {
		if m["t"] == "input" {
			data, _ := m["data"].(string)
			out = append(out, data)
		}
	}
	return out
}

// sentInput is the single input payload the page sent, or "" when it sent none.
func (h *clientHarness) sentInput(t *testing.T) string {
	t.Helper()
	all := h.sentInputs(t)
	if len(all) == 0 {
		return ""
	}
	return all[len(all)-1]
}

// countInputs is how many input messages the page sent.
func (h *clientHarness) countInputs(t *testing.T) int {
	t.Helper()
	return len(h.sentInputs(t))
}

// sentKeys renders the named keys the page sent, in order ("Backspace,Delete").
// A synthesized cut is exactly this list, so it is what the tests assert on.
func (h *clientHarness) sentKeys(t *testing.T) string {
	t.Helper()
	var names []string
	for _, m := range h.sent(t) {
		if m["t"] != "key" {
			continue
		}
		kk, _ := m["key"].(map[string]any)
		key, _ := kk["key"].(string)
		names = append(names, key)
	}
	return strings.Join(names, ",")
}

// sentKey reports whether a key event for key with the given modifier was sent.
func (h *clientHarness) sentKey(t *testing.T, key, modifier string) bool {
	t.Helper()
	for _, m := range h.sent(t) {
		if m["t"] != "key" {
			continue
		}
		kk, _ := m["key"].(map[string]any)
		if kk["key"] != key {
			continue
		}
		if flag, ok := kk[modifier].(bool); ok && flag {
			return true
		}
	}
	return false
}

// clipboardText is what the page's clipboard holds after a copy or cut.
func (h *clientHarness) clipboardText(t *testing.T) string {
	t.Helper()
	return h.call(t, "__clipboardText").String()
}

// clipboardReads counts the page's clipboard reads; a copy must not read.
func (h *clientHarness) clipboardReads(t *testing.T) int {
	t.Helper()
	return int(h.call(t, "__clipboardReads").ToInteger())
}

// execCommands lists the legacy execCommand calls the page made.
func (h *clientHarness) execCommands(t *testing.T) []string {
	t.Helper()
	v := h.vm.Get("__execCommands")
	var out []string
	if err := h.vm.ExportTo(v, &out); err != nil {
		t.Fatalf("export execCommands: %v", err)
	}
	return out
}

// keydown dispatches a synthetic keydown and reports whether the page claimed
// it (preventDefault).
func (h *clientHarness) keydown(t *testing.T, ev map[string]any) bool {
	t.Helper()
	return h.call(t, "__fire", "keydown", h.toValue(ev)).ToBoolean()
}

// keyup dispatches the keyup that closes a paste chord's window — the point the
// page reads the clipboard from when the browser delivered no paste.
func (h *clientHarness) keyup(t *testing.T, ev map[string]any) {
	t.Helper()
	h.call(t, "__fire", "keyup", h.toValue(ev))
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
// endpoints with different payloads. The geometry message is debounced (spec
// §14.6) so a drag-resize burst becomes one repaint, not one per pixel.
func TestClientJS_SSEResizeGoesToResizeEndpoint(t *testing.T) {
	h := newClientHarness(t)
	h.failHandshake(t)
	h.stubPosts(t)

	if got := int(h.call(t, "__fireWindow", "resize").ToInteger()); got == 0 {
		t.Fatal("page registered no resize listener")
	}
	if posts := h.posts(t); len(posts) != 0 {
		t.Fatalf("resize was not debounced; posted immediately: %v", posts)
	}
	if !h.call(t, "__fireTimer").ToBoolean() {
		t.Fatal("debounced resize never scheduled a geometry message")
	}
	posts := h.posts(t)
	if len(posts) == 0 || posts[0]["url"] != "/resize" {
		t.Fatalf("resize produced %v, want a POST to /resize", posts)
	}
}

// TestClientJS_ScrollListenerIsOnTheScrollContainer pins WHICH element owns the
// follow-tail listener: the scroll container, not the transcript list inside it.
// Listening on a non-scrollable child is exactly how "scrolling does nothing"
// shipped once already.
func TestClientJS_ScrollListenerIsOnTheScrollContainer(t *testing.T) {
	h := newClientHarness(t)
	screen := h.el(t, "screen")
	sb := h.el(t, "scrollback")
	if n := int(h.call(t, "__listenersOn", screen, "scroll").ToInteger()); n != 1 {
		t.Errorf("scroll container has %d scroll listeners, want exactly 1", n)
	}
	if n := int(h.call(t, "__listenersOn", sb, "scroll").ToInteger()); n != 0 {
		t.Errorf("transcript list has %d scroll listeners; the listener belongs on the container", n)
	}
}

// TestClientJS_ShrinkTrimsGridRows pins that a shrinking screen removes the rows
// that no longer exist. Keeping them left stale rows below the new bottom,
// inflating the scroll height and pushing the input box (and the caret) off
// screen — the visible symptom of "resizing does not work well".
func TestClientJS_ShrinkTrimsGridRows(t *testing.T) {
	h := newClientHarness(t)
	rowsBox := h.el(t, "rows")

	h.deliver(t, frameDocRows(t, 30, "top"))
	if n := h.count(t, rowsBox); n != 30 {
		t.Fatalf("rows after a 30-row frame = %d, want 30", n)
	}
	h.deliver(t, frameDocRows(t, 12, "top"))
	if n := h.count(t, rowsBox); n != 12 {
		t.Fatalf("rows after shrinking to 12 = %d, want 12 (stale rows were kept)", n)
	}
}

// The clipboard chords are the PAGE's: a non-editable grid gets neither a `copy`
// nor a `paste` event from the browser, so the page performs them itself —
// measured in Chrome with trusted chords and a real clipboard (bugs.md B5). These
// tests pin one chord each: copy, the interrupt, paste, cut.
func TestClientJS_CopyChordCopiesTheSelection(t *testing.T) {
	h := newClientHarness(t)
	h.open(t)

	// Ctrl+C with a selection: claimed, copied, and nothing sent to the terminal.
	h.call(t, "__setSelection", "copied text")
	if !h.keydown(t, map[string]any{"key": "c", "code": "KeyC", "ctrlKey": true}) {
		t.Error("page let Ctrl+C through with a live selection; the copy would be lost")
	}
	if got := h.clipboardText(t); got != "copied text" {
		t.Errorf("clipboard = %q after Ctrl+C, want the selection", got)
	}
	if got := h.clipboardReads(t); got != 0 {
		t.Errorf("Ctrl+C read the clipboard %d times", got)
	}

	// Cmd+C with a selection is the same chord on macOS.
	if !h.keydown(t, map[string]any{"key": "c", "code": "KeyC", "metaKey": true}) {
		t.Error("page let Cmd+C through with a live selection; the copy would be lost")
	}
}

// TestClientJS_CtrlCWithoutSelectionIsTheInterrupt pins the other half of the
// copy chord: with nothing selected the page must send it to the engine (0x03),
// or a running turn can no longer be interrupted.
func TestClientJS_CtrlCWithoutSelectionIsTheInterrupt(t *testing.T) {
	h := newClientHarness(t)
	h.open(t)

	h.call(t, "__setSelection", "")
	if !h.keydown(t, map[string]any{"key": "c", "code": "KeyC", "ctrlKey": true}) {
		t.Error("page let Ctrl+C through with no selection; the terminal lost its interrupt")
	}
	if !h.sentKey(t, "c", "ctrl") {
		t.Error("Ctrl+C with no selection did not reach the engine as a key event")
	}
	if got := h.clipboardText(t); got != "" {
		t.Errorf("Ctrl+C with no selection wrote %q to the clipboard", got)
	}
}

// TestClientJS_PasteChordHandsOffToTheBrowser pins the paste contract: the page
// focuses its hidden paste target and does NOT preventDefault, so the browser's
// own paste can deliver the clipboard (the only read path that needs no
// permission). The chord's keyup closes the window; when no paste event arrived —
// what a script-dispatched chord does — the page reads the clipboard itself.
func TestClientJS_PasteChordHandsOffToTheBrowser(t *testing.T) {
	h := newClientHarness(t)
	h.open(t)

	h.call(t, "__setClipboard", "pasted text")
	if h.keydown(t, map[string]any{"key": "v", "code": "KeyV", "ctrlKey": true}) {
		t.Error("page claimed Ctrl+V; the browser's own paste would be suppressed")
	}
	if got := h.activeElement(t); got != "paste-target" {
		t.Errorf("focus after Ctrl+V = %q, want the paste target the browser pastes into", got)
	}
	h.keyup(t, map[string]any{"key": "v", "code": "KeyV", "ctrlKey": true})
	if got := h.sentInput(t); got != "pasted text" {
		t.Errorf("paste sent %q as input, want the clipboard text", got)
	}
	if n := h.countInputs(t); n != 1 {
		t.Errorf("paste inserted %d times; want exactly once", n)
	}
	if got := h.activeElement(t); got != "grid" {
		t.Errorf("focus after the paste = %q, want the grid back", got)
	}
}

// TestClientJS_CutChordCopiesAndDeletes pins the cut: the selection goes to the
// clipboard and is removed from the input line with the engine's own keys. With
// the selection ending at the cursor that is one Backspace per character.
func TestClientJS_CutChordCopiesAndDeletes(t *testing.T) {
	h := newClientHarness(t)
	h.open(t)

	h.deliver(t, frameDocCursor(t, 6, 2, 6, "abcdef"))
	h.call(t, "__setSelectionRange", "def", 3*8, 6*8)
	if !h.keydown(t, map[string]any{"key": "x", "code": "KeyX", "ctrlKey": true}) {
		t.Error("page let Ctrl+X through with a live selection; the cut would be lost")
	}
	if got := h.clipboardText(t); got != "def" {
		t.Errorf("clipboard = %q after Ctrl+X, want the selection", got)
	}
	if got := h.sentKeys(t); got != "Backspace,Backspace,Backspace" {
		t.Errorf("cut sent %q, want three Backspaces for three characters", got)
	}
}

// TestClientJS_NativePasteStopsTheFallback pins the double-insert guard: when
// the browser's own paste arrives (the primary path — no permission involved),
// its text is inserted once and the clipboard fallback must not insert it again.
func TestClientJS_NativePasteStopsTheFallback(t *testing.T) {
	h := newClientHarness(t)
	h.open(t)

	h.call(t, "__setClipboard", "clipboard text")
	h.keydown(t, map[string]any{"key": "v", "code": "KeyV", "ctrlKey": true})
	if got := h.activeElement(t); got != "paste-target" {
		t.Fatalf("focus after Ctrl+V = %q, want the paste target", got)
	}
	if !h.paste(t, "clipboard text") {
		t.Error("the browser's paste was not prevented; the text would land in the textarea")
	}
	if got := h.sentInput(t); got != "clipboard text" {
		t.Errorf("native paste inserted %q, want the clipboard text", got)
	}
	if got := h.activeElement(t); got != "grid" {
		t.Errorf("focus after the native paste = %q, want the grid back", got)
	}
	// The keyup closes the paste window with the browser's paste already in: it
	// must stay silent, or the clipboard would be inserted twice.
	h.keyup(t, map[string]any{"key": "v", "code": "KeyV", "ctrlKey": true})
	if n := h.countInputs(t); n != 1 {
		t.Errorf("paste inserted %d times; the fallback duplicated the browser's paste", n)
	}
}

// TestClientJS_PasteWindowBackstopReadsWithoutAKeyup pins the backstop: a chord
// delivered without its keyup still gets its clipboard read, so a remote-input or
// synthetic chord cannot stall the paste.
func TestClientJS_PasteWindowBackstopReadsWithoutAKeyup(t *testing.T) {
	h := newClientHarness(t)
	h.open(t)

	h.call(t, "__setClipboard", "backstop text")
	h.keydown(t, map[string]any{"key": "v", "code": "KeyV", "ctrlKey": true})
	if !h.fireTimerOf(t, 500) {
		t.Fatal("paste scheduled no backstop timer")
	}
	if got := h.sentInput(t); got != "backstop text" {
		t.Errorf("backstop paste sent %q, want the clipboard text", got)
	}
	if got := h.activeElement(t); got != "grid" {
		t.Errorf("focus after the backstop paste = %q, want the grid back", got)
	}
}

// TestClientJS_PasteStaysWithTheBrowserWithoutAClipboardAPI pins the fallback:
// on an origin with no async clipboard (plain http on a LAN address) the page
// cannot read the clipboard itself, so it must LEAVE the chord to the browser —
// claim it and a browser that does fire a `paste` event would be swallowed.
func TestClientJS_PasteStaysWithTheBrowserWithoutAClipboardAPI(t *testing.T) {
	h := newClientHarness(t)
	h.open(t)
	h.call(t, "__dropClipboardAPI")
	if h.keydown(t, map[string]any{"key": "v", "code": "KeyV", "ctrlKey": true}) {
		t.Error("page claimed Ctrl+V; the browser's own paste would be suppressed")
	}
	// The paste window is always opened (the browser may still deliver a paste);
	// with no clipboard API the fallback can only give up and put the focus back.
	h.keyup(t, map[string]any{"key": "v", "code": "KeyV", "ctrlKey": true})
	if n := h.countInputs(t); n != 0 {
		t.Errorf("a paste the page could not read still sent %d input messages", n)
	}
	if got := h.activeElement(t); got != "grid" {
		t.Errorf("focus after an unreadable paste = %q, want the grid back", got)
	}
}

// TestClientJS_CopyFallsBackToExecCommand pins the legacy write path: an
// insecure origin has no async clipboard, and a refused write must not lose the
// copy while execCommand can still put the selection on the real clipboard.
func TestClientJS_CopyFallsBackToExecCommand(t *testing.T) {
	h := newClientHarness(t)
	h.open(t)

	// No clipboard API at all: the legacy path is the only one.
	h.call(t, "__dropClipboardAPI")
	h.call(t, "__setSelection", "legacy copy")
	h.keydown(t, map[string]any{"key": "c", "code": "KeyC", "ctrlKey": true})
	if cmds := h.execCommands(t); len(cmds) != 1 || cmds[0] != "copy" {
		t.Errorf("execCommand calls = %v, want one copy", cmds)
	}

	// Async write refused: the page must fall back rather than give up.
	h2 := newClientHarness(t)
	h2.open(t)
	h2.call(t, "__denyClipboardWrites", true)
	h2.call(t, "__setSelection", "fallback")
	h2.keydown(t, map[string]any{"key": "c", "code": "KeyC", "ctrlKey": true})
	if cmds := h2.execCommands(t); len(cmds) != 1 || cmds[0] != "copy" {
		t.Errorf("execCommand calls after a refused write = %v, want one copy", cmds)
	}
}

// TestClientJS_ReadOnlyViewerStillCopiesButNeverDrives pins the viewer's half of
// the contract: reading the screen is not driving it, so a copy works; paste and
// cut never reach the session.
func TestClientJS_ReadOnlyViewerStillCopiesButNeverDrives(t *testing.T) {
	h := newClientHarness(t)
	h.open(t)
	h.deliver(t, frameDocCursor(t, 6, 2, 6, "abcdef"))
	h.call(t, "__deliver", h.vm.ToValue(map[string]any{"t": "read_only", "text": "viewer"}))
	h.call(t, "__setSelection", "shared line")

	h.keydown(t, map[string]any{"key": "c", "code": "KeyC", "ctrlKey": true})
	if got := h.clipboardText(t); got != "shared line" {
		t.Errorf("read-only viewer copied %q, want the selection", got)
	}

	h.reset(t)
	h.call(t, "__setClipboard", "drives the session")
	h.keydown(t, map[string]any{"key": "v", "code": "KeyV", "ctrlKey": true})
	h.call(t, "__setSelectionRange", "def", 3*8, 6*8)
	h.keydown(t, map[string]any{"key": "x", "code": "KeyX", "ctrlKey": true})
	if n := h.countInputs(t); n != 0 {
		t.Errorf("a read-only viewer sent %d input messages", n)
	}
	if got := h.sentKeys(t); got != "" {
		t.Errorf("a read-only viewer sent keys to the session: %q", got)
	}
}

// TestClientJS_CutWalksTheCursorToTheSelection pins the general cut: when the
// selection is neither at the cursor nor reaching it, the cursor is walked to the
// selection's end (column arithmetic over the row's pixels) and the selected
// characters are deleted behind it.
func TestClientJS_CutWalksTheCursorToTheSelection(t *testing.T) {
	h := newClientHarness(t)
	h.open(t)

	// Cursor at column 1, selection covers columns 3..5 of a single-width row, so
	// the cursor walks to the selection's end (column 5, four steps right) before
	// the two selected characters are deleted behind it.
	h.deliver(t, frameDocCursor(t, 6, 2, 1, "abcdef"))
	h.call(t, "__setSelectionRange", "de", 3*8, 5*8)
	if !h.keydown(t, map[string]any{"key": "x", "code": "KeyX", "ctrlKey": true}) {
		t.Fatal("page let Ctrl+X through with a live selection")
	}
	if got := h.clipboardText(t); got != "de" {
		t.Errorf("clipboard = %q after the cut, want the selection", got)
	}
	if got := h.sentKeys(t); got != "ArrowRight,ArrowRight,ArrowRight,ArrowRight,Backspace,Backspace" {
		t.Errorf("cut sent %q, want four rights (cursor col 1 → selection end col 5) then two backspaces", got)
	}
}

// TestClientJS_CutLeavesOutputAlone pins that a selection reaching outside the
// cursor's row (the transcript above the input line) is copied, never deleted:
// output is not the editor's buffer.
func TestClientJS_CutLeavesOutputAlone(t *testing.T) {
	h := newClientHarness(t)
	h.open(t)

	h.deliver(t, frameDocCursor(t, 6, 2, 1, "abcdef"))
	// Rect outside the cursor row's box (top 0..16): the transcript.
	h.call(t, "__setSelectionRange", "output line", 0, 8)
	h.vm.RunString("window.__range.getClientRects = function () { return [{ left: 0, right: 80, top: -32, bottom: -16 }]; }")
	if !h.keydown(t, map[string]any{"key": "x", "code": "KeyX", "ctrlKey": true}) {
		t.Fatal("page let Ctrl+X through with a live selection")
	}
	if got := h.clipboardText(t); got != "output line" {
		t.Errorf("clipboard = %q after the cut, want the selection", got)
	}
	if got := h.sentKeys(t); got != "" {
		t.Errorf("cut deleted %q from the session although the selection is output", got)
	}
}

// TestClientJS_CutSkipsTheWalkOnAWideGlyphLine pins the width guard: a line
// holding a two-cell glyph makes cell columns and buffer characters disagree, so
// a cut that does not touch the cursor copies and leaves the line alone instead
// of deleting the wrong characters.
func TestClientJS_CutSkipsTheWalkOnAWideGlyphLine(t *testing.T) {
	h := newClientHarness(t)
	h.open(t)

	h.deliver(t, frameDocCursor(t, 6, 2, 1, "\u4f60\u597dabcdef"))
	h.call(t, "__setSelectionRange", "de", 3*8, 5*8)
	if !h.keydown(t, map[string]any{"key": "x", "code": "KeyX", "ctrlKey": true}) {
		t.Fatal("page let Ctrl+X through with a live selection")
	}
	if got := h.clipboardText(t); got != "de" {
		t.Errorf("clipboard = %q after the cut, want the selection", got)
	}
	if got := h.sentKeys(t); got != "" {
		t.Errorf("cut walked a wide-glyph line and sent %q", got)
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
