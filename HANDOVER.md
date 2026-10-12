# Handover — Web UI rendering + performance (assessment, fixes, validation)

**Date:** 2026-10-04
**Branch:** `feature/webui`
**State:** work complete and committed. `bugs.md` is empty (entries 1–5 archived).

---

## 1. What was asked

Resume the previous session's handover (web UI scroll / cursor / resize /
clipboard) and, on top of it:

* the web version must use the **native web** approach for history and scrolling;
* **drawing should be pushed to the browser** as far as possible, to avoid load
  on the server;
* do an **assessment**, not a simple fix — the design may change.

## 2. What was found (measured, not guessed)

Full record: [`docs/webui-perf-assessment.md`](docs/webui-perf-assessment.md).

1. **`CellGrid.TakeScrollback` was O(history) per frame.** `ScrollbackCells()`
   deep-copied the whole transcript and the caller used one frame's worth of it.
   Measured: 12.5 ms and 190 MB **per frame** at 20 000 rows of history — ~380 ms
   of CPU and ~5.7 GB/s of allocation at 30 fps. Invisible on a fresh session,
   which is why the first profile looked innocent.
2. **The transcript was unbounded on both sides** — the emulator's slice grew
   with the session (~9.4 KB per retained row) and so did the client's DOM.
3. **Client transcript layout was linear and forced on every appending frame**:
   1.23 ms/frame at 2 000 rows, 1.80 ms at 10 080.
4. **`Hub.Publish` encoded each frame once per client**, so server cost scaled
   with the number of viewers.
5. **Frames were built with no browser attached** (diffed, collapsed, encoded,
   then dropped by an empty hub).
6. The TUI compositor is the server's real cost centre (~40 % of samples under
   load) but it is shared with the terminal path and is what *produces* the
   screen — the web layer's job is to not add to it.
7. **(Second pass) The dirty-mark backlog.** `CellGrid.pending` was a *list of
   every row mark* taken since the last frame, appended once per `Process` call
   and sorted/deduplicated at the next `Patches`. Patches only runs when a
   browser is attached, so the first frame after a burst of output paid for the
   whole burst: 157 µs / 0.58 MB at 2 000 rows, 281 µs / 1.70 MB at 20 000,
   **2.46 ms / 18.8 MB at 200 000** — for a screen holding at most 200 distinct
   rows. `Process` alone measured flat (41 875 B / 362 allocs at every size),
   which localised the growth to the pending set, not the emulator. It is also
   why `BenchmarkPublishCost` looked non-flat after the first pass.

## 3. What changed

**Server** (`b2c8dbd0`): bounded transcript ring in `TermEmulator` with an
absolute base index, read incrementally (`ScrollbackLen`/`ScrollbackRow`);
`TakeScrollback` reads only new rows; the hub encodes once per fan-out and hands
every client the same `Payload`; `FrameSink.HasClients()` skips frame production
when nobody is watching. Benchmarks flat in history length.

**Client** (`cbbad1d4`): transcript bounded at 2 000 rows (oldest fall off in
batches, scroll offset compensated); `content-visibility: auto` on transcript
rows; follow-tail coalesced into one `requestAnimationFrame`. Scrolling stays
entirely the browser's — no spacer, no script-driven scroll correction.

**e2e** (`32971684`): a model-independent "page mechanics" section asserting the
scroll container, caret, follow-tail, transcript bound, resize and clipboard
chords.

