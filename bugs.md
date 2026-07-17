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

## Tool and chat history artefacts
Tool call in history shows artefacts of the input line — terminal rendering mixed with conversation output.

**Status:** Investigation needed.
**Hypothesis:** The conversation renderer may be interspersing raw terminal output with tool call metadata. The tool-execution render path should only display structured tool call/result blocks, not raw terminal frames.
**Localization:** `tui/chat_viewport.go` tool rendering logic vs. `tui/tool_execution.go` box rendering.
**Fix approach:** TBD after reproduction.

## Steering view preview
The steering view should show a preview of the message to be sent and a line count for multi-line messages. The user should be able to edit before sending.

**Status:** Investigation needed.
**Hypothesis:** The steering input component (`tui/steering*.go`) may not have a preview widget. The steering queue should populate the edit line instead of sending immediately.
**Localization:** `tui/editor_input.go` steering path + `core/commands/orchestrate_input.go` steering handler.
**Fix approach:** TBD after reproduction.

## Write tool UI is not working
Write stats show line count of preview instead of total written. Ctrl+O expand is unstable — shows only at end of write prep, reverts to preview after completion.

**Status:** Investigation needed.
**Hypothesis:** The stats footer uses `total` computed from streamed partial content (`content` param), which grows as data arrives. After completion, the tool result carries the full content — but if `resolveContent` falls back to partial args incorrectly, the wrong content is rendered. The expand instability may be a TUI-level expand-state issue.
**Localization:** `tools/writefile_renderer.go` `appendWriteStats` and `resolveContent`; `tui/tool_execution.go` expand handling.
**Fix approach:** TBD after reproduction with a large write test case.

## Tool panic on write/edit (typed-nil LSP manager) — FIXED (2026-07-16)
[details unchanged]

## 100% CPU / TUI stuck during long write (O(n^2) tool-arg streaming) — FIXED (2026-07-16)
[details unchanged]

## Skill issue — FIXED (2026-07-16)
[details unchanged]

## tool list — FIXED (2026-07-16)
[details unchanged]

## Tool call start a review but no output of work done — FIXED (2026-07-16)
[details unchanged]

(historical items — see `docs/archive/`)
