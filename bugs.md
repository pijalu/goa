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

## Archive

### Tool call budget counted total calls instead of duplicates (fixed)
**Problem:** The tool-call budget was enforced as a hard per-turn cap (`max_tool_calls: 50`). After 50 calls of *different* tools/files, the session was stopped with "Tool call budget exceeded". The `tool_call_limit_reset_window` was documented as a rolling window but never used for counting duplicates.

**Fix plan:**
1. Redefine `max_tool_calls` as the maximum number of duplicate occurrences of the same call (tool + parameters) within the rolling window defined by `tool_call_limit_reset_window`.
2. Use `max_tool_repeat_consecutive` as the maximum number of consecutive identical calls (default 2).
3. Rewrite `Agent.recordToolCallInBudgetWindow` to count duplicates in the rolling window and consecutive duplicates separately.
4. Return clear, specific guardrail hints to the model (consecutive vs. rolling-window) and keep the turn alive so the model can change approach.
5. Update default config: `max_tool_repeat_total: 0`, `max_tool_repeat_consecutive: 2`, `max_tool_calls: 3`, `tool_call_limit_reset_window: 10`.
6. Update config validation, docs, CLI flag help text, and comments.

**Validation:**
- `go vet ./...`
- `staticcheck ./...` (only pre-existing warnings)
- `gocognit -over 15 .` (only pre-existing over-budget functions)
- `gocyclo -over 12 .` (only pre-existing over-budget functions)
- `go test -count=1 -race -cover ./...`
- New/updated tests: `TestAgent_ToolBudget_DifferentCallsNotBlocked`, `TestAgent_ToolBudget_RollingWindowDuplicate`, `TestAgent_ToolBudget_ConsecutiveDuplicate`, `TestAgent_ToolBudget_LLMReceivesHintAndContinues`, `TestAgent_ToolBudget_GuardResultReturnedToLLM`, and updates to existing budget-related tests.

### SmartSearch index race and corruption (fixed)
**Problem:** Concurrent `smartsearch` calls in the same batch failed with `rename .../index.gob.tmp .../index.gob: no such file or directory`. `Builder.Save` used a fixed temp path, so the first goroutine to rename removed the shared temp file and later goroutines failed. There was also no automatic recovery from a corrupted index file.

**Fix plan:**
1. Make `Builder.Save` use a unique temp file name per invocation (`index.gob.<pid>.<nanoseconds>.tmp`).
2. Add a package-level mutex to serialize index writes within the same process.
3. Add a per-tool `indexMu` in `SmartSearchTool` so concurrent calls for the same project share one build/refresh.
4. In `SmartSearchTool.getOrBuildIndex`, detect build/refresh failures, remove the corrupted index file, rebuild from scratch, and report the rebuild in the tool result.

**Validation:**
- `go vet ./...`
- `staticcheck ./...` (only pre-existing warnings)
- `gocognit -over 15 .` (only pre-existing over-budget functions)
- `gocyclo -over 12 .` (only pre-existing over-budget functions)
- `go test -count=1 -race -cover ./...`
- New tests: `TestBuilder_SaveUniqueTempNames`, `TestBuilder_LoadCorruptedIndexRebuiltFromScratch`, `TestSmartSearchTool_CorruptedIndexRebuilt`, `TestSmartSearchTool_ConcurrentCallsDoNotCorruptIndex`.

### Context size (fixed)
**Problem:** On local providers (e.g. LM Studio), the real loaded context length (`loaded_context_length`) is only exposed after the model is loaded. `ResolveActiveModel` was querying `/api/v0/models` at startup, before the model was loaded, so it fell back to `max_context_length` (262144) instead of the actual loaded context (32768).

**Fix plan:**
1. Remove eager context-window detection from `ResolveActiveModel`.
2. Add `ProviderManager.RefreshLocalContextWindow()` that calls the existing `detectLocalContextWindow()` on demand.
3. Add `Agent.SetContextWindow(nCtx int)` so the runtime context window can be updated after detection.
4. Add `AgentManager.SetContextWindowRefresher()` and a one-shot `maybeRefreshContextWindow()` hook on the first `EventStateChange` after a session starts.
5. Wire the refresher in `internal/app/subsystems.go` to call `providerMgr.RefreshLocalContextWindow()`.
6. The refresh runs asynchronously; when it returns, it updates the active agent's `ContextWindow` and emits an `EventContextStats` so the footer reflects the real loaded length.

**Validation:**
- `go vet ./...`
- `staticcheck ./...` (no new warnings)
- `gocognit -over 15 .`
- `gocyclo -over 12 .`
- `go test -count=1 -race -cover ./...`
- New tests: `TestResolveActiveModel_NoEagerLocalContextDetection`, `TestAgentManager_RefreshContextWindow_OnFirstStateChange`, `TestSetContextWindow_UpdatesEffectiveMaxTokens`.

