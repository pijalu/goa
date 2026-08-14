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

(none)

# Archive

## Cursor jumps out of input during tool status redraw — FIXED
Tool status changes trigger a cursor glitch: the cursor momentarily jumps out of the input box, then returns to the expected position on the next render.

**Root cause (traced):** `appendCursorSeq` mapped the cursor row linearly (`screenRow = cursorRow - vt + 1`), but the paint layout is two-phase: transcript rows are linear in `vt`, while the chrome band (editor) is PINNED to the screen bottom (`screenRow = windowH + (cursorRow - contentEnd) + 1`). The two mappings agree only when `vt` equals the natural bottom anchor (`canvasLen - height`). During a tool-widget collapse (canvas shrinks by `d` while the scrollback watermark is still high) `windowTop` clamps `vt` above the natural anchor, and the cursor landed `d` rows ABOVE the editor row — outside the input box — until the canvas regrew (next streaming chunk) snapped it back.

**Fix:** `appendCursorSeq` (`tui/compositor.go`) now maps through the same two-phase layout as the paint: extracted `cursorScreenRow(targetRow, totalLines, vtop, height)` computes `contentEnd = totalLines - chromeH`; cursor rows at/above `contentEnd` map to the pinned chrome band (`windowH + (row - contentEnd) + 1`), rows below map linearly. No Scene/layout changes — the paint split is purely by canvas row.

**Tests:** `TestCompositor_CursorStaysOnEditor` (`tui/cursor_clamp_repro_test.go`, RED first): scrolled canvas with chrome, shrink transcript so `vt` clamps, explicit `Scene.Cursor` on the editor row; replayed through `screenEmulator`; asserts the hardware cursor row equals the painted editor row, not the linear map. `TestCompositor_CursorStaysOnEditorAcrossShrinkAndRegrow` covers the stacked shrink+regrow sequence (tool collapse + queued user message).

**Validation:** PTY session (opencode-go/deepseek-v4-flash): `read` tool running→done collapse + streaming — editor/input stayed pinned at the screen bottom, no visible cursor detach. Gates: `go vet`, `staticcheck`, `gocognit -over 15`, `gocyclo -over 12`, `go test -count=1 -race -cover ./...` all pass (pre-existing warnings unrelated to the change: `tui/render_trace.go` U1000 unused `sceneLayersTrace`, `renderLoop` gocognit 16, `scrollOffUnstable` gocyclo 13 — all untouched).

## Input cursor occasionally redrawn one line too high — FIXED
Cursor on the input box was sometimes redrawn one line above its true position (transient), worst after a `read` tool completes and a user message is queued right after (two stacked height changes).

**Root cause (traced):** same family as the cursor-jump bug. The frame where the tool widget collapses shrinks the canvas; `windowTop` clamps `vt` to the stale scrollback watermark (above the natural anchor); the two-phase repaint keeps the editor pinned at the screen bottom, but `appendCursorSeq` positioned the cursor with the linear mapping, placing it exactly `d` rows (the shrink delta) above the true editor row. A user message queued+sent regrows the canvas past the watermark on the next frame(s), re-aligning both mappings — the snap-back.

**Fix:** same `cursorScreenRow` two-phase mapping as above (single fix covers both bugs).

**Tests:** same regression tests, incl. the shrink+regrow sequence asserting the cursor row stays on the editor's painted row across consecutive renders.

**Validation:** same gates + PTY repro as the cursor-jump bug — all green.

## Blank screen + cursor on first line after `/new` — FIXED
After `/new`, the cursor landed on line 1 and the screen stayed blank until the input line.

**Root cause (traced):** the renderLoop requests a Scene snapshot from the commandLoop, then hands it to `compositor.Render`. `/new` (handleNewSession) runs `chat.Clear()` + `compositor.Clear()` on the commandLoop — which can land BETWEEN the snapshot and the Render. The stale pre-clear scene then consumed `clearRequested`, repainted the OLD canvas as a "first frame", and restored the stale `scrollTop`/`prevLines`. Every subsequent frame diffed against that stale baseline: `windowTop` clamped `vt` to the stale watermark far above the (now short) canvas, no transcript row was in range, and `appendCursorSeq` clamped the cursor to screen row 1 — blank window, cursor on line 1, chrome at the bottom.

**Fix:** clear-generation epoch on the Compositor. `Clear()` increments `clearGen` under its mutex; `Scene.ClearGen` is stamped at snapshot time (`buildSnapshot`/`renderNow` in `tui/tui.go` read `compositor.ClearGen()`); `Render` drops any scene whose generation is older than the current one (the wipe stays pending for the next, fresh frame). Stale snapshots racing a `/new` can no longer repaint the dead session.

**Tests:** `TestCompositor_RenderDropsStaleSceneAfterClear` (RED first): stale scene rendered after `Clear()` produces NO terminal writes and leaves the wipe pending; the next fresh frame wipes + paints. `TestTUI_ClearTranscriptNextFrameIsFresh`: full engine path — after `/new`-style Clear, the next frame contains no stale session rows and paints the fresh screen.

**Validation:** PTY session: `/new` from an active scrolled session (↑4.7K scrollback) — fresh header/banners/input painted immediately, no blank screen, no cursor at line 1. Gates all pass as noted above.

## Content not shown after `/new` until a resize — FIXED
After `/new`, no content was displayed at all until a window resize occurred, at which point everything appeared.

**Root cause (traced):** same stale-scene race as the blank-screen bug, without the visible cursor clamp: after the stale frame repainted the old canvas and restored the stale watermark, subsequent frames diffed against a baseline that no longer mapped onto the screen; nothing repainted the (short) fresh transcript because `windowTop` was clamped above it. A resize changed the width → `frameGeometryReset` → `drawWindowResetScrollback` reset `scrollTop`/`vt` to 0 and re-emitted everything — which is why a resize "fixed" it.

**Fix:** covered by the clear-generation epoch (+ its regression tests, which assert the frame after a racy stale delivery still paints fresh content with no resize).

**Validation:** same PTY `/new` repro (content visible without any resize) + gates as above — all green.

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
