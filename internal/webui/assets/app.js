// goa web UI client — the browser is the terminal.
//
// Receives row patches over a WebSocket, paints each row as a div of styled
// spans, keeps the rows that scrolled off in a transcript list, and turns
// keydown/paste into the raw bytes a real terminal would send (spec §7.5).
// Plain ES2018, no dependencies, no build step.
//
// Two planes share this file (specs/webui.md §22):
//
//   cells  the v1 pipeline: the whole screen as cell rows, a bounded row
//          list as history. PLANE === "cells".
//   blocks the conversation as semantic blocks rendered into HTML flow
//          content (#blocks) — native reflow, native history — while the
//          bottom chrome band (editor + status) stays cell-rendered in
//          #grid. An input-capturing overlay flips the page to full-cell
//          rendering until it closes. PLANE === "blocks".

(function () {
  "use strict";

  var SESSION = window.GOA_SESSION || "";
  // screenEl is the ONE scroll container (transcript + live grid); the grid and
  // the caret live inside it, so a scroll moves the caret with the content it
  // marks. scrollEl is the transcript list itself (cells plane only).
  var screenEl = document.getElementById("screen");
  var scrollEl = document.getElementById("scrollback");
  var blocksEl = document.getElementById("blocks");
  var gridEl = document.getElementById("grid");
  var rowsEl = document.getElementById("rows");
  var caret = document.getElementById("caret");
  var status = document.getElementById("status");

  var PLANE = document.body.dataset.plane === "blocks" ? "blocks" : "cells";

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

  if (PLANE === "blocks") {
    scrollEl.hidden = true;
    blocksEl.hidden = false;
  }

  // ---------------------------------------------------------------- transcript

  // The transcript is the one part of the page that grows with the session, so
  // it is BOUNDED — exactly like a terminal's scrollback buffer, which also
  // discards the oldest lines once it is full. The cells plane bounds row divs,
  // the blocks plane bounds block elements (their heights vary, so the scroll
  // compensation reads each removed element's height instead of counting rows).

  var TRANSCRIPT_MAX = 2000;
  // TRANSCRIPT_TRIM_BATCH is how many rows fall off at once. Dropping one at a
  // time would shift the row list (and re-flow) for every single line.
  var TRANSCRIPT_TRIM_BATCH = 100;

  var BLOCKS_MAX = 800;
  var BLOCKS_TRIM_BATCH = 50;

  var transcriptRows = 0; // rows currently in the transcript list (cells plane)

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
  // transcript (cells plane only; the blocks plane never receives one). Each
  // row is shipped exactly once, so a plain append is all that is needed —
  // except when the server says the batch REPLACES the transcript (msg.sbr):
  // the transcript was wiped there (the compositor clears it before
  // re-emitting the whole history at a new width, exactly as CSI 3J empties a
  // terminal's scrollback), so the rows it holds now are the whole transcript
  // and appending them would paint the same lines twice.
  function onScrollback(msg) {
    if (PLANE !== "cells" || !scrollEl) return;
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
  //
  // A row-count change is LAYOUT work, not user intent, but it moves the scroll
  // offset too — a shrink clamps scrollTop when the content no longer reaches
  // that far, which fires a scroll event. The listener below reads any scroll as
  // "the user scrolled away", so a window resize silently disarmed follow-tail
  // for the rest of the session and the live output stopped being followed
  // (bugs.md footer_band). The pin below re-asserts the intent synchronously: by
  // the time that clamp's scroll event is delivered the view is already back at
  // the bottom, so the listener re-computes the same answer.
  //
  // Unchanged row counts return immediately — the common per-frame case — so the
  // "one layout read per painted frame" contract of followTail still holds.
  function resizeRows(n) {
    if (n === rows.length) return;
    var wasFollowing = following;
    while (rows.length < n) {
      rows.push(null);
      rowsEl.appendChild(document.createElement("div")).className = "row";
    }
    while (rows.length > n) {
      rows.pop();
      var last = rowsEl.children[rowsEl.children.length - 1];
      if (last) rowsEl.removeChild(last);
    }
    if (wasFollowing && screenEl) {
      following = true;
      screenEl.scrollTop = screenEl.scrollHeight;
    }
  }

  // cursor is the engine's cursor as of the last frame (grid row/column), or
  // null while it is hidden. The clipboard chords need it to find the terminal's
  // input line — the one editable row on the page.
  var cursor = null;

  // bandChrome/overlayMode describe the blocks-plane band the caret lives in:
  // the caret is positioned relative to #grid, which shows only the LAST
  // bandChrome rows of the grid model until an overlay flips the page to
  // full-cell rendering. In cells mode bandChrome stays 0 (grid == screen).
  var bandChrome = 0;
  var overlayMode = false;

  function placeCaret(cur) {
    if (!cur || !cur.v) {
      cursor = null;
      caret.hidden = true;
      return;
    }
    cursor = cur;
    caret.hidden = false;
    // The band window has no top padding (see app.css) while the cells and
    // overlay planes keep the full PAD — the inset follows the mode.
    var topInset = (PLANE === "blocks" && !overlayMode) ? 0 : PAD;
    var rel = cur.r - Math.max(0, rows.length - bandChrome);
    if (overlayMode || rel < 0) rel = cur.r;
    caret.style.left = (PAD + cur.c * charWidth) + "px";
    caret.style.top = (topInset + rel * lineHeight) + "px";
  }

  // ─────────────────────────────────────────────────────────────── block plane

  // blocks holds the client-side model of every conversation block: id →
  // {id, kind, text, meta, el, body}. applyBlocks is the only writer.
  var blocks = {};

  // applyBlocks wires one frame's block operations into #blocks. A "reset"
  // op clears everything first (history compressed or cleared); "set"
  // creates or updates one block; a From offset means the text is a suffix
  // to append (a streaming block strictly growing).
  function applyBlocks(ops) {
    if (!ops || !ops.length) return;
    var dirty = false;
    for (var i = 0; i < ops.length; i++) {
      var op = ops[i];
      if (op.op === "reset") {
        blocksEl.textContent = "";
        blocks = {};
        dirty = true;
        continue;
      }
      if (op.op === "del") {
        var gone = blocks[op.id];
        if (gone && gone.el.parentNode) gone.el.parentNode.removeChild(gone.el);
        delete blocks[op.id];
        dirty = true;
        continue;
      }
      if (op.op !== "set") continue;
      var b = blocks[op.id];
      if (!b) {
        b = blocks[op.id] = { id: op.id, kind: "", text: "", meta: {}, el: null, body: null };
      }
      if (op.from > 0 && b.text.length <= op.from) {
        b.text = b.text.substring(0, op.from) + (op.text || "");
      } else if (op.from > 0 && b.text.length > op.from) {
        // The suffix assumption broke (a rewrite raced a delta): fall back to
        // replacing the tail wholesale, which is always a safe superset.
        b.text = b.text.substring(0, op.from) + (op.text || "");
      } else {
        b.text = op.text || "";
        if (op.kind) b.kind = op.kind;
        if (op.meta) b.meta = op.meta;
      }
      if (op.runs) b.runs = op.runs;
      renderBlock(b);
      dirty = true;
    }
    if (dirty) {
      trimBlocks();
      followTail();
    }
  }

  // trimBlocks bounds the block list the way trimTranscript bounds rows,
  // compensating the scroll offset by the removed elements' real heights
  // (blocks reflow, so no two blocks are the same height).
  function trimBlocks() {
    var kids = blocksEl.children;
    if (kids.length <= BLOCKS_MAX + BLOCKS_TRIM_BATCH) return;
    var drop = kids.length - BLOCKS_MAX;
    var height = 0;
    for (var i = 0; i < drop; i++) {
      var el = kids[0];
      if (!el) break;
      height += el.offsetHeight;
      delete blocks[Number(el.getAttribute("data-bid"))];
      blocksEl.removeChild(el);
    }
    if (!following && screenEl.scrollTop > 0) {
      screenEl.scrollTop = Math.max(0, screenEl.scrollTop - height);
    }
  }

  // el clears and fills one element from a maker function (all content goes
  // through DOM APIs — no innerHTML for anything the server sent).
  function fill(el, make) {
    el.textContent = "";
    make(el);
    return el;
  }

  // renderBlock (re)builds one block's DOM from its model. Interactive state
  // the user owns (a <details> they opened or closed) survives the re-render:
  // streaming updates must not fight the reader.
  function renderBlock(b) {
    var kind = b.kind || "info";
    if (!b.el) {
      b.el = document.createElement("div");
      b.el.className = "block";
      b.el.setAttribute("data-bid", b.id);
      blocksEl.appendChild(b.el);
    }
    b.el.className = "block " + kind;
    if (kind === "tool") b.el.setAttribute("data-status", (b.meta && b.meta.status) || "pending");
    var wasOpen = b.body && b.body.open;
    fill(b.el, function (root) {
      b.body = null;
      var meta = b.meta || {};
      switch (kind) {
        case "header": {
          var pre = document.createElement("pre");
          var runs = b.runs || [];
          for (var i = 0; i < runs.length; i++) pre.appendChild(runElement(runs[i]));
          root.appendChild(pre);
          break;
        }
        case "user": {
          // The TUI paints the user's text as one band (user_msg bg/fg); an
          // inline pre-wrap span keeps multi-line input inside that band —
          // a block-level child would start on a new line below it.
          var utext = document.createElement("span");
          utext.className = "u-text";
          utext.textContent = b.text;
          root.appendChild(utext);
          break;
        }
        case "assistant": {
          var md = document.createElement("div");
          md.className = "md";
          renderMarkdown(b.text, md);
          root.appendChild(md);
          break;
        }
        case "agent": {
          if (meta.agent) {
            var chip = document.createElement("div");
            chip.className = "agent-chip";
            chip.textContent = "[" + meta.agent + "]";
            root.appendChild(chip);
          }
          var amd = document.createElement("div");
          amd.className = "md";
          renderMarkdown(b.text, amd);
          root.appendChild(amd);
          break;
        }
        case "thinking": {
          var det = collapsible(wasOpen, meta.expanded !== "0");
          var sum = document.createElement("summary");
          sum.textContent = "thinking" + (meta.agent ? " — " + meta.agent : "") + "…";
          det.appendChild(sum);
          var body = document.createElement("div");
          body.className = "body";
          body.textContent = b.text;
          det.appendChild(body);
          b.body = det;
          root.appendChild(det);
          break;
        }
        case "system": {
          // The TUI's goa panel renders command output as markdown unless the
          // text looks preformatted (tui/chat_viewport_markdown.go heuristics
          // mirrored below) — the page must agree with it or /quota and its
          // siblings show raw source.
          if (looksPreformatted(b.text)) {
            var sper = document.createElement("pre");
            sper.textContent = b.text;
            root.appendChild(sper);
          } else {
            var span = document.createElement("div");
            span.className = "md";
            renderMarkdown(b.text, span);
            root.appendChild(span);
          }
          break;
        }
        case "tool": {
          var tdet = collapsible(wasOpen, meta.expanded !== "0");
          var tsum = document.createElement("summary");
          var name = document.createElement("span");
          name.className = "t-name";
          name.textContent = meta.tool || b.text || "tool";
          tsum.appendChild(name);
          if (meta.args) {
            var args = document.createElement("span");
            args.className = "t-args";
            args.textContent = " " + meta.args;
            tsum.appendChild(args);
          }
          var st = document.createElement("span");
          st.className = "t-status";
          st.textContent = " [" + (meta.status || "pending") + (meta.duration ? " · " + meta.duration : "") + "]";
          tsum.appendChild(st);
          tdet.appendChild(tsum);
          // Collapsed cards preview the head of the output (the TUI widget's
          // preview); the open body carries everything, scroll-capped.
          var prev = document.createElement("pre");
          prev.className = "t-preview";
          prev.textContent = headLines(b.text, 4);
          tdet.appendChild(prev);
          var out = document.createElement("pre");
          out.className = "t-out";
          out.textContent = b.text || "";
          tdet.appendChild(out);
          b.body = tdet;
          root.appendChild(tdet);
          break;
        }
        default: {
          var plain = document.createElement("pre");
          plain.textContent = b.text;
          root.appendChild(plain);
        }
      }
    });
  }

  // collapsible builds a <details> whose open state honours the block's
  // metadata but never undoes an explicit user toggle (wasOpen wins).
  function collapsible(wasOpen, metaOpen) {
    var det = document.createElement("details");
    if (wasOpen !== undefined) det.open = wasOpen;
    else det.open = !!metaOpen;
    return det;
  }

  // ───────────────────────────────────────── markdown classification
  //
  // The TUI decides per message whether text is markdown or verbatim
  // (isPreformatted / looksLikeMarkdown in tui/chat_viewport_markdown.go).
  // The page mirrors those heuristics so both renderers agree on which
  // blocks get the markdown treatment — /quota and friends arrive as system
  // panels whose source is markdown.

  // looksLikeMarkdown reports whether any line carries markdown syntax.
  function looksLikeMarkdown(text) {
    var lines = (text || "").split("\n");
    if (lines.length < 3) return false;
    for (var i = 0; i < lines.length; i++) {
      var t = lines[i].trim();
      if (!t) continue;
      if (/^#{1,6} /.test(t)) return true;          // ATX heading
      if (t.lastIndexOf("```", 0) === 0) return true; // fence
      if ((t.charAt(0) === "-" || t.charAt(0) === "*") && t.charAt(1) === " ") return true;
      if (t.charAt(0) >= "0" && t.charAt(0) <= "9" && t.charAt(1) === ".") return true;
      if (t.charAt(0) === "|" && t.charAt(t.length - 1) === "|") return true; // table
      if (/^(-{3,4}|\*{3,4})$/.test(t)) return true; // thematic break
      if (t.indexOf("`") >= 0) return true;          // inline code
    }
    return false;
  }

  // looksPreformatted mirrors isPreformatted: markdown is rendered; plain
  // tabular/indented text (wide lines, indented commands) is shown verbatim.
  function looksPreformatted(text) {
    if (looksLikeMarkdown(text)) return false;
    var lines = (text || "").split("\n");
    if (lines.length < 2) return false;
    var indentedCommands = false;
    var longLines = 0;
    for (var i = 0; i < lines.length; i++) {
      if (/^  \//.test(lines[i])) indentedCommands = true;
      if (lines[i].length > 60) longLines++;
    }
    return indentedCommands || longLines >= 2;
  }

  // headLines returns the first n non-empty-biased lines of text for a
  // collapsed card's preview.
  function headLines(text, n) {
    var lines = (text || "").split("\n");
    var out = [];
    for (var i = 0; i < lines.length && out.length < n; i++) {
      out.push(lines[i]);
    }
    var joined = out.join("\n");
    if (lines.length > n) joined += "\n… +" + (lines.length - n) + " lines";
    return joined;
  }

  // ─────────────────────────────────────────────── markdown (goa's subset)

  // renderMarkdown renders the markdown subset goa emits into DOM nodes:
  // ATX headings, fenced code, bullet/numbered lists, blockquotes, pipe
  // tables, hr, paragraphs; inline: bold, italic, strikethrough, inline
  // code, links. Everything lands via textContent — server text can never
  // become markup.
  function renderMarkdown(src, root) {
    var lines = (src || "").split("\n");
    var i = 0;
    while (i < lines.length) {
      var line = lines[i];
      if (/^\s*$/.test(line)) { i++; continue; }
      var fence = /^\s*```\s*(\S*)\s*$/.exec(line);
      if (fence) {
        var code = [];
        i++;
        while (i < lines.length && !/^\s*```\s*$/.test(lines[i])) {
          code.push(lines[i]);
          i++;
        }
        i++; // closing fence (or EOF)
        var pre = document.createElement("pre");
        var c = document.createElement("code");
        if (fence[1]) c.setAttribute("data-lang", fence[1]);
        c.textContent = code.join("\n");
        pre.appendChild(c);
        root.appendChild(pre);
        continue;
      }
      var h = /^(#{1,4})\s+(.*)$/.exec(line);
      if (h) {
        var hd = document.createElement("h" + h[1].length);
        renderInline(h[2], hd);
        root.appendChild(hd);
        i++;
        continue;
      }
      if (/^\s*(-{3,}|\*{3,}|_{3,})\s*$/.test(line)) {
        root.appendChild(document.createElement("hr"));
        i++;
        continue;
      }
      if (/^\s*>\s?/.test(line)) {
        var q = [];
        while (i < lines.length && /^\s*>\s?/.test(lines[i])) {
          q.push(lines[i].replace(/^\s*>\s?/, ""));
          i++;
        }
        var bq = document.createElement("blockquote");
        renderMarkdown(q.join("\n"), bq);
        root.appendChild(bq);
        continue;
      }
      if (/^\s*[-*+]\s+/.test(line)) {
        var ul = document.createElement("ul");
        while (i < lines.length && /^\s*[-*+]\s+/.test(lines[i])) {
          var li = document.createElement("li");
          renderInline(lines[i].replace(/^\s*[-*+]\s+/, ""), li);
          ul.appendChild(li);
          i++;
        }
        root.appendChild(ul);
        continue;
      }
      if (/^\s*\d+[.)]\s+/.test(line)) {
        var ol = document.createElement("ol");
        while (i < lines.length && /^\s*\d+[.)]\s+/.test(lines[i])) {
          var oli = document.createElement("li");
          renderInline(lines[i].replace(/^\s*\d+[.)]\s+/, ""), oli);
          ol.appendChild(oli);
          i++;
        }
        root.appendChild(ol);
        continue;
      }
      if (/^\s*\|.*\|\s*$/.test(line) && i + 1 < lines.length && /^\s*\|[\s:|-]+\|\s*$/.test(lines[i + 1])) {
        var tbl = document.createElement("table");
        var head = splitRow(line);
        var thead = document.createElement("tr");
        head.forEach(function (cell) {
          var th = document.createElement("th");
          renderInline(cell, th);
          thead.appendChild(th);
        });
        tbl.appendChild(thead);
        i += 2;
        while (i < lines.length && /^\s*\|.*\|\s*$/.test(lines[i])) {
          var tr = document.createElement("tr");
          splitRow(lines[i]).forEach(function (cell) {
            var td = document.createElement("td");
            renderInline(cell, td);
            tr.appendChild(td);
          });
          tbl.appendChild(tr);
          i++;
        }
        root.appendChild(tbl);
        continue;
      }
      // Paragraph: consecutive non-blank, non-structural lines.
      var para = [];
      while (i < lines.length && !/^\s*$/.test(lines[i]) &&
             !/^\s*```/.test(lines[i]) && !/^#{1,4}\s/.test(lines[i]) &&
             !/^\s*[-*+]\s+/.test(lines[i]) && !/^\s*\d+[.)]\s+/.test(lines[i]) &&
             !/^\s*>\s?/.test(lines[i]) && !/^\s*\|.*\|\s*$/.test(lines[i])) {
        para.push(lines[i]);
        i++;
      }
      if (para.length) {
        var p = document.createElement("p");
        renderInline(para.join("\n"), p);
        root.appendChild(p);
      } else {
        i++; // structural-line safety net: never stall the walk
      }
    }
  }

  function splitRow(line) {
    var t = line.trim();
    t = t.replace(/^\|/, "").replace(/\|$/, "");
    var cells = t.split("|");
    for (var i = 0; i < cells.length; i++) cells[i] = cells[i].trim();
    return cells;
  }

  // INLINE_RE matches, in priority order: bold, inline code, italic,
  // strikethrough, links. The first matching alternative wins.
  var INLINE_RE = /\*\*([^*]+)\*\*|`([^`]+)`|\*([^*\n]+)\*|~~([^~]+)~~|\[([^\]]+)\]\(([^)\s]+)\)/g;

  // renderInline appends the inline-level rendering of text to el.
  function renderInline(text, el) {
    var m;
    var last = 0;
    INLINE_RE.lastIndex = 0;
    while ((m = INLINE_RE.exec(text)) !== null) {
      if (m.index > last) el.appendChild(document.createTextNode(text.slice(last, m.index)));
      if (m[1] !== undefined) {
        var b = document.createElement("strong");
        b.textContent = m[1];
        el.appendChild(b);
      } else if (m[2] !== undefined) {
        var c = document.createElement("code");
        c.textContent = m[2];
        el.appendChild(c);
      } else if (m[3] !== undefined) {
        var it = document.createElement("em");
        it.textContent = m[3];
        el.appendChild(it);
      } else if (m[4] !== undefined) {
        var d = document.createElement("del");
        d.textContent = m[4];
        el.appendChild(d);
      } else if (m[5] !== undefined) {
        if (external(m[6])) {
          var a = linkElement(m[6]);
          a.textContent = m[5];
          el.appendChild(a);
        } else {
          el.appendChild(document.createTextNode(m[5] + " (" + m[6] + ")"));
        }
      }
      last = INLINE_RE.lastIndex;
    }
    if (last < text.length) el.appendChild(document.createTextNode(text.slice(last)));
  }

  function onFrame(msg) {
    cols = msg.cols || cols;
    resizeRows(msg.rows || 0);
    // The band state is applied BEFORE the patches and the caret: the caret
    // is positioned relative to the band window, and a frame that resized
    // the band (popup opened/filtered/closed, editor wrapped) must not place
    // it against the previous height — that put it outside the window on
    // every band-resizing keystroke.
    if (PLANE === "blocks") {
      var chrome = msg.chrome | 0;
      if (chrome !== bandChrome) {
        bandChrome = chrome;
        document.documentElement.style.setProperty("--chrome", String(Math.max(chrome, 1)));
      }
      var wantOverlay = !!msg.ovl;
      if (wantOverlay !== overlayMode) {
        overlayMode = wantOverlay;
        screenEl.classList.toggle("overlay-mode", overlayMode);
      }
      if (msg.blocks) applyBlocks(msg.blocks);
    }
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
          // Anything the user typed while the socket was connecting goes out now.
          flushSendQueue();
        };
        socket.onmessage = function (ev) { handleWire(ev.data); };
        socket.onclose = function () {
          clearTimeout(handshake);
          // A deliberate detach owns this tab's socket lifecycle: no retry.
          if (detached) return;
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
                  // A deliberate detach owns this tab's stream lifecycle.
                  if (detached) {
                    es.close();
                    stream = null;
                    return;
                  }
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
          return;
        }
        // The socket may still be connecting — the moment the listener answers
        // but the session is still wiring itself up. A keystroke typed in that
        // window is the user's input, not noise: hold it and flush on open
        // (bounded, so a permanently dead socket cannot grow the queue). The
        // server replays what reaches it before its engine starts, so the two
        // halves together make typing effective from the first frame.
        if (socket && socket.readyState === WebSocket.CONNECTING) {
          if (sendQueue.length < SEND_QUEUE_MAX) sendQueue.push(obj);
        }
      }

      // SEND_QUEUE_MAX bounds the pre-open input buffer.
      var SEND_QUEUE_MAX = 256;
      // sendQueue holds messages produced before the socket opened.
      var sendQueue = [];

      // flushSendQueue delivers the buffered messages in order, once.
      function flushSendQueue() {
        if (!socket || socket.readyState !== WebSocket.OPEN) return;
        var queued = sendQueue;
        sendQueue = [];
        for (var i = 0; i < queued.length; i++) socket.send(JSON.stringify(queued[i]));
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

  document.addEventListener("keyup", function (ev) {
    // The keyup of a paste chord ends its paste window: by now the browser has
    // either delivered the clipboard as a `paste` event or never will.
    if (ev.key === "v" && !ev.altKey && !ev.shiftKey) finishPaste();
  });

  document.addEventListener("keydown", function (ev) {
    // A detached tab drives nothing.
    if (detached) { ev.preventDefault(); return; }
    // Ctrl/Cmd+D closes THIS window: the tab detaches from the session and
    // the browser performs (or is invited to perform) its own close. It must
    // never reach the engine — an EOF byte would end the whole session and
    // take the server down for every other viewer; the server process is
    // owned by the console it was started from (its Ctrl+C).
    if ((ev.ctrlKey || ev.metaKey) && !ev.altKey && !ev.shiftKey &&
        (ev.key || "").toLowerCase() === "d") {
      ev.preventDefault();
      detachTab();
      return;
    }
    // Chords the browser owns stay the browser's: reload, devtools and tab
    // switching are how a user escapes a page, and a terminal that swallows
    // them is a trap. Everything else — including the engine's own Ctrl+W /
    // Ctrl+L bindings — is claimed, so the terminal behaves like a terminal.
    if (browserOwned(ev)) return;
    // The clipboard chords are the page's own (see CLIPBOARD_CHORDS). A viewer
    // may still copy — reading the screen is not driving it.
    var chord = clipboardChord(ev);
    if (chord === CHORD_DONE) {
      ev.preventDefault();
      return;
    }
    if (chord === CHORD_BROWSER) return;
    if (readOnly) return;
    // Modifier-only presses and Meta chords are sent as-is; the server's
    // encoder declines to encode them, and the browser keeps its default.
    send({ t: "key", key: keyEvent(ev) });
    ev.preventDefault();
  });

  // detached stops the key/paste paths after a detach: the tab no longer
  // drives or watches the session.
  var detached = false;

  // detachTab disconnects this tab from the session and asks the browser to
  // close the window (allowed outright for script-opened tabs; elsewhere the
  // status line says the tab is free to close).
  function detachTab() {
    if (detached) return;
    detached = true;
    try { if (socket) socket.close(); } catch (e) { /* already closing */ }
    if (stream) {
      try { stream.close(); } catch (e) { /* already closing */ }
    }
    setStatus("closed", "detached — you can close this tab");
    window.close();
  }

  // BROWSER_OWNED is the set of chords the page never claims: the function
  // keys with no engine binding, plus the browser's own navigation and devtools
  // chords.
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

  // ---------------------------------------------------------------- clipboard
  //
  // Copy, cut and paste are the PAGE's, not the browser's.
  //
  // Measured in Chrome with trusted chord events and a real clipboard: with
  // `#grid` — a plain non-editable div — focused, Cmd+C over a live selection
  // copies nothing (no `copy` event, clipboard unchanged) and Cmd+V inserts
  // nothing (no `paste` event at all). The browser's implicit clipboard chords
  // are wired to editable targets, and the terminal's rows are output: there is
  // nothing there for it to paste into. So the page performs the chords itself.
  //
  // Copy and cut write through the async clipboard where it exists (a secure
  // context: https, or http on loopback) and fall back to
  // `document.execCommand("copy")` — both work from a keydown, which is where a
  // user gesture comes from. Paste cannot be read the same way: Chrome leaves
  // `navigator.clipboard.readText()` behind a permission prompt even with a user
  // gesture (measured in a real browser: the promise never settles until the
  // prompt is answered). So a paste is handed to the browser first — the hidden
  // `#paste-target` below gives it an editable target to paste into, which
  // arrives as a `paste` event carrying the clipboard, no permission involved —
  // and only when no paste event shows up (a script-dispatched chord performs no
  // browser paste action at all) does the page read the clipboard itself.

  // CLIPBOARD_CHORDS are the chords the page claims for the clipboard.
  var CLIPBOARD_CHORDS = { c: 1, v: 1, x: 1 };

  // PASTE_WINDOW_MS backstops the paste window when no keyup ever arrives (a
  // chord delivered without one): the browser's own paste for a key event is part
  // of that event's dispatch, so anything after it is already too late to matter.
  var PASTE_WINDOW_MS = 500;

  // pasteTarget is a hidden textarea that exists for one reason: the browser
  // pastes into an editable element, and the terminal's grid is not one. It is
  // focused for the duration of a paste chord (never otherwise) and its value is
  // always empty — the paste event is prevented before the text lands in it.
  var pasteTarget = document.createElement("textarea");
  pasteTarget.id = "paste-target";
  pasteTarget.setAttribute("aria-hidden", "true");
  pasteTarget.setAttribute("autocapitalize", "off");
  pasteTarget.setAttribute("autocomplete", "off");
  pasteTarget.setAttribute("autocorrect", "off");
  pasteTarget.setAttribute("spellcheck", "false");
  pasteTarget.setAttribute("tabindex", "-1");
  pasteTarget.style.position = "fixed";
  pasteTarget.style.top = "0";
  pasteTarget.style.left = "-1000px";
  pasteTarget.style.width = "1px";
  pasteTarget.style.height = "1px";
  pasteTarget.style.opacity = "0";
  document.body.appendChild(pasteTarget);

  // pastePending marks a paste chord whose keyup has not been seen yet, and
  // pasteHandled records that the browser's own paste already delivered the
  // clipboard, so the fallback must not deliver it a second time.
  var pastePending = false;
  var pasteHandled = false;

  // hasSelection reports whether the page has a live text selection.
  function hasSelection() {
    return !!(window.getSelection && String(window.getSelection()).length > 0);
  }

  // clipboardAPI is the async clipboard, or null on an origin that has none
  // (plain http on a LAN address): there the legacy path is all there is.
  function clipboardAPI() {
    return (typeof navigator !== "undefined" && navigator.clipboard) || null;
  }

  // canReadClipboard reports whether the page has any clipboard read path.
  function canReadClipboard() {
    var api = clipboardAPI();
    return !!(api && (api.read || api.readText));
  }

  // focusTerminal puts the keyboard back on the grid. The paste target exists
  // only for the instant the browser needs an editable element to paste into.
  function focusTerminal() {
    if (document.activeElement === pasteTarget && gridEl) gridEl.focus();
  }

  // beginPaste hands a paste chord to the browser: the textarea takes focus so
  // the browser has somewhere to paste, and a `paste` event (with the clipboard
  // in clipboardData) is what the handler below turns into terminal input. That
  // is the path a real keyboard takes, and it needs no permission.
  function beginPaste() {
    pastePending = true;
    pasteHandled = false;
    pasteTarget.focus();
    setTimeout(finishPaste, PASTE_WINDOW_MS);
  }

  // finishPaste closes the paste window, from the chord's keyup or the backstop
  // timer. When the browser's own paste did not deliver the clipboard — a chord
  // dispatched without a keyboard performs no browser paste at all — the page
  // reads it itself. A key event is what a clipboard read wants, so keyup is the
  // point to do it from: the read keeps the user activation the API requires.
  function finishPaste() {
    if (!pastePending) return;
    pastePending = false;
    focusTerminal();
    if (pasteHandled || !canReadClipboard()) return;
    pasteFromClipboard().then(function (read) {
      if (!read) setStatus("error", "paste blocked — allow clipboard access for this site");
    });
  }

  // legacyCopy writes text through a detached textarea. execCommand is
  // deprecated, but it is the only write path an insecure origin has, and it
  // works in every browser that has a clipboard at all.
  function legacyCopy(text) {
    var ta = document.createElement("textarea");
    ta.value = text;
    ta.setAttribute("aria-hidden", "true");
    ta.setAttribute("tabindex", "-1");
    ta.style.position = "fixed";
    ta.style.top = "0";
    ta.style.left = "-1000px";
    document.body.appendChild(ta);
    ta.select();
    var ok = false;
    try {
      ok = document.execCommand("copy");
    } catch (err) {
      ok = false;
    }
    document.body.removeChild(ta);
    return ok;
  }

  // writeClipboard puts text on the real clipboard and reports whether it got
  // there, so a caller can tell a refusal from a success.
  function writeClipboard(text) {
    if (!text) return Promise.resolve(false);
    var api = clipboardAPI();
    if (!api || !api.writeText) return Promise.resolve(legacyCopy(text));
    return api.writeText(text).then(
      function () { return true; },
      function () { return legacyCopy(text); }
    );
  }

  // insertText sends pasted text as terminal input — the same message the
  // `paste` event handler sends, so the editor's own paste handling applies
  // (spec §7.5).
  function insertText(text) {
    if (!text) return false;
    send({ t: "input", data: text });
    return true;
  }

  // imageType picks the image MIME type of a clipboard item, or "text/plain"
  // when the item carries no image.
  function imageType(item) {
    var types = (item && item.types) || [];
    for (var i = 0; i < types.length; i++) {
      if (types[i].indexOf("image/") === 0) return types[i];
    }
    return "text/plain";
  }

  // insertClipboardItems walks the clipboard's items: an image is uploaded and
  // its stored path inserted (the terminal's own image-paste behaviour), text is
  // sent as input. Reading an item is asynchronous, so the walk is a promise
  // chain and insertion order is preserved.
  function insertClipboardItems(items) {
    var chain = Promise.resolve(true);
    (items || []).forEach(function (item) {
      var type = imageType(item);
      chain = chain.then(function () {
        return item.getType(type).then(function (blob) {
          if (type.indexOf("image/") !== 0) {
            if (!blob.text) return false;
            return blob.text().then(insertText);
          }
          return uploadImage(blob).then(function (path) {
            if (path) send({ t: "input", data: path });
            return true;
          });
        }, function () { return false; });
      });
    });
    return chain;
  }

  // readClipboardText is the text-only read path, for a browser with readText
  // but no read().
  function readClipboardText() {
    var api = clipboardAPI();
    if (!api || !api.readText) return Promise.resolve(false);
    return api.readText().then(function (text) {
      insertText(text);
      return true;
    }, function () { return false; });
  }

  // pasteFromClipboard inserts the clipboard's contents, resolving true when the
  // clipboard could be read at all — an empty clipboard is not a failure.
  function pasteFromClipboard() {
    var api = clipboardAPI();
    if (!api) return Promise.resolve(false);
    if (api.read) return api.read().then(insertClipboardItems, readClipboardText);
    return readClipboardText();
  }

  // copySelection copies the live selection.
  function copySelection() {
    return writeClipboard(String(window.getSelection())).then(function (ok) {
      if (!ok) setStatus("error", "copy blocked — the browser refused clipboard access");
      return ok;
    });
  }

  // cutSelection copies the live selection and removes it from the terminal's
  // input line. The rows above the input line are output — nothing there can be
  // cut — so a selection that reaches into the transcript is copied and left
  // alone.
  function cutSelection() {
    var sel = window.getSelection();
    var range = sel && sel.rangeCount ? sel.getRangeAt(0) : null;
    return writeClipboard(String(sel || "")).then(function (ok) {
      if (!ok) setStatus("error", "cut blocked — the browser refused clipboard access");
      if (range) deleteSelectedRange(range);
      return ok;
    });
  }

  // MAX_KEY_BURST bounds a synthesized edit: a longer selection is still copied,
  // but the input line is not walked one keypress at a time forever.
  var MAX_KEY_BURST = 512;

  // sendKeyBurst repeats one named key (Backspace, Delete, ArrowLeft, …). The
  // engine's editor is driven the way a keyboard drives it, so its undo,
  // kill-ring and auto-complete behaviour stay the terminal's own (spec §7.5).
  function sendKeyBurst(name, count) {
    if (count < 1) return;
    if (count > MAX_KEY_BURST) count = MAX_KEY_BURST;
    for (var i = 0; i < count; i++) send({ t: "key", key: { key: name } });
  }

  // columnAt maps a viewport x to the cell column it falls on inside a row. Cell
  // columns are what the cursor position (cur.c) counts in, and pixels are the
  // only place a wide glyph's two cells are visible to this code.
  function columnAt(rowBox, x) {
    var col = Math.round((x - rowBox.left) / charWidth);
    return col > 0 ? col : 0;
  }

  // singleWidth reports whether every character in text occupies one cell. The
  // editor's buffer index and a grid cell column coincide only then, which is
  // what lets a cut walk the cursor by column arithmetic.
  function singleWidth(text) {
    for (var i = 0; i < (text || "").length; i++) {
      var c = text.charCodeAt(i);
      if (c >= 0xd800 && c <= 0xdfff) return false; // surrogate pair: emoji, rare CJK
      if (c < 0x1100) continue;
      if (c <= 0x115f || (c >= 0x2e80 && c <= 0xa4cf) || (c >= 0xac00 && c <= 0xd7a3) ||
          (c >= 0xf900 && c <= 0xfaff) || (c >= 0xfe30 && c <= 0xfe4f) ||
          (c >= 0xff00 && c <= 0xff60) || (c >= 0xffe0 && c <= 0xffe6)) {
        return false;
      }
    }
    return true;
  }

  // deleteSelectedRange removes the selected characters from the terminal's
  // input line: the cursor is walked to the selection when it is not already
  // there, then Backspace (delete behind the cursor) or Delete (delete ahead of
  // it) removes exactly the selected characters.
  //
  // In both planes #rows holds the whole grid model and the cursor indexes it,
  // so the cursor's row element is looked up the same way; the blocks plane
  // merely HIDES the rows above the band with CSS.
  function deleteSelectedRange(range) {
    var row = cursor && rowsEl ? rowsEl.children[cursor.r] : null;
    if (!row) return;
    var rects = range.getClientRects ? range.getClientRects() : null;
    if (!rects || !rects.length) return;
    var box = row.getBoundingClientRect();
    for (var i = 0; i < rects.length; i++) {
      // Anything reaching outside the cursor's row is output (or another input
      // line), not this row's editable text.
      if (rects[i].top < box.top - 0.5 || rects[i].bottom > box.bottom + 0.5) return;
    }
    var chars = String(range).length;
    if (!chars) return;
    var startCol = columnAt(box, rects[0].left);
    var endCol = columnAt(box, rects[rects.length - 1].right);
    if (endCol === cursor.c) {      // selection ends at the cursor: cut behind it
      sendKeyBurst("Backspace", chars);
      return;
    }
    if (startCol === cursor.c) {    // selection starts at the cursor: cut ahead of it
      sendKeyBurst("Delete", chars);
      return;
    }
    // Walk the cursor to the selection's end. Columns and characters agree only
    // on a single-width line, so a line holding wide glyphs is left to the two
    // exact cases above instead of deleting the wrong characters.
    if (!singleWidth(row.textContent)) return;
    var delta = endCol - cursor.c;
    sendKeyBurst(delta > 0 ? "ArrowRight" : "ArrowLeft", Math.abs(delta));
    sendKeyBurst("Backspace", chars);
  }

  // A clipboard chord resolves one of three ways, and the difference is what
  // keeps the engine's own bindings intact:
  //
  //   CHORD_DONE    the page performed it (or deliberately swallowed it)
  //   CHORD_BROWSER the browser keeps it — nothing sent, nothing prevented, so
  //                 its own paste path can still fire where it has one
  //   CHORD_KEY     not a clipboard chord: it goes to the engine as a keystroke
  var CHORD_DONE = "done";
  var CHORD_BROWSER = "browser";
  var CHORD_KEY = "key";

  // clipboardChord performs a clipboard chord and says how it was resolved.
  // Ctrl/Cmd+C counts as a copy only with a live selection: with no selection
  // Ctrl+C is the terminal's interrupt and must reach the engine (and Meta+C has
  // nothing to copy, so it stays the browser's).
  function clipboardChord(ev) {
    if (!ev.ctrlKey && !ev.metaKey) return CHORD_KEY;
    if (ev.altKey || ev.shiftKey) return CHORD_KEY;
    var k = (ev.key || "").toLowerCase();
    if (!CLIPBOARD_CHORDS[k]) return CHORD_KEY;
    if (k === "c") {
      if (!hasSelection()) {
        // With the paste target focused the terminal is not the keyboard target,
        // so a Ctrl+C in that instant is not the engine's interrupt either.
        return document.activeElement === pasteTarget ? CHORD_BROWSER : CHORD_KEY;
      }
      copySelection();
      return CHORD_DONE;
    }
    if (k === "x") {
      if (readOnly) return CHORD_DONE; // a viewer never edits the session
      if (hasSelection()) cutSelection();
      return CHORD_DONE;
    }
    if (readOnly) return CHORD_DONE;
    // Paste is the browser's: it is the only one that can reach the clipboard
    // without a permission prompt, and the paste handler below turns what it
    // delivers into terminal input. beginPaste also covers the case where no
    // paste arrives at all.
    beginPaste();
    return CHORD_BROWSER;
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

  // onPaste turns a `paste` event into terminal input. It is the page's primary
  // paste path: the chord focuses #paste-target, so the browser pastes into an
  // editable element and delivers the clipboard here — no permission needed. The
  // event is always prevented, so nothing ever lands in the textarea.
  //
  // Clipboard text is inserted as-is (the editor applies its own paste handling —
  // markers, normalization — exactly as for a terminal paste). An image has no
  // textual form, so it is uploaded and its stored path inserted: the same text
  // the terminal editor inserts for a clipboard image.
  document.addEventListener("paste", function (ev) {
    pasteHandled = true;
    focusTerminal();
    if (readOnly) {
      ev.preventDefault();
      return;
    }
    var data = ev.clipboardData || window.clipboardData;
    if (!data) {
      ev.preventDefault();
      return;
    }
    var image = imageItem(data);
    if (image) {
      ev.preventDefault();
      uploadImage(image).then(function (path) {
        if (path) send({ t: "input", data: path });
      }).catch(function () { setStatus("error", "image upload failed"); });
      return;
    }
    // Always prevent the default: the paste target must stay empty, and what the
    // terminal inserts is decided here, not by the browser.
    ev.preventDefault();
    var text = data.getData("text");
    if (!text) return;
    send({ t: "input", data: text });
  });

  // A resize burst is debounced (spec §14.6): the cell metrics are re-measured
  // immediately (the layout is already changing), but the geometry message the
  // server turns into a full repaint waits for the drag to settle. In the
  // blocks plane the conversation reflows by itself — the geometry only drives
  // the band and the engine's layout — so the page never repaints history on
  // resize.
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
