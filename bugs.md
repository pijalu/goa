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

