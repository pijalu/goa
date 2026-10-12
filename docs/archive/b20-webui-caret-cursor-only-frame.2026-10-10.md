<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# B20 — web UI: the caret does not move when the keystroke changes no cell

Closed 2026-10-10. Archived from `bugs.md`.

## Reported

On the web page (`goa server`, blocks plane): typing a character moves the caret,
typing a **space** does not — and the space itself only shows up on the *next*
keystroke. Measured on the live page with a real Chromium through
`agent-browser`, driving the page with `KeyboardEvent`s and reading `#caret`:

```
keydown 'a'  → caret left 16.44px   row33 "a"
keydown ' '  → caret left 16.44px   row33 "a"        <- nothing moved
keydown 'b'  → caret left 33.31px   row33 "a b"      <- the space lands late
```

## Root cause (traced, not guessed)

The TUI compositor *does* emit the cursor move for such a keystroke: `renderDiff`
always ends with `appendCursorSeq` (`tui/compositor_rowdiff.go:403`), which writes
an absolute CUP `\x1b[<row>;<col>H` — and it has to, because when a space lands on
a blank cell the canvas row for the input line is byte-identical, so the cursor
sequence is the *only* byte of the frame.

On the server those bytes reach `CellGrid.Process` (`internal/webui/cells.go`),
which decided "did the screen move" from the emulator's dirty **rows** alone
(`return len(dirty) > 0`). With no cell changed the answer was false, so
`VirtualTerminal.WriteString` returned without publishing
(`internal/webui/terminal.go`) and **no frame reached the browser at all**: the
page kept the previous frame's `cur`, and its caret — placed by
`placeCaret(msg.cur)` on every frame — stayed where it was until the next keystroke
that happened to change a cell. The client was not at fault, which is why the fix
is server-side.

Cursor **visibility** (`\x1b[?25h/l`) and the title are the same class of "state
outside the cells" and were already handled — by `emu.MarkDirty()`, which marks
*every* row, i.e. it pays a whole-screen row diff for a caret that moved one
column. The cursor **position** was simply never in that class.

## Fix

* `CellGrid` gains `stateDirty`: "something outside the cell rows moved (cursor
  position, cursor visibility, title) — a frame is due even though no row differs".
* `Process` captures the emulator's cursor (row, col) and visibility before and
  after the bytes and raises `stateDirty` when either moved; it reports a change
  on `len(dirty) > 0 || stateDirty`.
* `Patches` ships a **state-only** frame for that case: no row patches at all (the
  frame's `Cursor` is the payload), and `takeDirtyRowsLocked` now reports whether
  the frame is state-only so the "nothing is marked → compare every row" fallback
  does not fire on every cursor move.
* `MarkState` (title, cursor visibility) sets the same flag instead of
  `emu.MarkDirty()`, so those paths got cheaper too; `FullPatches`,
  `DiscardChanges`, `Clear` and `Resize` clear it (they ship the whole screen or
  drop the changes).
* `VirtualTerminal.publish` no longer uses `patches == nil` as "not taken yet":
  with state-only frames an empty patch set is a legitimate result, so an explicit
  `taken` flag says which plane already drained the grid's dirty marks — otherwise
  the blocks plane would re-ask the grid and hit the whole-screen fallback.
* Client: no change needed; a test now pins that a patchless frame still
  repositions the caret.

## Tests

* `TestCellGrid_CursorOnlyMoveIsAFrame` — a bare CUP after a text write reports a
  change, produces no row patches, exposes the new column, and the state mark is
  consumed exactly once.
* `TestCellGrid_StateMarkDoesNotSuppressRowPatches` — the state flag never hides
  rows that did change.
* `TestCellGrid_MarkStateShipsNoRows` — title/visibility ship state, not rows.
* `TestVirtualTerminal_CursorOnlyWritePublishesAFrame` — a cursor-only write
  publishes exactly one frame per plane, both with empty patches and the moved
  cursor.
* `TestClientJS_CaretFollowsAPatchlessFrame` — the page repositions `#caret` from a
  frame with no patches (goja, real `app.js`).

RED-before-fix verified: with the production change stashed,
`TestVirtualTerminal_CursorOnlyWritePublishesAFrame` fails with
`a cursor-only write published 0 cells frames, want exactly 1` — the bug itself.

## Validation

* Real browser re-run on a rebuilt `goa server`: typing `a`, then four spaces, then
  `z` moved the caret `16.44 → 24.88 → 33.31 → 41.75 → 50.19 → 58.63px` (one cell
  each) with the row showing `a    z` — the space lands immediately, caret included.
* `go vet ./...`, `staticcheck ./internal/webui/...`, `gocognit -over 15
  internal/webui/`, `gocyclo -over 12 internal/webui/` — clean.
* `go test -count=1 -race -cover ./internal/webui/...` — ok (webui 90.9%,
  supervisor 90.3%).

## Residual risk

A frame now ships whenever the caret moves, which is one extra tiny document per
keystroke (no rows, a few dozen bytes). That is the intended cost: the caret has to
reach the browser. The whole-screen diff that `emu.MarkDirty()` used to force for
state changes is gone with it, so state-only frames are strictly cheaper than
before.