**Server, second pass** (this revision): `CellGrid.pending` is a **set with one
flag per screen row** instead of a list of every mark, so a frame costs one pass
over the screen whatever arrived since the last one and its memory is bounded by
the geometry. `Resize` re-sizes the set and marks every row (a geometry change is
a repaint), so no row of an old screen can be shipped against a new one; the now
dead `normalizeRows` (map + sort) was deleted. `scrollback_cost_test.go` gains
`TestFrameCostIsBoundedByTheScreen` (verified RED against the old code: "a 100-row
burst left 2479 pending entries") and `TestResizeRebasesTheDirtySet`, and both
benchmarks now run up to 200 000 rows.

## 4. Verification (be precise)

* Go benchmarks flat in history: 12.5 ms / 190 MB → ~22 µs / 47 KB per frame at
  20 000 rows; `BenchmarkPublishCost` 430 678 B / 541 allocs at 1 000 rows vs
  430 740 B / 543 allocs at 20 000 (was 508 140 → 1 698 846 B).
* New Go tests: transcript contract (incremental, absolute indices, eviction,
  clear, allocation budget), hub encodes once, no-client skip still serves the
  next attach.
* New goja tests: transcript bound, drop order, scroll compensation, follow-tail
  stays armed across trims, coalescing.
* **Real Chrome** (`agent-browser`, real `/help` output through the real engine):
  transcript held at exactly 2 000 rows and ~12 000 nodes while ~3 500 rows
  streamed through; follow-tail armed after trims; `scrollHeight − clientHeight`
  exactly the transcript + grid height; `grid.offsetHeight == 16 + rows*16`;
  caret visible and positioned; renderer ~4 % of a core while streaming, ~0.4 %
  idle.
* e2e mechanics section: extracted verbatim and executed — 8/8 PASS; re-run on the
  second-pass server **10/10 PASS** (added `transcript_order`: transcript rows keep
  ascending absolute indices across grow+shrink cycles).
* Dirty-set regression tests verified the honest way: the fix was reverted and
  `TestFrameCostIsBoundedByTheScreen` failed against HEAD (`a 100-row burst left
  2479 pending entries`), then passed with the fix. A temporary diagnostic
  benchmark (deleted) isolated `Process` alone as flat in history, which is what
  pointed at the pending set.
* Gate: `go vet ./...` clean; `staticcheck ./...` clean except two pre-existing,
  unrelated findings in `plugins/` (`S1021` in `bridge_extended_surface.go:117`,
  `U1000` in `vm_frame.go:95`); `gocognit -over 15` and `gocyclo -over 12` clean;
  `go test -count=1 -race -cover ./...` green; webui 90.6 %, tui 76.0 %.

## 5. Decisions worth keeping

* **The transcript is bounded, not windowed behind a spacer.** A spacer window
  was built and measured first; the real-browser pass showed it makes the
  scrollbar promise rows that cannot be shown and lets content be evicted out
  from under a view parked at the top. Bounding is the same O(1) cost with the
  browser owning the whole interaction.
* **Trim compensates the scroll offset only when NOT following.** Compensating
  while following leaves the view a batch short of the bottom; the next scroll
  event reads that as "the user scrolled away" and detaches follow-tail. This was
  found only in a real browser and is now a regression test.
* **Row patching was left alone.** Measured 0.0018 ms/row to rebuild vs
  0.0008 ms to reuse — ~0.006 % of a core at 2 rows/frame. Not worth the
  complexity; the measurement is recorded in the assessment.

* **The dirty set is a set, not a log.** Bounded by the screen, re-sized on
  resize: the alternative (draining marks eagerly, e.g. in `DiscardChanges`) is
  what the no-client path already does per frame, and a log makes the *next*
  frame's cost depend on how long nobody was watching.

## 6. Open / not addressed

* **`goa server` does not exit cleanly** on SIGINT or SIGTERM: `runWebServer`
  builds the session with `signal.NotifyContext` then blocks in `New(subs).Run()`,
  which never observes that context, so the listener stops but the process stays
  up and the profiling flags never write their files. Reproduced on the binary
  built for the browser pass (`kill -INT` → `kill -0` still true after 5 s);
  recorded in `docs/webui-perf-assessment.md` §5.1. It is why no end-to-end pprof
  capture of the fixed server exists (the benchmark covers that path instead).
  Pre-existing, in the session lifecycle rather than the web layer, and left
  unfixed: it is a different defect class from the per-frame cost this work
  targets.
* Overscroll → browser-history fallback (needs a trackpad swipe) and real
  clipboard flavours (rich/HTML paste) are still hand-checked only.
* `internal/agentic/provider/models/api.json` shows as modified — pre-existing
  and unrelated.

## 7. Gotchas

* Assets are `//go:embed`-ed: **any** change to `index.html`/`app.css`/`app.js`
  needs a `go build` + server restart before browser validation.
* The goja harness only sees `app.js`; CSS/layout bugs are invisible to it — use
  the real browser. Its `El` stub now clamps `scrollTop` like the real property.
* `agent-browser eval` returns the **expression's value**: pass an IIFE
  (`(function(){…})()`), not a bare function expression, or you get `{}`. The
  result also comes back JSON-encoded, which is why the e2e normalises with
  `has`/`jnum`.
* `agent-browser eval` with a multi-line argument does not survive every shell
  wrapper — put multi-line JS in a file and pass `"$(cat file)"`.
