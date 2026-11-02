<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# B53 — Markdown table rows repainted at different widths leave scrollback artifacts

Closed: 2026-11-02. Archived from `bugs.md`. Fix commit: e286d79b
(`fix(tui): B53 session-pinned markdown table geometry across width changes`).

## Report

When a markdown table is taller than the viewport (or streams over a
terminal width change), parts of the SAME table end up painted at different
column widths: the scrolled-off (already-in-terminal-scrollback) rows keep
the old geometry while the visible rows are repainted at the new one —
separators and cell columns misalign row over row (consecutive `├──┼──┤`
borders of differing widths inside one table, cells wrapping at different
points).

Cause: the TUI paints into the native terminal; rows that scroll offscreen
are frozen in scrollback and can never be repainted. On resize,
`assistantMessage.renderFrame` recreates the incremental renderer and
repaints at the new width — but only the on-screen lines actually reach the
terminal; everything above stays at the old geometry. Any width change
mid-table (user resize, tmux pane, font change) therefore splits the table
visually.

Expected: a table must never present mixed geometry — either its lines are
stable across width changes (same column widths wherever painted), or the
whole transcript is re-emitted after a width change.

Options considered: (A) session-pinned table render width — cheap,
stable-by-construction for grows/repaints; (B) transcript re-emit on
resize — complete but heavy; (C) viewport-owned scrollback — biggest
change. **Option A implemented.**

## Resolution (Option A — session-pinned table geometry)

`sessionTablePin` (tui/markdown_table_session.go) tracks the narrowest
transcript content width observed this session; `MDStreamRenderer.tablePin`
(a small `tableWidthPolicy` hook) consults it in `renderTable`, so column
widths are computed at that pinned width and a repaint at any later
(wider) width is byte-identical to the rows scrollback already holds.
Prose reflow untouched; only table geometry is pinned. Wired for the
transcript message renderers (assistantMessage incremental renderer +
agentMessage); panels/widgets keep unpinned renderers (ephemeral, fully
repainted).

Tests:
- `tui/markdown_table_pin_test.go` — RED→GREEN: `TestB53_TableRepaintAtWiderWidth_Identity`
  (tall table rendered at W1=70 then re-rendered at W2=140: identical table
  rows; failed pre-fix with 93 vs 63 rows),
  `TestB53_MidStreamWidthChange_NoMixedGeometry` (streaming table across a
  mid-stream width change: final frame homogeneous and identical to the W1
  geometry; failed pre-fix),
  `TestB53_SessionTablePinPolicy` (monotonic min, reset).
- `internal/app/b53_table_resize_test.go` — filmstrip validation:
  `TestB53_Filmstrip_ResizeDuringStreamedTable_NoMixedGeometry` streams a
  30-row table at terminal width 72, performs a real mid-table resize to
  140, and asserts every captured frame — before and after the resize —
  paints the same pinned geometry (no mixed widths on scroll). Verified
  non-vacuous: with the pin wiring temporarily disabled it fails
  (post-resize separator `├─54┼─35┤` vs pinned `├─41┼─26┤`).

Validation: `go test -count=1 -race -timeout 600s ./tui/... ./internal/app/...`
and `go vet ./...` green; gocognit/gocyclo/staticcheck clean.

Known trade-off (inherent to immutable native scrollback): after a terminal
SHRINK the session min drops, so tables painted afterwards use the new,
narrower pin while rows already in scrollback keep the older geometry.
Within any single paint and across grows/repaints the geometry is stable;
full shrink-consistency remains Option B (transcript re-emit) if ever
needed in practice.
