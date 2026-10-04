// goa web UI client — the browser is the terminal.
//
// Receives row patches over a WebSocket, paints each row as a div of styled
// spans, keeps the rows that scrolled off in a transcript list, and turns
// keydown/paste into the raw bytes a real terminal would send (spec §7.5).
// Plain ES2018, no dependencies, no build step.

(function () {
  "use strict";

  var SESSION = window.GOA_SESSION || "";
  // screenEl is the ONE scroll container (transcript + live grid); the grid and
  // the caret live inside it, so a scroll moves the caret with the content it
  // marks. scrollEl is the transcript list itself.
  var screenEl = document.getElementById("screen");
  var scrollEl = document.getElementById("scrollback");
  var gridEl = document.getElementById("grid");
  var rowsEl = document.getElementById("rows");
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

  // ---------------------------------------------------------------- transcript

  // The transcript (the rows that scrolled off the live grid) is the one part of
  // the page that grows with the session, so it is BOUNDED — exactly like a
  // terminal's scrollback buffer, which also discards the oldest lines once it
  // is full.
  //
  // Bounding it this way (rather than keeping every row and windowing the DOM
  // against a spacer) is what keeps the scrolling entirely the browser's: the
  // scroll range is the transcript that actually exists, the scrollbar never
  // promises rows that cannot be shown, and there is no script-driven scroll
  // correction to get wrong. The only thing script does when the oldest rows
  // fall off is compensate scrollTop by their height, so the view does not jump.
  //
  // Measured in Chrome: appending a row and following the tail costs ~1.8 ms per
  // frame once 10 000 transcript rows are in the DOM, and ~0.33 ms flat once the
  // transcript is bounded — unbounded, the cost grew with the session for as
  // long as the tab stayed open.
  var TRANSCRIPT_MAX = 2000;
  // TRANSCRIPT_TRIM_BATCH is how many rows fall off at once. Dropping one at a
  // time would shift the row list (and re-flow) for every single line.
  var TRANSCRIPT_TRIM_BATCH = 100;

  var transcriptRows = 0; // rows currently in the transcript list

  // raf is requestAnimationFrame where it exists; the timer fallback keeps the
  // page working in an engine that only has timers.
  var raf = window.requestAnimationFrame
    ? function (fn) { return window.requestAnimationFrame(fn); }
    : function (fn) { return setTimeout(fn, 16); };

  // ---------------------------------------------------------------- rendering

  // FOLLOW_SLACK_PX is how close to the bottom still counts as "at the bottom":
  // one line of slack keeps follow-tail from flapping on sub-pixel rounding.
  var FOLLOW_SLACK_PX = 24;

  // PAD is the grid's padding in px, applied on all four sides. It is what the
  // caret offset and the cell-count arithmetic below account for.
  var PAD = 8;

  // RESIZE_DEBOUNCE_MS collapses a drag-resize burst into one geometry message
  // (spec §14.6): every resize is a full server-side repaint, so sending one per
  // intermediate pixel would make the agent screen thrash.
  var RESIZE_DEBOUNCE_MS = 120;

  function atBottom() {
    return screenEl.scrollHeight - (screenEl.scrollTop + screenEl.clientHeight) <= FOLLOW_SLACK_PX;
  }

  function measure() {
    var probe = document.createElement("span");
    probe.textContent = "M";
    probe.style.visibility = "hidden";
    probe.style.position = "absolute";
    gridEl.appendChild(probe);
    var r = probe.getBoundingClientRect();
    gridEl.removeChild(probe);
    if (r.width > 0) charWidth = r.width;
    if (r.height > 0) lineHeight = r.height;
    // Publish the measured metrics to CSS: the row height and the caret size
    // must use the SAME cell size the geometry math uses, or a font/zoom change
    // desyncs them and the caret drifts off the input box.
    var root = document.documentElement.style;
    root.setProperty("--cell-w", charWidth + "px");
    root.setProperty("--cell-h", lineHeight + "px");
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
    var div = rowsEl.children[idx];
    if (!div) div = rowsEl.appendChild(buildRow(null));
    div.textContent = "";
    var runs = patch.runs || [];
    for (var i = 0; i < runs.length; i++) div.appendChild(runElement(runs[i]));
    rows[idx] = patch.runs;
  }

  // onScrollback appends the rows that scrolled off the live grid to the
  // transcript. Each row is shipped exactly once, so a plain append is all that
  // is needed — except when the server says the batch REPLACES the transcript
  // (msg.sbr): the transcript was wiped there (the compositor clears it before
  // re-emitting the whole history at a new width, exactly as CSI 3J empties a
  // terminal's scrollback), so the rows it holds now are the whole transcript
  // and appending them would paint the same lines twice.
  function onScrollback(msg) {
    if (!scrollEl) return;
    if (msg.sbr) clearTranscript();
    var list = msg.sb || [];
    if (!list.length) return;
    appendTranscript(list);
    followTail();
  }

  // clearTranscript empties the transcript list — the server's statement that
  // the rows it holds now are the whole history (a wipe it then re-emitted).
  function clearTranscript() {
    if (transcriptRows === 0 && scrollEl.children.length === 0) return;
    scrollEl.textContent = "";
    transcriptRows = 0;
  }

  // appendTranscript adds rows (oldest first) to the transcript and enforces the
  // bound.
  function appendTranscript(list) {
    trimTranscript(list.length);
    for (var i = 0; i < list.length; i++) {
      scrollEl.appendChild(buildRow(list[i].runs));
    }
    transcriptRows += list.length;
  }

  // trimTranscript makes room for `incoming` rows by dropping the oldest ones.
  // Rows past the bound fall off the FRONT — the oldest history — and a reader
  // who is scrolled up has the scroll offset moved with them, so the text they
  // are reading does not jump by the height of the dropped rows.
  //
  // While following the tail the offset is deliberately NOT compensated: there
  // the browser's own clamp already lands the view on the new bottom, and
  // subtracting the dropped height would leave it a batch short of the bottom —
  // which the next scroll event would read as "the user scrolled away" and
  // follow-tail would detach. (Found in a real browser: after a few trims the
  // view had drifted all the way to the top of the transcript.)
  //
  // It trims a batch at a time so the row list is not shifted (and re-flowed)
  // for every single line.
  function trimTranscript(incoming) {
    var total = transcriptRows + incoming;
    if (total <= TRANSCRIPT_MAX + TRANSCRIPT_TRIM_BATCH) return;
    var drop = total - TRANSCRIPT_MAX;
    for (var i = 0; i < drop; i++) {
      var first = scrollEl.children[0];
      if (!first) break;
      scrollEl.removeChild(first);
    }
    transcriptRows -= drop;
    // Everything below the dropped rows moved up by their height; moving the
    // scroll offset with it keeps the same content in the viewport.
    if (!following && screenEl.scrollTop > 0) {
      screenEl.scrollTop = Math.max(0, screenEl.scrollTop - drop * lineHeight);
    }
  }

  // followTail schedules the follow-tail scroll for the next animation frame. A
  // burst of frames produces ONE scroll, and the scrollHeight read that forces
  // layout happens once per painted frame instead of once per message.
  var tailQueued = false;

  function followTail() {
    if (!following || tailQueued) return;
    tailQueued = true;
    raf(function () {
      tailQueued = false;
      if (!following) return;
      screenEl.scrollTop = screenEl.scrollHeight;
    });
  }

  // resizeRows matches the row list to the server's screen height. It both
  // grows AND shrinks: a shrinking window that kept its old rows would leave
  // stale rows below the new bottom, inflating the scroll height and putting
  // the input box (and the caret) off-screen.
  function resizeRows(n) {
    while (rows.length < n) {
      rows.push(null);
      rowsEl.appendChild(document.createElement("div")).className = "row";
    }
    while (rows.length > n) {
      rows.pop();
      var last = rowsEl.children[rowsEl.children.length - 1];
      if (last) rowsEl.removeChild(last);
    }
  }

  function placeCaret(cur) {
    if (!cur || !cur.v) {
      caret.hidden = true;
      return;
    }
    caret.hidden = false;
    caret.style.left = (PAD + cur.c * charWidth) + "px";
    caret.style.top = (PAD + cur.r * lineHeight) + "px";
  }

  function onFrame(msg) {
    cols = msg.cols || cols;
    resizeRows(msg.rows || 0);
    (msg.patches || []).forEach(applyRow);
    placeCaret(msg.cur);
    if (msg.title) document.title = msg.title;
    // Follow-tail: only auto-scroll while the user has not scrolled away.
    followTail();
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
    return Math.max(20, Math.floor((screenEl.clientWidth - 2 * PAD) / charWidth));
  }

  function measureRows() {
    return Math.max(6, Math.floor((screenEl.clientHeight - 2 * PAD) / lineHeight));
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
  // keys with no engine binding, plus the browser's own navigation, devtools
  // and clipboard chords.
  var BROWSER_OWNED_FKEYS = { F5: 1, F11: 1, F12: 1 };
  var BROWSER_OWNED_CHORDS = { r: 1, q: 1 };
  var BROWSER_OWNED_DEVTOOLS = { i: 1, j: 1, c: 1 };

  // CLIPBOARD_CHORDS are the browser's own clipboard chords. They must stay the
  // browser's: `preventDefault` on a Ctrl/Cmd+V keydown suppresses the very
  // `paste` event this page relies on, and claiming Ctrl+C makes selecting text
  // and copying it do nothing. The paste handler below turns a paste into
  // terminal input, and a copy with a live selection needs no help from us.
  var CLIPBOARD_CHORDS = { c: 1, v: 1, x: 1 };

  // clipboardOwned reports whether a chord belongs to the browser's clipboard.
  // Ctrl+C is only the browser's when something is selected: with no selection
  // it is the terminal's interrupt, which the agent must still receive.
  function clipboardOwned(ev) {
    var k = (ev.key || "").toLowerCase();
    if (!CLIPBOARD_CHORDS[k]) return false;
    if (k === "c") {
      var sel = window.getSelection ? String(window.getSelection()) : "";
      return sel.length > 0;
    }
    return true;
  }

  // browserOwned reports whether a keydown belongs to the browser.
  function browserOwned(ev) {
    if (BROWSER_OWNED_FKEYS[ev.key]) return true;
    if (!ev.ctrlKey && !ev.metaKey) return false;
    var k = (ev.key || "").toLowerCase();
    if (ev.shiftKey) return !!BROWSER_OWNED_DEVTOOLS[k];
    if (clipboardOwned(ev)) return true;
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

  // A resize burst is debounced (spec §14.6): the cell metrics are re-measured
  // immediately (the layout is already changing), but the geometry message the
  // server turns into a full repaint waits for the drag to settle.
  var resizeTimer = null;

  window.addEventListener("resize", function () {
    measure();
    if (resizeTimer) clearTimeout(resizeTimer);
    resizeTimer = setTimeout(function () {
      resizeTimer = null;
      send({ t: "resize", cols: measureCols(), rows: measureRows() });
    }, RESIZE_DEBOUNCE_MS);
  });

  // Follow-tail state follows the user's own scroll: scrolling up detaches,
  // returning to the bottom re-attaches. The listener is passive so it never
  // blocks the browser's scrolling, and it is on the scroll container — the
  // element that actually scrolls — not on the transcript list inside it.
  // Nothing else needs to run on scroll: the transcript is a real list of real
  // rows, so the browser owns the whole interaction.
  if (screenEl) {
    screenEl.addEventListener("scroll", function () {
      following = atBottom();
    }, { passive: true });
  }

  // Typing must reach the terminal even when nothing is focused.
  document.addEventListener("click", function () { gridEl.focus(); });

  measure();
  connect();
})();