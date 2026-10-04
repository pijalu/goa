# Web UI rendering — measured assessment and design

**Date:** 2026-10-04
**Scope:** `goa server` (the browser-is-the-terminal web UI) — CPU cost, scrolling,
history and where the drawing work should live.
**Status:** assessment complete; the fixes it prescribes are tracked in `bugs.md`.

This document is an *engineering record*, not user documentation (lowercase name =
repository metadata, not a `goa://` doc entry).

---

## 1. Method

Everything below is measured, not reasoned about:

* **Server CPU** — `./goa server --perf-load --cpuprofile=…` (the engine's own
  synthetic 60 Hz streaming load), profile read with `go tool pprof -top`.
* **Server per-frame cost vs. history length** — Go benchmarks in
  `internal/webui/scrollback_cost_test.go` (`BenchmarkTakeScrollback`,
  `BenchmarkPublishCost`), which drive a real `CellGrid` through the real
  emulator.
* **Client cost** — a real Chrome driven by `agent-browser`, measuring the exact
  code path `app.js` runs (append transcript rows → follow-tail scroll → forced
  layout) at increasing transcript sizes.
* **Client/server totals** — CPU-time deltas sampled with `ps -o time=`.

The synthetic `--perf-load` never scrolls a line off the screen, which is why the
first profile looked innocent; the benchmarks below were written specifically to
cover what the synthetic load misses. That gap is itself a finding (§3.1).

---

## 2. Measurements

### 2.1 Where the server's CPU goes under streaming load

`goa server --perf-load --perf-load-duration 40s --cpuprofile`, one browser
attached, 2.81 s of samples over 40.4 s (**6.95 % of one core**):

| Share | Cost centre |
|---|---|
| 40.2 % | `tui.(*Compositor).Render` → `Scene.compose`, `placeLayer`, `ansi.Width`, `ansi.Strip` (regexp) |
| ~27 % | allocator/GC (`mallocgc`, `spanInlineMarkBits.init`, `memclrNoHeapPointers`) |
| 6.1 % | `webui.(*VirtualTerminal).WriteString` (emulator re-parse of the compositor's bytes) |
| 4.6 % | `webui.(*CellGrid).Patches` (re-diff, `rowCellsLocked` copies) |
| 3.2 % | `webui.(*wsClient).write` |
| rest | runtime, syscalls, `kevent` |

The dominant cost is the **TUI engine's own compositor**, which the terminal path
pays too. The web layer proper is ~6 %.

### 2.2 Per-frame cost vs. transcript length — the real defect

`BenchmarkTakeScrollback` / `BenchmarkPublishCost` (120×40 grid, Apple M4 Pro):

| Scrollback history | `TakeScrollback` | bytes allocated | whole web frame (parse+diff+runs+transcript) |
|---|---|---|---|
| 0 rows | 15 ns | 0 B | 6.2 µs |
| 100 rows | 86 µs | 0.6 MB | — |
| 1 000 rows | 676 µs | 9.1 MB | 697 µs |
| 5 000 rows | 3.18 ms | 47 MB | — |
| 20 000 rows | **12.5 ms** | **190 MB** | **12.7 ms** |

`TermEmulator.ScrollbackCells()` deep-copies the **entire** transcript
(`out[i] = append([]CellAttrs(nil), row...)` for every row) and
`CellGrid.TakeScrollback()` calls it **once per frame**, only to slice off the
handful of rows that are actually new. The cost is O(history) per frame:

* at 20 000 rows and 30 fps that is **~380 ms of CPU and ~5.7 GB of allocation
  per second** — the server pins a core and feeds the GC for nothing;
* it is invisible on a fresh session (6 µs/frame) and gets worse for the entire
  life of the session. This is the "goa server uses significant CPU" report.

The emulator's transcript is also **unbounded** (`e.scrollback = append(…)`, no
cap), so server memory grows with session length: ~9.4 KB per retained row at
120 columns → ~190 MB at 20 000 rows, per session.

### 2.3 Client cost vs. transcript length

Real Chrome, `app.js`'s own path (append rows → `scrollToBottom()` → forced
layout), measured on the live page:

| Transcript rows in DOM | ms per append+follow-tail frame |
|---|---|
| 2 000 | 1.23 |
| 4 020 | 1.34 |
| 6 040 | 1.51 |
| 8 060 | 1.49 |
| 10 080 | 1.80 |

`#scrollback` only ever grows: every row that leaves the screen is appended and
kept as DOM forever. Layout cost is linear in session length (~0.17 µs/row), and
`scrollToBottom()` in `onFrame`/`onScrollback` reads `scrollHeight`, forcing that
layout **on every frame that appends a row**.

Tested alternatives at 10 000 rows:

| Variant | ms/frame |
|---|---|
| unbounded DOM (today) | 3.81 |
| `content-visibility: auto` on rows (native off-screen skipping) | 1.40 |
| **bounded DOM window + spacer** | **0.33 (constant)** |

`content-visibility: auto` is native and helps 2.7×, but the cost stays linear
(the container still positions every box). A bounded DOM window with a spacer
element holding the evicted rows' height is **O(1)** and keeps `scrollHeight`
truthful, so the native scrollbar, wheel, keyboard and touch scrolling are all
unchanged — the browser still owns scrolling.

