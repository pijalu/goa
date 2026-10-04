<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# B4 — Web UI: input line / bottom status bar not rendered correctly

Closed 2026-10-04. Archived from `bugs.md`.

## Reported

The bottom of the page (input line, separators, status and model lines) was not
rendered correctly in the browser. The DOM measurement in the report showed the
cells themselves were laid out cleanly at 1280×900 (`#screen` clientHeight 876,
all rows 16 px, no overlap, no horizontal overflow), and the screenshot structure
showed one separator rule and a gap before the status line with no model line in
the captured area — i.e. the footer band was below the fold, not mis-measured.

Expected: the footer renders exactly like the terminal's — input row(s),
separator, status row with the right-aligned mode, model line — all inside the
viewport, one row per line, never clipped or overlapping.

## Root cause

The same startup-canvas overflow as B3. Before the B3 fix the startup banner was
emitted as boxed `AddSystemMessage` panels (three rows per entry, ~21 rows for
five to seven entries), so the startup canvas was taller than a small browser's
grid. The compositor bottom-anchors the window and scrolls the overflow off the
top; in the web path those displaced rows are shipped to the page as transcript
and drawn **above** the live grid. The transcript then made `#screen` taller than
its viewport and the grid (carrying the input/status/model rows) was pushed below
the fold — the "footer rows falling outside the visible area" symptom.

B3's fix — the startup banner as single-row info entries
(`addStartupInfo` → `ChatViewport.AddInfoMessage`, commit `08e0f320`) — drops the
startup canvas to ~24 rows, so it fits the browser grid, no rows are shipped as
transcript on load, and the footer band sits at the bottom of the viewport. B3's
geometry-change history suppression (`CellGrid.Resize` →
`TermEmulator.EraseScrollback` + `geometryChange`) removes the other way rows
could be manufactured into the transcript by the browser's first resize.

There is no residual web-specific geometry defect: the grid the browser receives
is, at every measured geometry, exactly the terminal's own screen.

## Measurements

Ground truth: the TUI rendered into a byte-capturing terminal, its stream replayed
through `tui.TermEmulator` (what an `e2e/ptydrive --log` PTY capture yields on
replay), against the same component tree rendered onto `webui.VirtualTerminal`
(the web grid) — started at the server default 120×40 and resized to the client
geometry, which is the page's connect flow. Row-for-row equality holds at:

`158×53, 120×37, 117×35, 100×35, 120×30, 100×24, 80×20, 60×12, 40×10, 30×6`

(fresh and post-resize), and the same hold against a **real PTY capture** of the
built `goa` binary (run with `pty.StartWithSize`, replayed through
`tui.TermEmulator`) at 120×30 and 100×24: the bottom band sits on the same rows
and the two separator rules are byte-identical. Pinned by
`internal/app/webui_b4_footer_test.go`:
- `TestWebUI_BottomBandMatchesTerminal` — web grid == terminal screen, every row.
- `TestWebUI_BottomBandMatchesPTY` — the real TUI captured in a PTY, replayed,
  band-for-band equal to the web grid.
- `TestWebUI_BottomBandIsInsideTheGrid` — the band is *present* and ordered:
  input row with the typed text, full-width separator, full-width separator,
  status row ending with the right-aligned upper-cased mode badge, model line as
  the last row.

Real browser (Chrome via `agent-browser`), fresh load at three geometries, DOM
geometry audit (all rows inside `#screen`, grid never crossing into `#status`):

| viewport | rows | `#rows` height | expect `16+rows*16` | all rows inside | overlaps `#status` | last rows |
|---|---|---|---|---|---|---|
| 1280×633 | 37 | 608 | 608 | yes | no | sep / input / sep / status(YOLO) / model |
| 1100×480 | 27 | 448 | 448 | yes | no | sep / input / sep / status(YOLO) / model |
| 900×320 | 17 | 288 | 288 | yes | no | sep / input / sep / status(YOLO) / model |

Screenshots: [`b4-webui/1280x633.png`](b4-webui/1280x633.png),
[`b4-webui/1100x480.png`](b4-webui/1100x480.png),
[`b4-webui/900x320.png`](b4-webui/900x320.png). (Headless Chrome caps the
viewport at 633 px high; the reported 1000×600 was also audited at 35 rows with
the same invariants before the screenshots.)

## RED/GREEN

- Sensitivity: temporarily forcing the web geometry off by one row
  (`VirtualTerminal.Size()` returning `height-1`) makes both
  `TestWebUI_BottomBandMatchesTerminal` and `TestWebUI_BottomBandMatchesPTY` fail
  (the band shifts by a row); reverting restores green. The tests detect exactly
  the footer-band regression class, against both the harness terminal and a real
  PTY.
- The browser `footer_band` check added to `e2e/w1_webui_browser.sh` asserts all
  grid rows are inside `#screen`, the grid never crosses into `#status`, and the
  model line is the last row.

## Residual risks

- On displays shorter than the chrome band needs (the client clamps the grid to 6
  rows), neither the terminal nor the web page can show the whole footer; both
  produce the same (degraded) screen, which is the compositor's small-height
  behaviour, not a web divergence. No web-specific action.
- The page's `--status-h` (the 24 px connection bar) is a fixed value; if that
  bar's height ever changes, `#screen`'s bottom inset must change with it. Not
  reachable today (the bar is fixed at 24 px, single line).
