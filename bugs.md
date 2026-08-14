# Bug and feature Tracking

## Guideline
1. Create a detailed fix plan for each bug - the plan must contain test approach and validation steps - execute the plan and validate the fix when all elements are in place.
2. Any issues found must be fixed and the fix plan must be updated accordingly.
3. Issues found during testing must be fixed and the fix plan must be updated accordingly.
4. Each bug should be moved to archive when tested and closed as the associated plan.
5. Use interactive shell to validate the output of the tool - you must verify the actual terminal output.
6. Check code quality with each tool run separately (do not chain them with `;` or `&&`):
    - `go vet ./...`
    - `staticcheck ./...`
    - `gocognit -over 15 .`
    - `gocyclo -over 12 .`
    - `go test -count=1 -race -cover ./...`
    Fix any new issues introduced by the change. Pre-existing warnings are acceptable only if they are unrelated to the change and explicitly noted.

At the end of the session - the bug list should be empty and this file should only contain the guidelines for bug reporting.
If new items are added, restart the process.

# To fix

## Cursor jumps out of input during tool status redraw
Tool status changes trigger a cursor glitch: the cursor momentarily jumps out of the input box, then returns to the expected position on the next render.

**Symptoms:** During a redraw caused by a tool status change, the cursor leaves the input line; it snaps back to the correct position on the following render (transient, not persistent).

**Trigger (user-reported):** Tool status change — cursor should NOT move at all while the tool widget redraws; it only visually jumps out and back.

**Likely cause:** Recently introduced TUI fixes to avoid the duplicated row at the scrollback boundary (4627094). The duplicated-row guard routes repaints through a path that skips or repositions the editor row carrying the cursor (see the "cursor one line too high" entry below for the traced cursor data flow); a skipped/rewritten row near the input can make the cursor appear detached from the input box during that frame.

**Plan:** Reproduce in PTY with a tool status change (esp. `read` running → done), pin the repaint path for the frame where the cursor jumps (trace `full` vs `diff` vs `drawWindow` after the duplicated-row fix), assert the cursor row stays inside the input box across the transition, fix, add regression test, validate per bug guidelines (go vet, staticcheck, gocognit, gocyclo, go test -race -cover).

## Input cursor occasionally redrawn one line too high
Cursor on the input box is sometimes incorrectly redrawn one line above its true position (transient glitch, not persistent).

**Symptoms:** After certain repaints/scrolls, the input cursor sits one line too high; next render usually snaps it back.

**Trigger (user-reported):** Linked to tool calls / change of status — usually after a `read` tool completes (status running → done; widget collapses/updates in the chat viewport). The same issue gets WORSE after a user message is queued and sent: the transcript grows (new user row appended) at roughly the same time as the tool status flips, stacking two height changes (chat grows, tool widget collapses) in consecutive frames near the editor.

**Cursor data flow (traced):**
- Editor embeds `CURSOR_MARKER` in the focused input row (`renderEditorFrame` → `renderContentLine` with `hasCursor`).
- `buildScene` → `extractCursorMarker` scans layers for the marker, strips it from the layer content, sets `Scene.Cursor{Row, Col}` absolute in canvas coords: `marker row + layer.Rect.Y` (`tui.go`).
- Editor `Rect.Y` = accumulated base-layer Y; `buildBaseLayers` bottom-aligns via `totalH` (`Rect.Y = y + totalH - len(lines)`), so cursor row = bottom chrome position.
- Compositor emits the hardware cursor in the same synced buffer: `appendCursorSeq` → `screenRow = cursorRow - vt + 1`, where `vt = windowTop(canvasLen, height)` (`compositor.go`).
- Steady-state path is `renderDiff` (repaints changed rows only); tool-widget growth/shrink inside the window routes through `drawWindow` via `windowContentShifted` guard.

**Working hypothesis:** the compositor's canvas MODEL and the actual SCREEN desync by one row during the tool-widget height change — either a row not repainted (unchangedRow skip) whose screen position moved, or `vt`/scroll delta disagreeing with the emitted line-feed scroll. Cursor is positioned per the model, so it lands one row above the visually stale input box.

