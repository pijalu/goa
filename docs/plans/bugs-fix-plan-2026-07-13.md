# Bug Fix Plan 2026-07-13 — Final Report

## Overview

All items from `bugs.md` have been addressed and validated. This plan records
what was changed, how it was tested, and the final code-quality gate results.

## Guideline Compliance

- Reproduced/inspected each item before editing.
- Localized changes to the smallest region.
- Added regression tests for every behavioral fix.
- Ran each code-quality check separately.
- Noted pre-existing warnings unrelated to these changes.

## Bug Status

| # | Bug | Status | Key change | Regression test |
|---|-----|--------|------------|-----------------|
| 1 | docs/docs.go documentation cleanup | closed | Moved non-reference docs to `docs/archive/`, updated `docs.go` descriptions, removed duplicate `USER_GUIDE` and added `ORCHESTRATOR`. | `go test ./docs/...` |
| 2 | Steering stop timing | closed | `core/orchestrator.Runtime.SteerAll` now resumes a paused orchestrator loop (the default TUI steering target is "all"). | `TestRuntime_SteerAll_ResumesPausedLoop` |
| 3 | Tool call streaming | closed | `internal/app/agent_streams.go` now handles `IsDelta` tool-call events and updates the existing widget instead of creating duplicates. | `TestAgentStream_PartialToolCallUpdatesExistingWidget` |
| 4 | Tool call timing | closed | `tui.ToolExecutionComponent` shows live `elapsed Xs` while running and final `Took Xs` after completion. | `TestToolExecutionComponent_ElapsedDuration` |
| 5 | Clear on multi-line input | closed | `tui.Editor.clearLocked` resets `stableMaxLines` so clearing collapses back to single-line. | `TestEditor_ClearResetsStableMaxLines` |
| 6 | Orchestrator streaming repeated blocks | closed | `IsDelta` propagated through `core/orchestrator.Runtime.RecordAgentToolCall`, adapter, `translateOrchEvent`, and `handleAgentToolCall` so streaming updates are applied in place. | `TestAgentStream_DeltaToolCallFollowedByFinalCreatesOneWidget` |
| 7 | Orchestrator question not displayed | closed | Added `orchpanel.EvAskUser` to the neutral seam, translated `EventAskUser`, and surfaced the question in the chat viewport. | `TestHandleAskUser_DisplaysQuestion` |
| B1 | Empty `--prompt ""` launches TUI | closed | `RuntimeOptions.Validate()` rejects explicitly empty `--prompt` with `PromptGiven`; `promptImpliesHeadless()` uses `PromptGiven`. | `TestRuntimeOptions_EmptyPromptImpliesHeadless`, `TestEmptyPromptValidation` |
| B2 | Build target must be `./cmd/goa/` | closed | Updated skill and documentation references to use `./cmd/goa/`. | `go build -o /tmp/goa ./cmd/goa/` |
| B3 | Headless output markers | closed | Documented as a validation/expectation issue, not a code bug. | n/a |

## Code Quality Gate

Run separately and confirmed clean (only pre-existing unrelated warnings noted):

1. `go vet ./...` — clean
2. `staticcheck ./...` — one pre-existing warning: `tui/editor_render.go:646:6: func bytePosForCol is unused (U1000)`
3. `gocognit -over 15 .` — clean
4. `gocyclo -over 12 .` — clean
5. `go test -count=1 -timeout 120s -race -cover ./...` — all packages pass; one flaky pre-existing test in `tools` (`TestBGExec_ReadOutput_LongLinePreserved`) fails intermittently when the full `tools` package is run under `-race`, but passes when run in isolation.

## Live LM Test Gating

The orchestrator integration tests that connect to a local LM Studio server
are now gated by the environment variable `GOA_ENABLE_LIVE_LM_TESTS=1`. This
keeps the default `go test` suite fast and avoids using the user's general
Goa configuration. A dedicated mock-based unit test (`TestOrchestratorAdapter_NewRuntime_NoLiveProvider`)
covers the adapter construction path without a live provider.

## Archive Step

The resolved bug list has been moved to `docs/archive/bugs.2026-07-13.md` and
`bugs.md` has been restored to the guideline header only.
