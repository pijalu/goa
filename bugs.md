# Bug and feature Tracking

## Guideline
1. Create a detailed fix plan for each bug - the plan must contain test approach and validation steps - execute the plan and validate the fix when all elements are in place.
2. Any issues found must be fixed and the fix plan must be updated accordingly.
3. Issues found during testing must be fixed and the fix plan must be updated accordingly.
4. Each bug should be moved to docs/archive when tested and closed as the associated plan.
5. Use interactive shell/filmstrip to validate the output of the tool - you must verify the actual terminal output.
6. Check code quality with each tool run separately (do not chain them with `;` or `&&`):
- `go vet ./...`
- `staticcheck ./...`
- `gocognit -over 15 .`
- `gocyclo -over 12 .`
- `go test -count=1 -race -cover ./...`
Fix any issues.
! For cognitive and cyclomatic complexity, Pre-existing warnings are acceptable only if they are unrelated to the change and explicitly noted !

At the end of the session - the bug list should be empty, change committed and this file should only contain the guidelines for bug reporting.
If new items are added, restart the process.

Use goals to execute the fix plan - focus on micro tasks goals with new contextto lower context usage - use todos for micro tasks that should share context

Commit at the end of each fix with a clear and descriptive commit message

## Report format
Describe the bug or feature request under `# To fix` below. Keep one section
per item with a short title, the observed behavior, and the expected behavior.

# To fix

### B56 — toggle menu values must not rerender the whole screen

**Observed behavior.** In the tool config menu (and every other
"toggle"-style menu value, e.g. on/off entries), flipping a value
rerenders the complete screen and moves the selection back — an annoying
jump that loses the cursor position on every toggle.

**Expected behavior.** Toggling an on/off value updates only that value's
cell (the selected row's text on↔off), keeps the selector on the same
entry, and produces no full-screen repaint, scroll reset or cursor move.

**Fix and validation plan.**
1. Root cause (verified 2026-11-02): every toggle handler ends with a
   full menu re-open — e.g. core/commands/config_tools.go:99
   `toolToggleHandler` → `c.settingTools()` → a NEW
   `ctx.SelectOption(...)` overlay, so selection resets to the top and
   the screen repaints. Same shape in the other config toggle menus
   (skills, compression, …).
2. Fix design: update in place instead of re-opening —
   a. tui Selector gains an in-place items update (replace item
      labels/descriptions by Value, selection index preserved; no
      overlay rebuild), exposed on the command context
      (e.g. `ctx.UpdateSelectorItems`);
   b. toggle handlers call the update primitive for the flipped row and
      stay in the SAME selector (no re-invoke, no re-centering); only
      error/restart-required paths may flash without touching selection;
   c. apply to every toggle-kind menu value (tools, skills,
      compression, and any other bool row driven by SelectOption).
3. RED first: a live-engine test toggling a row twice asserts the
   selector instance is preserved (same overlay, cursor unchanged) —
   fails today because a new overlay is built; plus a tui unit test for
   the in-place update primitive (selection kept by Value, unknown
   Value keeps cursor).
4. Validate: filmstrip before/after (no full-screen wipe, no cursor
   jump on toggle), toggle regressions for each menu kind, touched
   tui/core suites + gates separately, interactive-shell check.
   Archive, close, commit.