**Suspects (to investigate):**
- Editor render path (tui/editor_render.go): cursor row computed against a stale viewport height / chrome height when the bottom chrome band changed (goal bubble, footer shrink) between frame builds.
- Compositor repaint of the bottom chrome band vs. the input layer's cached cursor offset (tui/compositor.go two-phase repaint from the archived screen-glitching fix).
- Differential render skipping the editor row that carries the cursor (CSI 2026 sync window) when only chrome height changed.
- Tool widget collapse after `read` completes: height delta in the chat layer → canvas shrink → `vt`/cursor-row arithmetic; check `repaintWindow` unchangedRow mapping (`prevIdx = i - vt + c.vt`) for the shifted editor row.

**Plan:** Reproduce in PTY with tool-call (esp. `read`) + status-change sequences, pin the exact repaint path (trace `full` vs `diff`), assert screen rows match canvas model after the transition, add regression test, validate per bug guidelines (go vet, staticcheck, gocognit, gocyclo, go test -race -cover).

## Blank screen + cursor on first line after `/new`
After issuing `/new`, the cursor lands on line 1 and the screen stays blank until the input line.

**Symptoms:** Screen blank below top; only the input line is rendered; cursor sits at the first line.

**Plan:** Reproduce in PTY (`/new` from an active session), trace `buildScene`/viewport height after session reset, determine why transcript rows are not rendered (stale canvas height, cleared layer, or `vt`/`windowTop` desync), fix, add regression test, validate per bug guidelines (go vet, staticcheck, gocognit, gocyclo, go test -race -cover).

## Content not shown after `/new` until a resize
After `/new`, no content is displayed at all until a window resize occurs, at which point everything appears.

**Symptoms:** Screen shows only the input line (and possibly chrome), transcript empty; any resize repaints everything correctly. Distinct trigger from the cursor-at-first-line bug — recovery is a resize, not a repaint.

**Plan:** Reproduce in PTY (`/new` then resize), trace whether a full repaint (`full` path) is emitted after session reset; check `dirty`/diff state after `reset`/`clear` (canvas reset may not invalidate scene rows, so nothing repaints until a resize forces a full render), fix invalidation on reset, add regression test, validate per bug guidelines (go vet, staticcheck, gocognit, gocyclo, go test -race -cover).

# Archive

## Screen glitching — FIXED
The screen history can have double line at boundaries.

**Root cause:** When the chrome band shrinks (goal bubble clears), the canvas shortens by the chrome delta. `windowTop` used the natural bottom anchor (`canvasLen - height`) without clamping to the scrollback watermark, so `vt` dipped below `scrollTop`. Rows already emitted into terminal scrollback were repainted on screen — appearing twice: once in scrollback, once at the top of the visible window.

**Fix (2 parts):**
1. `windowTop` (`compositor.go`): Always clamp `vt >= scrollTop`. A scrolled-off row must never reappear on screen — the terminal offers no "unscroll", so repainting it would duplicate it.
2. `repaintWindow` / `drawWindow` (`compositor.go`): Two-phase repaint — transcript rows in screen rows [1, windowH], chrome rows in [windowH+1, height]. This keeps the chrome band pinned at the screen bottom even when `vt` is clamped above the natural anchor, preventing the "chrome in the middle with blank rows below" problem that the original partial clamp (commit 6921104) was working around.

**Tests:**
- `TestCompositor_ChromeShrinkNoDuplicate` (`tui/compositor_boundary_dup_repro_test.go`): reproduces the exact bug — chrome grows then shrinks while the transcript is scrolled, asserts no row appears twice across scrollback+screen.
- `TestCompositor_OneRowShrinkNoDuplicate` (`tui/compositor_partial_shrink_test.go`, replaces `TestCompositor_OneRowShrinkNoBlankBottom`): verifies a 1-row transcript shrink leaves a truthful blank row instead of duplicating a scrolled-off row.

**Validation:** `go vet`, `staticcheck`, `gocognit`, `gocyclo`, `go test -race -cover ./...` — all pass, no new warnings.

Log: /Users/muaddib/dev/creaves.project/.goa/exports/goa-export-20260813-204324.zip
