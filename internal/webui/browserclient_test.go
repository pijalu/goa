// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import (
	"strings"
	"testing"
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
