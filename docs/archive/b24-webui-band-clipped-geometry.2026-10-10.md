<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# B24 — web band: the status bar was cut at the right edge

Closed 2026-10-10. Archived from `bugs.md`.

## Reported

"status bar is cut - current status show: `coding-posture - yolo`", clarified on
follow-up as **the right edge losing characters**: "it does not always fully show —
eg: YOLO was cut in YOL".

## Root cause

The page reports the terminal geometry (`{t:"resize"}`) when the socket opens and on
`window.resize` — and at no other time. The content box that geometry is measured
from (`#screen`) can change **without a window resize**: a classic (width-taking)
scrollbar appears the moment the conversation grows past the viewport and narrows
`#screen` by its width. The server kept the wider grid, the band rows were therefore
wider than the box that displays them, and `.grid { overflow: hidden }` clipped the
right edge — one to a few cells, which is exactly a trailing `YOLO` losing its last
letter. It "did not always" happen because it needs a conversation long enough to
scroll.

Measured on a real page (Chromium via `agent-browser`), narrowing `#screen` from 900px
to 860px as a scrollbar would:

```
PRE-FIX   boxW=844  cellsFitBox=100  statusCells=104  clipped=TRUE   tail "│ YOLO"
FIXED     boxW=844  cellsFitBox=100  statusCells=100  clipped=false  tail "│ YOLO"
```

The fix was validated with that A/B: the pre-fix client was rebuilt from `HEAD` and
served on its own port, then the same measurement was taken.

## Fix

* `sendGeometry()` is the one place that reports geometry: it measures, compares with
  `lastGeometry`, and sends only when the `cols×rows` actually changed (each message
  costs a full server-side repaint).
* `scheduleGeometry()` keeps the existing 120 ms debounce.
* A `ResizeObserver` on `#screen` calls `measure()` + `scheduleGeometry()`, so any
  change to the box — scrollbar, zoom, late font swap — re-reports. The observer
  cannot loop: `measure()` republishes the cell metrics, but a report only goes out
  when the measured geometry changed.
* `socket.onopen` clears `lastGeometry` before reporting, because a fresh connection
  may be a fresh server that knows nothing about this page's geometry.

## Tests

* `TestClientJS_ReReportsGeometryWhenTheBoxNarrows` — the page watches `#screen`,
  reports nothing before the debounce, then sends exactly one `resize` with
  `cols=85` for a 700px container, and stays silent when a second callback changes
  nothing. RED before the fix: "the page does not watch the scroll container".
* The stub gained a `ResizeObserver` (`window.__fireResizeObserver`, `__roWatches`).
* `TestClientJS_SSEResizeGoesToResizeEndpoint` (existing) still passes: the debounce
  and the `/resize` endpoint are unchanged.

## Validation

* A/B in a real browser (above).
* `go test -count=1 -race ./internal/webui/` ok; `go vet`, `staticcheck`,
  `gocognit -over 15`, `gocyclo -over 12` clean on the package.
* Full `go test -count=1 ./...` green (90 packages, exit 0) for the session's changes.

## Residual risk

The geometry still only follows the box the page measures (`#screen`); a layout that
narrows the *grid* without touching `#screen` (none exists today — the grid is a
block child of the screen) would need its own observer. A cell whose glyph the
browser's fallback font renders wider than one cell (measured: `⟡` at 9.8px against
an 8.44px cell) can still push a row's tail past the box; that is a font-metric
mismatch, not a geometry one, and is left for a separate report.
