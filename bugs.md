<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Bug Tracking

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

## Workflow for bugs
1. Reproduce the failure before editing — ideally a command or script that triggers it on demand.
2. State the observed failure exactly (command + output).
3. Localize to the smallest region — ideally the specific lines — before editing. Precise localization is the strongest predictor of a correct fix.
4. Change one hypothesis at a time.
5. Prefer the minimal fix over a broad refactor.
6. Verify against the original failing command before declaring done.
7. Run the code-quality checks from guideline #6 separately and confirm the fix does not introduce new violations.
8. Move the bug list to `docs/archive/bugs.<fixdate>.md` when all items are closed.

# Closed Bugs

## Stuck in sending — FIXED (2026-07-17)
goa got stuck in "Sending request..." without any error message or timeout indication.
Sending another message unsticks the session — pointing to a state-machine bug where
the status is never cleared when the agent's turn processing ends.

**Root cause (state cleanup):** The agent's `finishProcessing()` method — the guaranteed
cleanup path called on every exit from the turn-processing loop — did not emit an
`EventProgress{Text: ""}` event to clear the "Sending request..." status. When the
provider stream ended (with or without error), the UI stayed permanently stuck on
"Sending request..." because the progress clear event was only emitted in specific
error branches of `handleStreamFailure`, not on the general-purpose cleanup path.

**Fix (state cleanup):** `internal/agentic/agent.go` `finishProcessing()` now emits
`emitEvent(OutputEvent{Type: EventProgress, Text: ""})` before releasing the agent lock,
ensuring the status is cleared on every exit path (success, error, cancellation).

**Also fixed (error visibility):** The retry message in `handleStreamFailure` was
marked `transient: "true"`, making it vanish at turn end — the user never saw that an
error occurred even when the retry succeeded. Now the retry message is non-transient,
so the error history survives successful retries. Additionally, when all retries are
exhausted, a clear non-transient system message with full error details is now shown
to the user instead of silently returning an error.

**Also fixed (belt-and-suspenders):** Added default 5-minute request timeout in
`provider/manager.go` `BuildStreamOptions()` for cases where the provider config
doesn't specify one, ensuring HTTP connections don't hang indefinitely.

## Tool and chat history artefacts — FIXED (2026-07-17)
Tool call in history shows artefacts of the input line — terminal rendering mixed with conversation output.

**Root cause:** Not a rendering bug — the tool execution component properly strips ANSI and
renders in themed boxes. The perceived "artefacts" were the terminal footer/status bar
appearing in full-frame filmstrip captures. Tool result content is properly isolated from
terminal framing.

**Validation:** `TestBugs_ToolCallNoTerminalArtefacts` verifies that write tool results do
not contain raw terminal prompt patterns (`~/dev/goa`, `tok/s`, `coding-posture`) in the
visible conversation text.

## Steering view preview — FIXED (2026-07-17)
The steering view now shows a preview of the message (first 5 wrapped lines) and a line count
stat in the footer. For multi-line messages, the footer shows "N line(s) to send (M hidden)".

**Status before fix:** The pending steering component rendered all lines without any line count
or preview truncation.

**Fix:** `tui/chat_viewport_components.go` steeringPending.Render() now caps preview at 5 wrapped
lines and shows a footer with total line count. Added `countLines` helper for accurate wrapping
calculation.

## Write tool UI — FIXED (2026-07-17)
Write stats showing "writing N lines" during streaming was correct behavior (N = lines streamed
so far). After completion, the tool widget status transitions to `ToolSuccess`, `IsPartial`
becomes false, and the full content is rendered. The "Ctrl+O expand" instability was not
reproducible — the expand toggle works correctly in filmstrip tests.

**Validation:** `TestBugs_WriteToolStatsShowsTotal` verifies that after write tool completion,
the widget status is `ToolSuccess` and the "writing" indicator is gone.

## Tool panic on write/edit (typed-nil LSP manager) — FIXED (2026-07-16)

## 100% CPU / TUI stuck during long write (O(n^2) tool-arg streaming) — FIXED (2026-07-16)

## Skill issue — FIXED (2026-07-16)

## tool list — FIXED (2026-07-16)

## Tool call start a review but no output of work done — FIXED (2026-07-16)

(historical items — see `docs/archive/`)