Isolated cost of the append path at 10 000 rows (cv:auto active):
append only ≈ 0 ms, append + forced layout 1.36 ms, append + scroll + layout
1.61 ms. **The forced layout is the cost**, not the DOM insertion.

### 2.4 Client/server CPU totals (synthetic 30 fps stream, 1 browser)

| | CPU |
|---|---|
| `goa` process | ~7 % of one core |
| Chrome renderer | ~4.8 % of one core |
| Chrome GPU | ~5 % of one core |

Neither is alarming *for this load* — because the synthetic load never scrolls.
The two O(history) defects above are what make a real long session expensive.

### 2.5 Frame encoding is per client

`Hub.Publish` hands the same `*Frame` to every client; each transport's writer
goroutine calls `codec.EncodeFrame(f)` itself (`transport_ws.go:318`,
`transport_sse.go:217`). With N attached browsers the same screen is
JSON-encoded, and every `wireRun`/`wireRow` re-allocated, **N times per frame**.
The hub is the only place that knows the fan-out width, so it is the only place
that can encode once.

### 2.6 Frames are produced with no browser attached

`VirtualTerminal.publish` always calls `grid.Patches()` and
`grid.TakeScrollback()`; `Hub.Publish` then discards the frame when the client
set is empty. A `goa server` with nobody watching still pays the whole web cost
(§2.2 included) for every engine frame.

---

## 3. Findings, ranked by measured impact

1. **`TakeScrollback` is O(history) per frame** (§2.2). 12.5 ms + 190 MB per
   frame at 20 000 rows. *Critical.*
2. **The transcript is unbounded on both sides** — server (emulator slice) and
   client (DOM) (§2.2, §2.3). Cost and memory grow with session length, forever.
   *High.*
3. **Client transcript layout is linear and forced on every appending frame**
   (§2.3). *High.*
4. **Per-client frame encoding** (§2.5) — server cost scales with the number of
   viewers instead of being constant. *Medium.*
5. **Frames are built with no client attached** (§2.6). *Medium.*
6. **The compositor is the server's real cost centre** (§2.1) but it is the
   engine, shared with the terminal path, and it is what *produces* the screen.
   Moving it is out of scope for the web UI; the web UI's job is to not add to
   it. *Noted, not actioned.*

---

## 4. Design

### 4.1 Principles

* **The server ships what moved; the browser draws.** The wire format already is
  a row delta; the browser already owns the cell model, the runs → DOM mapping,
  the caret and the scrolling. Keep it that way — the fixes below make the
  server stop doing work that is proportional to the *past* and let the browser
  stop doing work proportional to the *past*.
* **Native web for scrolling and history.** A real overflow container, a real
  scrollbar, browser-owned wheel/keyboard/touch/overscroll. No JS-emulated
  scrolling, no `preventDefault` on wheel. `overscroll-behavior: auto` stays, so
  a gesture past either end chains to the browser (history swipe / page).
  Windowing must therefore be *invisible* to the scroll geometry: the spacer
  preserves `scrollHeight`, so the scrollbar never lies.
* **Bounded work per frame.** Every per-frame path must be O(changed), never
  O(session). This is the property that makes a 10-hour session cost the same as
  a 10-second one.

### 4.2 Server

| Change | Why |
|---|---|
| `TermEmulator`: expose incremental transcript access (`ScrollbackLen`, `ScrollbackRow`) instead of a whole-buffer deep copy | kills finding 1 |
| `TermEmulator`: cap the transcript (ring, with an absolute base index) | bounds memory (finding 2) |
| `CellGrid.TakeScrollback`: read only the rows since `sentScrollback`, clamped to the ring base | kills finding 1 |
| Encode each frame **once** in the `Hub`; clients write the shared bytes | kills finding 4 |
| Skip frame production when no client is attached | kills finding 5 |

### 4.3 Client

| Change | Why |
|---|---|
| Transcript data model in JS (run lists) + a **bounded DOM window** + a spacer for evicted rows | kills findings 2, 3 |
| Rehydrate evicted rows from the data model when the user scrolls into the spacer | history keeps working, natively |
| Coalesce follow-tail scrolling into one `requestAnimationFrame`, and only when the content grew | removes the per-frame forced layout |
| Patch rows in place (reuse spans) instead of `textContent=""` + rebuild | removes per-frame DOM churn/GC |

---

## 5. Verification plan

* Go benchmarks `BenchmarkTakeScrollback` / `BenchmarkPublishCost` must be flat
  in history length (they are the regression detector for finding 1).
* Unit tests: ring eviction, absolute-base clamping, resume/attach after
  eviction, one-encode-per-frame (hub), no-client frame skip.
* goja client tests: window bound, spacer height, rehydration, follow-tail
  coalescing, caret position unchanged.
* Real browser (`agent-browser`): scrollbar geometry, wheel + follow-tail
  detach/re-arm, overscroll → browser history, cut/copy/paste, resize.
* `e2e/w1_webui_browser.sh`: add scroll/caret/resize/clipboard assertions so
  these are regression-checked, not hand-checked.
