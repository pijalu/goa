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
