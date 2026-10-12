<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<!-- Copyright (C) 2026 Pierre Poissinger -->

# Archived: Web UI — startup screen corrupted on load (mascot cut, fragment in transcript)

Closed 2026-10-04. Moved from `bugs.md` (B3).

## B3 — Web UI: startup screen corrupted on load (mascot cut, fragment in transcript)

**Observed.** On first load at a geometry other than the server's initial
120×40, the screen is already corrupted: the header/mascot is missing its top
rows (the visible grid starts mid-art) and those rows are rendered as
transcript/history above the live grid, i.e. part of the mascot is split out and
drawn next to/under the plugin card. Reproduced twice (fresh server each time,
browser at 1000×600 → grid 35 rows):

```
rows 35  sbChildren 2   # 2 rows of the mascot landed in the transcript
grid row 0: " ▄ ▄▄▄ ▄       ▄  ▄       ▄  ▄▄      ████ ..."   <- starts at mascot line 3
```

Cause of the split: the client's first `{t:"resize"}` makes `CellGrid.Resize`
shrink the emulator, which pushes the top rows into the emulator's scrollback
(a real terminal does the same), and `VirtualTerminal.Resize` ships them to the
browser as transcript rows before the engine's geometry-repaint lands. Rows that
are part of the *current* screen must never be shipped as history.

**Expected.** After the first resize settles, the screen is exactly what a fresh
session started at that geometry shows: full header/mascot in the grid, no
startup rows duplicated into the transcript.

**Plan.**
- Make a geometry change not manufacture history: on `CellGrid.Resize` (shrink),
  drop the rows that fall off the top instead of retaining them as scrollback, or
  clamp `sentScrollback` so they are never shipped — whichever keeps the terminal
  contract for the *terminal* path intact (the TUI's own path must not change).
- Test approach: app-level test with `webui.VirtualTerminal` — start the session
  at 100×30, render, `vt.Resize(90,24)` (shrink) and `vt.Resize(140,40)` (grow),
  render, then assert the grid's text equals a fresh session's screen at the same
  geometry and that `vt.Grid()`'s scrollback holds no row equal to a grid row.
- Validation: browser load at 3 geometries (small/medium/large), screenshot +
  DOM dump: mascot complete, transcript empty after load; e2e mechanics still
  green.

**Status (partially fixed).** The geometry change no longer manufactures
history: `CellGrid.Resize` now drops the emulator's retained scrollback
(`TermEmulator.EraseScrollback`), re-bases the shipped mark, and suppresses the
scroll overflow of the repaint that re-anchors the screen (the rows it pushes
off belong to the screen being replaced). Implementation + tests:
`internal/app/webui_b3_geometry_test.go` (`TestWebUI_StartupScreenSurvivesGeometryChange`,
`TestWebUI_TranscriptRowsAreNotOnScreen`). Verified RED before the fix (the
shrink shipped a mascot row) and green after, including the whole
`./internal/app/ ./internal/webui/ ./tui/` suite.

**Remaining (measured, corrected cause).** The reported 35-row browser still
shows the grid starting mid-mascot, and this is NOT caused by the resize: the
startup screen's canvas is taller than the terminal (measured 37 rows of content
on a 35-row browser; header 10-12 + the boxed startup info panels ~21 + chrome
6), so the compositor bottom-anchors the window and scrolls the header's top
rows into the emulator's scrollback. A session *started* at that geometry
(`GOA_B3_GEOM=117x35` probe) does the same, so the grid equals a fresh
session's screen — the remaining defect is that the startup screen does not fit.
Fixing it means either (a) the startup banner must fit the screen (single-line
info entries via `ChatViewport.AddInfoMessage` instead of 3-row boxed
`AddSystemMessage` panels), or (b) the compositor must pin the header band so
the transcript region absorbs the overflow (a TUI change affecting the terminal
path too). (a) is small; (b) is the general fix. Decide, then finish the browser
validation at 3 geometries.


## What was actually wrong (measured)

The reported cause ("the first resize shrinks the emulator and ships the rows it
drops as transcript") is not what happens: `TermEmulator.shrinkRows` drops the
bottom rows and never touches the scrollback. Two separate defects:

1. **The geometry change manufactured history.** The resize clears nothing: the
   emulator kept the scrollback recorded at the old geometry, and the
   compositor's re-anchoring repaint pushed the *top rows of the screen being
   replaced* off the top; `VirtualTerminal.publish` shipped them to the page as
   transcript rows. Measured with an instrumented server: at the browser's first
   resize (`GRIDRESIZE 120x40 -> 116x35`) the following frame carried
   `scrollback=2` — the mascot's first two rows, drawn above a live grid that
   started at mascot line 3.
2. **The startup screen did not fit.** The startup banner was emitted with
   `ChatViewport.AddSystemMessage`, which draws a three-row bordered panel per
   line. Five to seven banner entries therefore took ~21 rows, so the startup
   canvas was 37 rows on the reported 35-row browser (header 10-12 + banner ~21
   + chrome 6) and the compositor bottom-anchored the window. A session *started*
   at that geometry behaved identically (probe: `GOA_B3_GEOM=117x35` at the
   server start), which is what identified the fit — not the resize — as the
   remaining cause.

## Fix

* `TermEmulator.EraseScrollback()` — the CSI 3J wipe as a callable method.
* `CellGrid.Resize` — a geometry change drops the retained history, re-bases the
  shipped-scrollback mark, and suppresses the scroll overflow of the repaint that
  re-anchors the screen.
* `CellGrid.TakeScrollback` — drops (never ships) rows pushed while that
  suppression is armed.
* `VirtualTerminal.WriteString` — ends the suppression on the first write that
  changed the screen: that write *is* the re-anchoring repaint.
* `internal/app/prompt.go` — the startup banner goes through
  `addStartupInfo`/`ChatViewport.AddInfoMessage` (one row per entry, no border),
  so the startup screen fits a 24-row window instead of needing 37.

## Verification

* `internal/app/webui_b3_geometry_test.go`:
  `TestWebUI_StartupScreenSurvivesGeometryChange` (100x30 → 90x24 → 140x40 →
  100x30 equals a fresh session's screen at each geometry, whole mascot, zero
  shipped transcript rows) and `TestWebUI_TranscriptRowsAreNotOnScreen` (every
  shipped transcript row is off-screen at the moment it ships).
* Verified RED before each part: with the shipping fix stashed the shrink
  shipped a mascot row; with `addStartupInfo` temporarily emitting the boxed
  panel the grid started at mascot line 5 at 100x30.
* Real browser (agent-browser, rebuilt goa): 1000x600 (35 rows) and 1280x633
  (37 rows, the headless display caps larger viewports) both show grid row 0 =
  the mascot's first row and `#scrollback` = 0 rows after load.
* `go test -count=1 -timeout 180s ./internal/app/ ./internal/webui/ ./tui/`
  green; `go vet` clean; `gocognit -over 15` / `gocyclo -over 12` clean for the
  touched files.
