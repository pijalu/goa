<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<!-- Copyright (C) 2026 Pierre Poissinger -->

# Archived: Web UI — rendering fixes (scroll / cursor / resize / clipboard) and the O(history) frame cost

Closed 2026-10-04. Moved from `bugs.md` (entries 1–5: "scrolling does nothing
and mixes two layers", "the cursor is missing", "resizing does not work well",
"cut/copy/paste misbehaves", "complete performance assessment; push drawing
complexity to the client").

The full measurement record, the design and the numbers live in
[`docs/webui-perf-assessment.md`](../../webui-perf-assessment.md). This entry
records what changed and how it was verified.

## 1–4. Scrolling, caret, resize, clipboard

Redesigned around ONE scroll container (`#screen`) holding the transcript above
the live grid, so the browser owns scrolling and the wheel reaches the content:

* `#scrollback` and `#grid` were both `position:absolute; inset:0` overlays with
  the grid painting on top and the scrollback `pointer-events:none` — the wheel
  never reached it and dragging its scrollbar revealed the transcript *behind*
  the grid ("text all over"). Now: one scroller, transcript in flow above the
  grid, grid bottom-anchored with `margin-top:auto` (not `justify-content:
  flex-end`, which clips the scroll origin and makes the top unreachable).
* The caret was permanently hidden: frames carried `cur.v=false` from the first
  frame because DECTCEM (`\x1b[?25h` / `\x1b[?25l`) was not modelled. Now
  `TermEmulator` owns the mode (`CursorVisible`), the grid reads it, and a
  visibility toggle marks the screen dirty so it ships on a frame.
* Resize only ever appended rows, so a shrink kept stale rows that inflated the
  scroll height and pushed the input off-screen. `resizeRows` now trims, and the
  resize is debounced 120 ms.
* Clipboard chords were claimed and `preventDefault`-ed, which suppressed the
  very `paste` event the page relies on and blocked native copy. `Ctrl/Cmd+C|V|X`
  now stay the browser's (`Ctrl+C` only with a live selection).
* `white-space: pre` moved from `#grid` to `.row`: on the container it rendered
  the HTML formatting whitespace as ~5 phantom blank lines.

## 5. The performance assessment

Measured first, then fixed. Two defects accounted for "goa server uses
significant CPU", and both were invisible on a fresh session:

* **`TermEmulator.ScrollbackCells()` deep-copied the whole transcript** and
  `CellGrid.TakeScrollback()` called it once per frame to slice off the handful
  of new rows. Benchmarks (`internal/webui/scrollback_cost_test.go`):

  | history | before | after |
  |---|---|---|
  | 1 000 rows | 676 µs / 9.1 MB | 18 µs / 3.5 KB |
  | 5 000 rows | 3.18 ms / 47 MB | 26 µs / 6.8 KB |
  | 20 000 rows | 12.5 ms / 190 MB | 28 µs / 7.1 KB |

  At 20 000 rows and 30 fps that was ~380 ms of CPU and ~5.7 GB/s of allocation
  for nothing. The transcript is now a bounded ring with an absolute base index,
  read incrementally, which also bounds server memory (it used to grow with the
  session at ~9.4 KB per retained row).
* **The client's transcript was unbounded too.** Measured in Chrome: 1.23 ms per
  appending frame at 2 000 rows, 1.80 ms at 10 080, growing with the session.
  The transcript is now bounded at 2 000 rows — the way a terminal bounds its
  scrollback — with the scroll offset compensated when the oldest rows fall off,
  plus native `content-visibility: auto` for the rows scrolled out of view.
  Verified in a real browser: the DOM stayed at exactly 2 000 rows and ~12 000
  nodes while ~3 500 rows streamed through.
* Two smaller ones: `Hub.Publish` encoded each frame **once per client** (now
  once per fan-out, shared bytes), and frames were built even with **no browser
  attached** (`FrameSink.HasClients()` now lets the terminal drop the dirty marks
  instead of diffing, collapsing and encoding a screen nobody receives).

The engine's own compositor is the server's real cost centre (~40 % of samples
under load) but it is shared with the terminal path and is what *produces* the
screen; the web layer's job is to not add to it.

## Verification

* Benchmarks flat in history length (they are the regression detector).
* `TestTakeScrollback*` (incremental, absolute indices, eviction, clear,
  allocation budget), `TestHubEncodesFrameOncePerFanOut`,
  `TestPublishWithNoClientsStillServesTheNextAttach`,
  `TestClientJS_Transcript*` (bound, drop order, scroll compensation,
  follow-tail stays armed), `TestClientJS_FollowTailIsCoalescedPerFrame`.
* Real Chrome via `agent-browser`: grid height `== 16 + rows*16` (the
  phantom-line fix), caret visible and positioned, one scroller with truthful
  geometry, transcript bounded and stable, follow-tail armed after trims,
  renderer ~4 % of a core while streaming and ~0.4 % idle.

## Two bugs found only in the real browser (both regression-tested)

1. Trimming the transcript compensated the scroll offset even while the view was
   pinned to the tail, leaving it a batch short of the bottom — the next scroll
   event read that as "the user scrolled away", so follow-tail detached and the
   view drifted to the top of the transcript. Fixed by compensating only when the
   view is NOT following. `TestClientJS_TranscriptTrimKeepsFollowTailArmed` was
   verified to fail without the fix.
2. The goja DOM stub did not clamp `scrollTop`, so a page assigning
   `scrollHeight` landed past the bottom and "is the view at the bottom" was a
   fiction. The stub now clamps like the real property.

## Not addressed (out of scope, noted)

* `goa server` does not exit cleanly on `/quit`, SIGINT or SIGTERM: the process
  lingers after the HTTP listener stops, and the profiling flags never write
  their files. Pre-existing, unrelated to this work, worth its own entry.
* `internal/agentic/provider/models/api.json` shows as modified — pre-existing
  and unrelated.
