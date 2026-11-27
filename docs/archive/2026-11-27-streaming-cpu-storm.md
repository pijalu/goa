# Streaming render CPU storm — fixed

**Status:** fixed 2026-11-27
**Area:** `internal/ansi`, `tui` (streaming markdown + viewport render), `internal/app/perf_load.go`

## Excessive CPU usage during streaming

**Observed:** `goa` CPU usage while streaming is much higher than expected —
over 20% of a core sustained during a response, even on small conversations.
Usage CLIMBS once the streamed answer grows past the visible screen / buffer
(i.e. as rows spill into scrollback) and stays high for the rest of the turn.
**Expected:** streaming should be dominated by network wait; per-chunk render
work should stay well under a few percent (incremental diff path, no
full-transcript recompose per chunk).

### Profiling result (measured, not guessed)

CPU profile of a simulated stream (`tui` benchmark harness: fill N history
entries, then one `RenderNow()` per streamed chunk — the synchronous worst case
the app hits per dirty frame):

| answer size (chunks) | ns/chunk |
|---|---|
| 5   | 1.23 ms |
| 50  | 1.30 ms |
| 200 | 2.31 ms |
| 600 | 4.98 ms |

Linear in ANSWER length (flat in history length) — exactly the reported "climbs
once the answer grows past the screen". Cumulative profile of the 4.6 ms/chunk
case:

- `ansi.Width` **53%** — of which `uniseg.(*Graphemes).Next` 38% (full
  sentence/word/line-break segmentation just to measure a width) plus
  `ansi.Strip` 13% (regexp per line).
- `assistantMessage.renderFrame` **40%** — `m.incr.Render(m.text)` then
  `padToWidth(" "+line, width)` over EVERY line of the whole answer, every
  frame: O(answer) `ansi.Width` calls per chunk → O(n²) over a turn.

The markdown re-parse itself is already incremental (`IncrementalMDRenderer`),
so the O(len) per-chunk cost is the per-line PADDING (width measurement) of the
entire message, not the parse.

### Fix plan

1. **`ansi.Width` fast path** (`internal/ansi`): single-pass scan that counts
   printable ASCII runes while skipping CSI/OSC/2-byte escapes; when only those
   were seen, return the count without `Strip` + `uniseg` segmentation (ASCII
   glyphs are all single-rune clusters of width 1). Tabs, other control bytes,
   non-ASCII runes and unknown escapes fall back to the existing
   grapheme-cluster path — identical results, no behaviour change.
   Test: table-driven equivalence against the slow path for ASCII, styled,
   tab, wide/emoji, ZWJ, combining-mark and RI-flag rows + a benchmark
   asserting the ASCII path does not allocate.
2. **Incremental line padding** (`tui`): memoize the transformed (padded)
   form of the renderer's STABLE markdown prefix, so a chunk only pads the
   newly stabilized lines plus the open tail instead of the whole answer.
   `IncrementalMDRenderer` exposes how many leading rendered lines are stable;
   a small reusable `lineTransformCache` (SRP: source-prefix memoization, no
   knowledge of markdown) reuses them keyed by width. Cache is dropped when the
   width changes or the text stops being an append (the incremental renderer
   already resets its prefix in that case).
   Test: render the same document chunk-by-chunk and assert byte-identical
   output to a one-shot render at every step, plus an allocation/CPU check that
   per-chunk cost stays flat as the answer grows.

### Validation steps

- `go test -count=1 -race ./tui ./internal/ansi` (identical-render regression
  tests must fail before the fix and pass after).
- Re-run the profiling benchmark: per-chunk cost must be flat across
  answer sizes 5…600 chunks (target: no growth trend, ~<2 ms/chunk).
- Filmstrip/e2e: stream a long answer in a real `goa` session and confirm the
  terminal output is unchanged (rows, wrapping, colors, scrollback).
- Full gate: `go vet ./...`, `staticcheck ./...`, `gocognit -over 15 .`,
  `gocyclo -over 12 .`, `go test -count=1 -race -cover ./...`.

### Result (measured after the fix)

| shape | before | after |
|---|---|---|
| PTY `--perf-load` (growing stream, 30 s) | **47.6 %** CPU | **12.2 %** CPU |
| benchmark, 600-chunk answer | 4.98 ms/chunk | 0.50 ms/chunk |
| benchmark, 500-line single paragraph | 4.98 ms/chunk | 0.43 ms/chunk |

Per-chunk cost is now flat in answer size (0.38–0.50 ms across 5…600 chunks,
previously 1.23 → 4.98 ms). Both PTY runs emitted the same 1563 distinct
streamed lines, so the terminal output is unchanged — only the work is gone.

The measurement harness also had to be made faithful: `--perf-load` used to
REPLACE one line every tick, which cannot exhibit growth at all. It now appends
to a single assistant message (`--perf-load-grow=false` restores the old
single-line shape), i.e. it reproduces the reported scenario end to end.

Three distinct O(answer)-per-chunk costs were found and fixed:

1. `ansi.Width` — full `uniseg` sentence/word/line-break segmentation (plus a
   regexp `Strip`) to measure one row's width. Now a single-pass ASCII + escape
   scan with the cluster path as fallback: 0 allocations, ~80 ns/row.
2. Per-line padding of the WHOLE answer every chunk (`padToWidth` → `Width`).
   Now memoized over the renderer's stable prefix (`lineTransformCache`).
3. Markdown re-parse of the whole answer when it has no blank lines (one open
   paragraph). The incremental renderer now also treats a finished paragraph
   line as an append-stable cut point (paragraph lines render independently with
   hard breaks), while lines inside fences/lists/quotes/tables never cut.
4. Bonus constant factors on the same path: `HexToRGB` no longer uses
   `fmt.Sscanf`, `Fg`/`Bg`/`FgRGB`/`BgRGB` build sequences without `fmt` and
   memoize per hex colour.