# TODO

## Change of profile is not saved
Changing the mode to coder to coding-posture is not saved: 
1/ Change the mode to coding-posture.
2/ Exit the application.
3/ Restart the application: Back to coder mode.

The mode change must be saved

## Local mode size
The local model max tokens size should be based on loaded_context_length on LMstudio, on loaded model (state	"loaded") - this means the information can only be used after the model is loaded/after end of the first turn.
Make sure this is correctly done as the tool currently show max_context_length, which can only be the default (and unlikely) value on local providers.

Validate if llama.cpp server is using the same paradigm as LMstudio provider and in all case, follow the same logic for all local: Only report context after receiving the first delta from the first response.

## Cursor input line position
The cursor input line position is often incorrect - especially at end of line where it will jump to the next line.

## First long input
After start and typing a long input creating a scroll will not be correctly rendered - there will not be any scroll possible outside of the terminal window.

## Loop catching in thinking
The UI show important loop in thinking but there were no clear warning on thinking loops where it was expected to trigger the loop protection:
```
▾ thinking...
  ▏I can see the main.ts files are very similar. The pbl version has additional imports from SDK runtime (isMobileDevice,
  ▏setMobileScreenThreshold) and some extra localStorage handling. Let me now examine the component files in pbl/presto2 to compare with
  ▏what exists in gf/presto2.
  ▏
  ▏I can see the main.ts files are very similar. The pbl version has additional imports from SDK runtime (isMobileDevice,
  ▏setMobileScreenThreshold) and some extra localStorage handling. Let me now examine the component files in pbl/presto2 to compare with
  ▏what exists in gf/presto2.
  ▏
  ▏I can see the main.ts files are very similar. The pbl version has additional imports from SDK runtime (isMobileDevice,
  ▏setMobileScreenThreshold) and some extra localStorage handling. Let me now examine the component files in pbl/presto2 to compare with
  ▏what exists in gf/presto2.
  ▏
  ▏I can see the main.ts files are very similar. The pbl version has additional imports from SDK runtime (isMobileDevice,
  ▏setMobileScreenThreshold) and some extra localStorage handling. Let me now examine the component files in pbl/presto2 to compare with
  ▏what exists in gf/presto2.
  ▏
  ▏I can see the main.ts files are very similar. The pbl version has additional imports from SDK runtime (isMobileDevice,
  ▏setMobileScreenThreshold) and some extra localStorage handling. Let me now examine the component files in pbl/presto2 to compare with
  ▏what exists in gf/presto2.
  ▏
  ▏I can see the main.ts files are very similar. The pbl version has additional imports from SDK runtime (isMobileDevice,
  ▏setMobileScreenThreshold) and some extra localStorage handling. Let me now examine the component files in pbl/presto2 to compare with
  ▏what exists in gf/presto2.
  ▏
  ▏I can see the main.ts files are very similar. The pbl version has additional imports from SDK runtime (isMobileDevice,
  ▏setMobileScreenThreshold) and some extra localStorage handling. Let me now examine the component files in pbl/presto2 to compare with
  ▏what exists in gf/presto2.
  ▏
  ▏I can see the main.ts files are very similar. The pbl version has additional imports from SDK runtime (isMobileDevice,
  ▏setMobileScreenThreshold) and some extra localStorage handling. Let me now examine the component files in pbl/presto2 to compare with
  ▏what exists in gf/presto2.
  ▏
  ▏I can see the main.ts files are very similar. The pbl version has additional imports from SDK runtime (isMobileDevice,
  ▏setMobileScreenThreshold) and some extra localStorage handling. Let me now examine the component files in pbl/presto2 to compare with
  ▏what exists in gf/presto2.
  ▏
  ▏I can see the main.ts files are very similar. The pbl version has additional imports from SDK runtime (isMobileDevice,
  ▏setMobileScreenThreshold) and some extra localStorage handling. Let me now examine the component files in pbl/presto2 to compare with
  ▏what exists in gf/presto2.
  ▏
  ▏I can see the main.ts files are very similar. The pbl version has additional imports from SDK runtime (isMobileDevice,
  ▏setMobileScreenThreshold) and some extra localStorage handling. Let me now examine the component files in pbl/presto2 to compare with
  ▏what exists in gf/presto2.
  ▏
  ▏I can see the main.ts files are very
```

Full log: /Users/muaddib/dev/lnb/.goa/exports/goa-export-20260702-073119.zip (9 entries, 104399 bytes)

## Scroll
Scroll content is not always correctly populated, especially during streaming, possibly when there are issues with the server or network. The scrolling back should be smooth and complete at all time.
