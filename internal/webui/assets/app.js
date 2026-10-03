// goa web UI client — the browser is the terminal.
//
// Receives row patches over a WebSocket, paints each row as a div of styled
// spans, keeps the rows that scrolled off in a transcript list, and turns
// keydown/paste into the raw bytes a real terminal would send (spec §7.5).
// Plain ES2018, no dependencies, no build step.

(function () {
  "use strict";

  var SESSION = window.GOA_SESSION || "";
  var grid = document.getElementById("grid");
  var scrollEl = document.getElementById("scrollback");
  var caret = document.getElementById("caret");
  var status = document.getElementById("status");

  var rows = [];      // per-row run cache — skip untouched rows
  var cols = 0;
  var lineHeight = 16;
  var charWidth = 8;
  var socket = null;
    var reconnectDelay = 500;

  // following is the follow-tail flag: while it is true the client keeps the
  // newest output in view, and the moment the user scrolls up it goes false so
  // live streaming cannot yank the view back down under their cursor. Scrolling
  // back to the bottom re-arms it (spec §7.3).
  var following = true;

  // ---------------------------------------------------------------- rendering

  // FOLLOW_SLACK_PX is how close to the bottom still counts as "at the bottom":
  // one line of slack keeps follow-tail from flapping on sub-pixel rounding.
  var FOLLOW_SLACK_PX = 24;

  function atBottom() {
    return scrollEl.scrollHeight - (scrollEl.scrollTop + scrollEl.clientHeight) <= FOLLOW_SLACK_PX;
  }

  function scrollToBottom() {
    scrollEl.scrollTop = scrollEl.scrollHeight;
  }

  function measure() {
    var probe = document.createElement("span");
    probe.textContent = "M";
    probe.style.visibility = "hidden";
    probe.style.position = "absolute";
    grid.appendChild(probe);
    var r = probe.getBoundingClientRect();
    if (r.width > 0) charWidth = r.width;
    if (r.height > 0) lineHeight = r.height;
    grid.removeChild(probe);
  }

  // flags maps the server's attribute bitmask (spec §20) to CSS classes.
  function flags(classes, f) {
    if (!f) return;
    if (f & 1) classes.push("b");
    if (f & 2) classes.push("i");
    if (f & 4) classes.push("u");
    if (f & 8) classes.push("d");
    if (f & 16) classes.push("inv");
    if (f & 32) classes.push("s");
  }

  // external reports whether a URI may become a clickable anchor. Only
  // http(s)/mailto qualify: file:, javascript: and data: stay plain text, so
  // terminal output can never turn into script execution in the page.
  function external(uri) {
    return /^(https?:|mailto:)/i.test(uri);
  }

  // linkElement builds an <a> for an OSC-8 run. The href is set through the
  // property API (never innerHTML), and rel="noopener noreferrer" keeps the
  // opened page from reaching back through window.opener.
  function linkElement(uri) {
    var a = document.createElement("a");
    a.href = uri;
    a.rel = "noopener noreferrer";
    a.target = "_blank";
    // Clicking a link must not double as a keystroke in the terminal.
    a.addEventListener("click", function (ev) { ev.stopPropagation(); });
    return a;
  }

  // runElement escapes text, then applies styling. Text can never become
  // markup: everything goes through textContent.
  function runElement(run) {
    var text = run[0] || "";
    var f = run[1] | 0;
    var fg = run[2] || "";
    var bg = run[3] || "";
    var link = run[4] || "";
    var el = link && external(link)
      ? linkElement(link)
      : document.createElement("span");
    el.textContent = text;
    if (f || fg || bg) {
      var classes = [];
      flags(classes, f);
      if (classes.length) el.className = classes.join(" ");
      if (fg || bg) {
        el.style.color = fg || "";
        el.style.backgroundColor = bg || "";
      }
    }
    return el;
  }

  function buildRow(runs) {
    var div = document.createElement("div");
    div.className = "row";
    var list = runs || [];
    for (var i = 0; i < list.length; i++) div.appendChild(runElement(list[i]));
    return div;
  }

  function applyRow(patch) {
    var idx = patch.row;
    while (rows.length <= idx) rows.push(null);
    var div = grid.children[idx];
    if (!div) div = grid.appendChild(buildRow(null));
    div.textContent = "";
    var runs = patch.runs || [];
    for (var i = 0; i < runs.length; i++) div.appendChild(runElement(runs[i]));
    rows[idx] = patch.runs;
  }

  // onScrollback appends the rows that scrolled off the live grid to the
  // transcript list above it. Each row is shipped exactly once, so a plain
  // append is all that is needed.
  function onScrollback(msg) {
    if (!scrollEl) return;
    var list = msg.sb || [];
    for (var i = 0; i < list.length; i++) {
      scrollEl.appendChild(buildRow(list[i].runs));
    }
    if (following) scrollToBottom();
  }

  function resizeRows(n) {
    while (rows.length < n) {
      rows.push(null);
      grid.appendChild(document.createElement("div")).className = "row";
    }
  }

  function placeCaret(cur) {
    if (!cur || !cur.v) {
      caret.hidden = true;
      return;
    }
    caret.hidden = false;
    var pad = 8;
    caret.style.left = (pad + cur.c * charWidth) + "px";
    caret.style.top = (pad + cur.r * lineHeight) + "px";
  }

  function onFrame(msg) {
    cols = msg.cols || cols;
    resizeRows(msg.rows || 0);
    (msg.patches || []).forEach(applyRow);
    placeCaret(msg.cur);
    if (msg.title) document.title = msg.title;
    // Follow-tail: only auto-scroll while the user has not scrolled away.
    if (following) scrollToBottom();
  }

  // ---------------------------------------------------------------- transport

  function setStatus(state, text) {
        status.dataset.state = state;
        status.textContent = text;
      }

      // mode is the active transport: "ws" (primary) or "sse" (fallback). Once the
      // WebSocket handshake fails we switch to SSE for the rest of the page load —
      // a network path that blocks the upgrade will keep blocking it, so retrying
      // WS forever would strand the user on a dead screen (spec §11.2).
      var mode = "ws";
              // lastSeq is the newest frame seq painted. It rides the reconnect
              // handshake (a WS hello, or ?since= on the event stream) so the server can
              // tell "you already have the current screen" from "you fell behind" — the
              // grid is authoritative, so a resume is answered with the whole screen or
              // with silence, never a delta replay (spec §8).
              var lastSeq = 0;
              // wsOpened distinguishes "connected then dropped" (retry WS) from "never
              // connected" (fall back to SSE). Only the latter changes the mode.
              var wsOpened = false;
              // stream is the live EventSource, and streamDelay the backoff for
              // reopening one. Both exist only in the fallback mode.
              var stream = null;
              var streamDelay = 500;

              function wsURL() {
                var proto = location.protocol === "https:" ? "wss:" : "ws:";
                return proto + "//" + location.host + "/ws?s=" + encodeURIComponent(SESSION);
              }

              function eventsURL(since) {
                var url = "/events?s=" + encodeURIComponent(SESSION);
                // A resume carries the revision the page holds; a first connection has
                // nothing to resume from and says so by omitting it.
                if (since) url += "&since=" + encodeURIComponent(since);
                return url;
              }

      function connect() {
        mode = "ws";
        var socket2 = new WebSocket(wsURL());
        socket = socket2;

        // A handshake that never completes must not hang the page forever. When
        // the timer fires the socket is torn down and the SSE fallback engages.
        var handshake = setTimeout(function () {
          if (wsOpened) return;
          try { socket2.close(); } catch (e) { /* already closing */ }
        }, WS_HANDSHAKE_MS);

        socket.onopen = function () {
          wsOpened = true;
          clearTimeout(handshake);
          reconnectDelay = 500;
          setStatus("live", "connected");
          // Tell the server the last frame we saw, then push our geometry.
          send({ t: "hello", since: lastSeq });
          send({ t: "resize", cols: measureCols(), rows: measureRows() });
        };
        socket.onmessage = function (ev) { handleWire(ev.data); };
        socket.onclose = function () {
          clearTimeout(handshake);
          if (!wsOpened) {
            // Handshake failed (blocked, proxied, refused): fall back for good.
            connectSSE();
            return;
          }
          // A live socket dropped: retry WS with backoff.
          wsOpened = false;
          setStatus("closed", "disconnected — retrying…");
          setTimeout(connect, reconnectDelay);
          reconnectDelay = Math.min(reconnectDelay * 2, 10000);
        };
        socket.onerror = function () {
          clearTimeout(handshake);
          if (!wsOpened) setStatus("connecting", "websocket unavailable…");
        };
      }

      // connectSSE switches to the fallback transport: a one-way event stream for
              // frames and plain POSTs for input (spec §11.2). Degradation is invisible
              // beyond a slightly higher input latency and a discreet badge.
              function connectSSE() {
                mode = "sse";
                wsOpened = false;
                setStatus("degraded", "realtime degraded — using SSE");
                openStream();
              }

              // openStream opens the event stream at the revision the page holds, after
              // the current backoff. It is both the first connection and every resume:
              // one code path, so the two cannot drift apart.
              function openStream() {
                var es = new EventSource(eventsURL(lastSeq));
                stream = es;
                es.onopen = function () {
                  if (stream !== es) return;
                  streamDelay = 500;
                  setStatus("degraded", "realtime degraded — connected");
                };
                es.onmessage = function (ev) {
                  if (stream === es) handleWire(ev.data);
                };
                es.onerror = function () {
                  if (stream !== es) return;
                  // EventSource would reconnect on its own, replaying a URL with no
                  // resume hint. Reopening it here is what carries ?since=, so the
                  // server can answer with nothing when the screen is already current.
                  es.close();
                  stream = null;
                  setStatus("degraded", "realtime degraded — reconnecting…");
                  var wait = streamDelay;
                  streamDelay = Math.min(streamDelay * 2, 10000);
                  setTimeout(openStream, wait);
                };
              }

              // requestResync asks the server for the authoritative screen after the
              // page noticed it lost a frame. Over the socket that is a hello; over the
              // fallback there is no socket to send one on, so the stream is reopened at
              // the last revision the page actually holds.
              function requestResync() {
                if (mode === "sse") {
                  if (stream) {
                    stream.close();
                    stream = null;
                  }
                  setStatus("degraded", "realtime degraded — resyncing…");
                  var wait = streamDelay;
                  streamDelay = Math.min(streamDelay * 2, 10000);
                  setTimeout(openStream, wait);
                  return;
                }
                send({ t: "hello", since: lastSeq });
              }

              // acceptFrame decides whether a frame may be painted. A delta is only
              // meaningful on top of every frame before it, so a seq that skips one
              // means output was lost — and both transports drop the oldest queued
              // frame for a client that cannot keep up. Painting such a delta would
              // show a screen that never existed, so the page asks for the whole grid
              // instead (spec §8). Full frames are self-contained and always applied.
              function acceptFrame(msg) {
                if (typeof msg.seq !== "number") return true; // unnumbered server: nothing to check
                if (msg.seq <= lastSeq) return false;        // already painted
                if (!msg.full && lastSeq !== 0 && msg.seq > lastSeq + 1) {
                  requestResync();
                  return false;
                }
                lastSeq = msg.seq;
                return true;
              }

      // handleWire parses one wire document from either transport and dispatches
      // it. Both transports speak the same FrameCodec format, so the rendering
      // path above is shared and there is no second code path (spec §11.2).
      function handleWire(raw) {
        var msg;
        try {
          msg = JSON.parse(raw);
        } catch (e) {
          return;
        }
        if (msg.t === "frame") {
                    if (!acceptFrame(msg)) return;
                    onFrame(msg);
                  }
        else if (msg.t === "scrollback") onScrollback(msg);
        else if (msg.t === "session_rotated") {
                  // The conversation rotated (`/new`, `/clear`): this page's URL is
                  // the old session, which no longer resolves. Following the new id
                  // is the difference between a working tab and one that retries a
                  // dead URL forever (spec §8).
                  if (msg.session && msg.session !== SESSION) {
                    window.location.replace("/s/" + encodeURIComponent(msg.session));
                  }
                }
        else if (msg.t === "read_only") {
          readOnly = true;
          setStatus(mode === "sse" ? "degraded" : "live",
            msg.text ? "read-only — " + msg.text : "read-only viewer");
        }
        else if (msg.t === "bye") setStatus("closed", "closed: " + (msg.text || ""));
        else if (msg.t === "error") setStatus("error", msg.text || "error");
      }

      // WS_HANDSHAKE_MS bounds the WebSocket handshake before we give up and fall
      // back to SSE (spec §11.2 detection: 3 s).
      var WS_HANDSHAKE_MS = 3000;

      function send(obj) {
        if (mode === "sse") {
          sendViaPost(obj);
          return;
        }
        if (socket && socket.readyState === WebSocket.OPEN) {
          socket.send(JSON.stringify(obj));
        }
      }

      // sendViaPost delivers one message over HTTP when the fallback transport is
          // active. Each message type has its own endpoint so the server keeps doing
          // the work it does over the socket: geometry to /resize, a key *descriptor*
          // to /key (encoded server-side by the same KeyEncoder), raw bytes to /input.
          // Best-effort — a failure surfaces in the status line rather than being
          // swallowed.
          function sendViaPost(obj) {
            var url = "/input";
            var payload = { data: obj.data || "" };
            if (obj.t === "resize") {
              url = "/resize";
              payload = { cols: obj.cols, rows: obj.rows };
            } else if (obj.t === "key") {
              url = "/key";
              payload = { key: obj.key };
            }
            fetch(url, {
              method: "POST",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify(payload)
            }).catch(function () {
              setStatus("error", "input failed (sse fallback)");
            });
          }

  function measureCols() {
    return Math.max(20, Math.floor((grid.clientWidth - 16) / charWidth));
  }

  function measureRows() {
    return Math.max(6, Math.floor((grid.clientHeight - 16) / lineHeight));
  }

  // ------------------------------------------------------------------- input

  // The page never encodes a keystroke: it reports what was pressed and the
  // server (KeyEncoder) decides the terminal bytes, so the byte table lives in
  // one tested place (spec §7.5). Encoding here would duplicate it and let the
  // two drift apart.

  // readOnly mirrors the server mode (--read-only, or an over-capacity hub).
  // While set, key presses are ignored so a viewer never drives a session
  // someone else owns.
  var readOnly = false;

  function keyEvent(ev) {
    return {
      key: ev.key,
      code: ev.code || "",
      ctrl: !!ev.ctrlKey,
      alt: !!ev.altKey,
      shift: !!ev.shiftKey,
      meta: !!ev.metaKey,
      repeat: !!ev.repeat
    };
  }

  document.addEventListener("keydown", function (ev) {
    if (readOnly) return;
    // Chords the browser owns stay the browser's: reload, devtools and tab
    // switching are how a user escapes a page, and a terminal that swallows
    // them is a trap. Everything else — including the engine's own Ctrl+W /
    // Ctrl+L bindings — is claimed, so the terminal behaves like a terminal.
    if (browserOwned(ev)) return;
    // Modifier-only presses and Meta chords are sent as-is; the server's
    // encoder declines to encode them, and the browser keeps its default.
    send({ t: "key", key: keyEvent(ev) });
    ev.preventDefault();
  });

  // BROWSER_OWNED is the set of chords the page never claims: the function
  // keys with no engine binding, plus the browser's own navigation and
  // devtools chords.
  var BROWSER_OWNED_FKEYS = { F5: 1, F11: 1, F12: 1 };
  var BROWSER_OWNED_CHORDS = { r: 1, q: 1 };
  var BROWSER_OWNED_DEVTOOLS = { i: 1, j: 1, c: 1 };

  // browserOwned reports whether a keydown belongs to the browser.
  function browserOwned(ev) {
    if (BROWSER_OWNED_FKEYS[ev.key]) return true;
    if (!ev.ctrlKey && !ev.metaKey) return false;
    var k = (ev.key || "").toLowerCase();
    if (ev.shiftKey) return !!BROWSER_OWNED_DEVTOOLS[k];
    return !!BROWSER_OWNED_CHORDS[k];
  }

  // uploadImage posts pasted image bytes and resolves with the stored path.
  function uploadImage(blob) {
    return new Promise(function (resolve, reject) {
      var form = new FormData();
      form.append("file", blob, "pasted-image");
      fetch("/upload", { method: "POST", body: form })
        .then(function (res) {
          if (!res.ok) {
            return res.text().then(function (t) { reject(new Error(t || res.status)); });
          }
          return res.json();
        })
        .then(function (data) { resolve(data.path || ""); })
        .catch(reject);
    });
  }

  // imageItem finds an image in the clipboard payload, if any.
  function imageItem(data) {
    if (!data.items) return null;
    for (var i = 0; i < data.items.length; i++) {
      var it = data.items[i];
      if (it.kind === "file" && it.type && it.type.indexOf("image/") === 0) {
        return it.getAsFile();
      }
    }
    return null;
  }

  // onPaste inserts clipboard text as-is (the editor applies its own paste
  // handling — markers, normalization — exactly as for a terminal paste). An
  // image has no textual form, so it is uploaded and its stored path inserted:
  // the same text the terminal editor inserts for a clipboard image.
  document.addEventListener("paste", function (ev) {
    if (readOnly) return;
    var data = ev.clipboardData || window.clipboardData;
    if (!data) return;
    var image = imageItem(data);
    if (image) {
      ev.preventDefault();
      uploadImage(image).then(function (path) {
        if (path) send({ t: "input", data: path });
      }).catch(function () { setStatus("error", "image upload failed"); });
      return;
    }
    var text = data.getData("text");
    if (!text) return;
    ev.preventDefault();
    send({ t: "input", data: text });
  });

  window.addEventListener("resize", function () {
    // The cell size is measured here, not per frame: a measurement forces a
    // synchronous layout, and frames arrive at the engine's tick rate.
    measure();
    send({ t: "resize", cols: measureCols(), rows: measureRows() });
  });

  // Follow-tail state follows the user's own scroll: scrolling up detaches,
  // returning to the bottom re-attaches. The listener is passive so it never
  // blocks the browser's scrolling.
  if (scrollEl) {
    scrollEl.addEventListener("scroll", function () {
      following = atBottom();
    }, { passive: true });
  }

  // Typing must reach the terminal even when nothing is focused.
  document.addEventListener("click", function () { grid.focus(); });
  grid.setAttribute("tabindex", "0");

  measure();
  connect();
})();