<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Bug Tracking Archive — 2026-07-13

All items below were resolved on 2026-07-13. See `docs/plans/bugs-fix-plan-2026-07-13.md`
for the detailed fix plan and validation steps.

## Guideline (retained for reference)

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

## Closed Bugs

### docs/docs.go
The docs.go should only contains document required for goa agent to understand goa - not about changes and fixes.
Execute a cleanup of all documentation to ensure it only contains the required content

Bug fixes, older plan should be moved to archive.

**Resolution:** Non-reference docs moved to `docs/archive/`. `docs/docs.go` updated
(removed moved entries, added `ORCHESTRATOR`, removed duplicate `USER_GUIDE`).

### Steering stop
Steering text was sent very late - check with Pi at ../pi if the steering on goa is sent as soon as it could be ! (steering should be sent as soon as possible without breaking the flow)

**Resolution:** `core/orchestrator.Runtime.SteerAll` now resumes the orchestrator
loop when it is paused waiting for a user answer. The default TUI steering target
is "all", so broadcasts were previously leaving the orchestrator stuck; this is
now fixed.

### Tool call streaming
write (and edit) should support streaming of tool call: when the LLM is streaming a tool call, the details should be visible to the user in real time to avoid a "freezing" feeling when the tool call is being prepared.

**Resolution:** `IsDelta` is propagated through `core/orchestrator.Runtime.RecordAgentToolCall`,
`internal/app/orchestrator_adapter.go`, `translateOrchEvent`, and
`handleAgentToolCall` so partial tool-call arguments update the existing widget
instead of creating duplicates.

### Tool call timing
When executing a tool call, especially for execution tools like bash, the TUI should show the elapsed time in addition of the timeout duration.

**Resolution:** `tui.ToolExecutionComponent` tracks `startTime` and renders
`elapsed Xs` while running and `Took Xs` after completion.

### Clear on multi-line input does not resize input
On clearing a multi-line input line, the input area does not resize to fall back to a single-line input mode.

**Resolution:** `tui.Editor.clearLocked` resets `stableMaxLines` so the input
collapses back to single-line height.

### Orchestrator streaming does not work
In orchestrator mode, streaming does not work as expected and will get repeated blocks.

**Resolution:** Same as "Tool call streaming" — delta propagation prevents
repeated/duplicate blocks for tool calls. Content/thinking deltas were already
handled correctly; the repeated blocks were caused by non-delta tool-call events
creating new widgets.

### Orchestrator question does not work
Orchestrator is waiting for a response from the user but no question is being asked/displayed.

**Resolution:** Added `orchpanel.EvAskUser` to the neutral seam, translated
`orchestrator.EventAskUser`, and surfaced the question in the chat viewport.

### B1: Empty `--prompt ""` launches TUI instead of headless mode
**Resolution:** `RuntimeOptions.Validate()` rejects explicitly empty `--prompt`
(via `PromptGiven`). `promptImpliesHeadless()` uses `PromptGiven` so the empty
flag still implies headless mode and produces an error instead of opening the TUI.

### B2: `goa build -o` target must be `./cmd/goa/` not `.`
**Resolution:** Documentation and skill references updated to use `./cmd/goa/` as
the build target.

### B3: Headless mode output format uses structured markers
**Resolution:** Documented as a validation/expectation issue, not a code bug. No
code change.

## Code Quality Gate Results

- `go vet ./...` — clean
- `staticcheck ./...` — one pre-existing warning: `tui/editor_render.go:646:6: func bytePosForCol is unused (U1000)`
- `gocognit -over 15 .` — clean
- `gocyclo -over 12 .` — clean
- `go test -count=1 -timeout 120s -race -cover ./...` — clean except for one
  flaky pre-existing test (`TestBGExec_ReadOutput_LongLinePreserved` in `tools`)
  that fails intermittently under `-race` when the full package is run but passes
  in isolation.

## Live LM Test Gating

Orchestrator integration tests that connect to a local LM Studio server now
require `GOA_ENABLE_LIVE_LM_TESTS=1`. A mock-based unit test
(`TestOrchestratorAdapter_NewRuntime_NoLiveProvider`) covers the adapter path
without a live provider.
