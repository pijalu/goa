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

### Tool widget update with multiple tools (fixed)
**Problem:** Tool widgets were not correctly updated when multiple tools were called in streaming mode. The user expected all widgets to show their content/results, but some widgets remained unchanged.

**Root cause:** `App.lookupActiveTool` in `internal/app/stats.go` fell back to the legacy single-slot `activeTool` whenever a non-empty `ToolCallID` was not found in the `activeTools` map. When multiple tools were in flight, this could update the wrong widget. Additionally, `activeTool` and `activeTools` were never cleared at the end of a turn, so stale references could corrupt updates in the next turn.

**Fix applied:**
1. Changed `lookupActiveTool` to only match non-empty IDs against the `activeTools` map; it no longer falls back to the legacy slot for ID-bearing results.
2. Added `findPendingTool` in `internal/app/stats.go` to walk the chat entries from oldest to newest and update the first still-pending tool widget when the result has no matching ID (provider omitted IDs).
3. Cleared `activeTool` and `activeTools` in `handleSessionEnd` so they do not leak across turns.
4. Extracted the widget-update logic into `applyToolResultToWidget` to avoid duplication and ensure output, status, and partial flag are set consistently.

**Test approach:**
- Added `TestHandleToolResult_MultipleToolsWithIDs` to verify that each tool widget is updated with the correct result when `ToolCallID` is present.
- Added `TestHandleToolResult_MultipleToolsWithoutIDs` to verify that results are still routed to the pending widgets when the provider omits IDs.

**Validation:**
- `go vet ./...` (no new issues)
- `staticcheck ./internal/app/...` (only pre-existing warnings unrelated to the change)
- `gocognit -over 15 .` (no new over-budget functions)
- `gocyclo -over 12 .` (no new over-budget functions)
- `go test -count=1 -race -cover ./...` (all pass; one unrelated flaky test in `core` failed on the first run, passed on rerun)

### Status bar cache label (fixed)
**Problem:** The status bar cache-hit indicator showed a coffee mug (`☕20%`) instead of the requested `CH20.0%` format.

**Root cause:** `buildFooterStatParts` in `internal/app/stats.go` formatted the cache hit as `☕%.0f%%`.

**Fix applied:**
1. Changed the format string to `CH%.1f%%` in `internal/app/stats.go`.
2. Updated `TestFormatFooterStats_CacheHitPercentage` in `internal/app/app_test.go` to assert the new `CH20.0%` format and the absence of `CH` when `PromptN` is zero.

**Validation:**
- `go test -count=1 -run TestFormatFooterStats_CacheHitPercentage ./internal/app` (pass)
- `go vet ./...` (no new issues)
- `staticcheck ./internal/app/...` (only pre-existing warnings unrelated to the change)
- `gocognit -over 15 .` (no new over-budget functions)
- `gocyclo -over 12 .` (no new over-budget functions)
- `go test -count=1 -race -cover ./...` (all pass)

### Input cursor position and marker scan (fixed)
**Problem:** Review of the input cursor path found two issues: (1) the hardware cursor was placed at an incorrect screen row on the first frame because `Compositor.fullFrame` set `c.prevH` after calling `positionHardwareCursor`, and (2) `extractCursorMarker` scanned every base layer from top to bottom on every frame, making cursor extraction O(chat history length).

**Root cause:**
1. `fullFrame` initialized the previous-frame height at the very end of the function, so `positionHardwareCursor` used `c.prevH == 0` on the first render and clamped the emitted CUP row to 0.
2. `extractCursorMarker` iterated base layers front-to-back; since the cursor marker is always emitted by the focused input component (the last few base layers), it unnecessarily walked the entire chat history.

**Fix applied:**
1. Moved `c.prevH = height` in `tui/compositor.go` so it is set before `positionHardwareCursor` is called.
2. Rewrote `extractCursorMarker` in `tui/tui.go` to scan base layers and their lines in reverse order, so it finds the focused input layer first.

**Test approach:**
- Added `TestCompositor_CursorPositionOnFirstFrame` to verify the CUP row emitted on the first full-frame render matches `Scene.Cursor.Row`.
- Added `TestExtractCursorMarker_ReverseScanPrefersLastLayer` to verify the extractor finds the marker in the last base layer and computes the right column.

**Validation:**
- `go vet ./...` (no new issues)
- `staticcheck ./tui/... ./core/...` (only pre-existing warnings unrelated to the change)
- `gocognit -over 15 .` (no new over-budget functions)
- `gocyclo -over 12 .` (no new over-budget functions)
- `go test -count=1 -race -cover ./...` (all pass)

### agent stop (fixed)
**Problem:** The agent stopped in the middle of a workload without reporting anything in the UI. The user saw the agent go idle with no explanation.

**Root cause:** `AgentManager.runAgentTurn` runs the active agent in a goroutine and had no panic recovery. If the agent `Run` method panicked (e.g. due to a bug in the streaming path, an unexpected provider response, or an invariant violation), the goroutine died silently, `am.running` was set to false, and no `EventEnd` reached the UI. From the user's perspective the agent simply stopped.

**Fix applied:**
1. Introduced a small `agentRunner` interface (`Run` / `RunWithImages`) so `runAgentTurn` can be tested with a panic-injecting runner.
2. Added a `defer recover` to `runAgentTurn` that logs the panic and emits an `EventEnd` with descriptive error text. The UI's existing `EventEnd` error path surfaces this to the user and resets the footer/spinner, so the user knows the turn ended abnormally.
3. The existing cleanup defer (cancel context, set `am.running = false`, dispatch pending steering) still runs before the panic handler emits the event, so the manager is left in a consistent state.

**Test approach:**
- Added `TestAgentManager_TurnPanic_EmitsEndEvent` in `core/agentmanager_test.go` using a `panicRunner` mock that panics in `Run`. It verifies that:
  - An `EventEnd` is emitted on the internal events channel,
  - The `EventEnd` carries non-empty error text,
  - `AgentManager.IsRunning()` returns false after the panic.

**Validation:**
- `go vet ./core/`
- `staticcheck ./core/`
- `gocognit -over 15 .` (no new violations)
- `gocyclo -over 12 .` (no new violations)
- `go test -count=1 -race ./core/`
- `go test -count=1 -race -cover ./...` (all pass)

### Performance issues (fixed)
**Problem:** The TUI used too much CPU on redraws. The chat viewport re-rendered the entire conversation on every streaming update, and the compositor diffed the full canvas against the previous frame, so both rendering and diff work grew linearly with history.

**Root cause:**
1. `ChatViewport.Render` used a single viewport-level cache that was invalidated on every mutation (`Append`, `UpdateLast`, `RemoveLast`), forcing every message view to re-render each frame.
2. `Compositor.renderChangePath` ran `firstLastDiff` over the entire previous and new canvas, so the diff cost was O(history length) per frame.
3. `TUI.renderLoop` polled every 16ms, which is fine as a ceiling but was described as a target rather than a max refresh rate.

**Fix applied:**
1. Added per-entry render state (`renderedWidth`, `renderedLines`, `dirty`) to `MessageEntry` in `tui/model.go`. `ChatViewport` now re-renders only entries whose state changed. When the last entry is the only dirty one — the common streaming case — `updateLastEntry` splices the new entry lines into the existing frame cache instead of rebuilding the full conversation.
2. Added `Compositor.visibleRegionDiff` in `tui/compositor.go`. The compositor now diffs only the visible viewport (bounded by terminal height) and accounts for downward scrolling, so a small append only redraws the new bottom lines. `emitLargeScroll` uses the previous viewport top + height as the gap start for large scrolls, preserving the scrollback-population invariant.
3. Changed `TUI.renderLoop` in `tui/tui.go` from a polling ticker to a signal-driven model: `RequestRender` sends to a buffered `dirtyChan`, and the loop waits for that signal, then throttles to a maximum of 60fps (16ms). This makes the refresh rate a ceiling, not a target, and the loop sleeps when there is no work.

**Test approach:**
- Added `TestChatViewport_PerEntryCache` in `tui/performance_test.go` to verify that only the changed entry is re-rendered during streaming-style updates.
- Added `TestCompositor_OnlyRedrawsVisibleChanges` to verify that a single-line change in a 100-line canvas produces only a few CUP sequences, not a full redraw.
- Added `BenchmarkChatViewport_StreamAppend` to measure the streaming update path.

**Validation:**
- `go vet ./...` (no new issues)
- `staticcheck ./...` (only pre-existing warnings unrelated to the change; new `tui/...` files are clean)
- `gocognit -over 15 .` (only pre-existing over-budget functions)
- `gocyclo -over 12 .` (only pre-existing over-budget functions)
- `go test -count=1 -race -cover ./...` (all pass)

### Jail warning (fixed)
**Problem:** The bash jail produced false-positive `jail_violation` errors for safe commands that wrote code into the project using a heredoc. The exported session showed three `jail_violation` tool results; one of them was a command that only referenced project paths (`tools/longline_diag_test.go`) and contained Go `//` comments inside the heredoc body.

**Root cause:** `looksLikePath` in `tools/bash_jail.go` treated any token starting with `/` as a filesystem path. A bare `//` token (e.g. a Go comment) is not a meaningful path, but it was passed to `pathUnderDir`, which resolved it as the root directory and reported it as outside the project.

**Fix applied:**
1. Refined `looksLikePath` so that slash-prefixed tokens must contain at least one non-slash character to be considered a path. Bare slash sequences (`//`, `///`, etc.) are now ignored.
2. Real absolute paths such as `/tmp`, `/etc/passwd`, `./tools/`, and `../outside` continue to be detected correctly.

**Test approach:**
- Added `TestBashJail_SlashSlashComment_NoFalsePositive`, which builds a heredoc command containing `//` comments and verifies `bashReferencesOutsidePath` returns false.
- Added `TestBashJail_RealAbsolutePaths_StillDetected` to ensure `/tmp`, `/etc/passwd`, `cd /tmp && pwd`, and `find /tmp` are still rejected.
- Extended `TestBashJail_LooksLikePath` with `//`, `///`, and `//tmp` cases.
- Added `TestBashTool_Jail_HeredocWithSlashSlashComment` in `tools/bash_test.go` to run the real `BashTool` with `Jail=true` and confirm the command executes and prints the file content.

**Validation:**
- `go vet ./...` (no new issues)
- `staticcheck ./...` (only pre-existing warnings unrelated to the change)
- `gocognit -over 15 .` (only pre-existing over-budget functions)
- `gocyclo -over 12 .` (only pre-existing over-budget functions)
- `go test -count=1 -race -cover ./...` (all pass)
- Replayed the exported session's `jail_violation` commands; the false-positive command (`tools/longline_diag_test.go` heredoc) no longer triggers jail.


### Scroll after first message does not work (fixed)
**Problem:** After the first message in a new session, content above the current viewport was not available in scrollback — only blank lines appeared. The user could not scroll back to see earlier content.

**Root cause:** `Compositor.emitViewportScroll` had two branches: a gap-content branch for large viewport advances (`scroll > height`) and a bare-newline branch for small advances. Both assumed there was a prior full viewport on screen that could be pushed into scrollback. On the first scroll of a session, the previous viewport was empty or only partially filled, so the bare newlines pushed blank rows into scrollback and the gap-content branch did not write enough gap lines to populate the history.

**Fix applied:**
1. Added a `firstScrollDone` sentinel to `Compositor` in `tui/compositor.go`.
2. In `emitViewportScroll`, when `!firstScrollDone`, write the entire canvas from the top with `\r\n` so the terminal naturally scrolls and every line (including the scrolled-off ones) is recorded in scrollback. The subsequent CUP loop in `writeDifferential` redraws the visible rows, so the result is equivalent to a full-frame write for the first scroll only.
3. After the first scroll, `firstScrollDone` is set to true and the existing large/small scroll branches take over.
4. Refactored `emitViewportScroll` into helpers (`emitFirstScroll`, `emitLargeScroll`) to keep cognitive/cyclomatic complexity within budget.

**Test approach:**
- Added `TestChatFirstScroll_PopulatesScrollbackWithScrolledOffLines`, which renders a small initial message, then appends a message that exceeds the viewport by a few lines. It verifies that an early line from the scrolled-off region (e.g. `first line 1`) and the initial content (`start`) are present in the emulated scrollback.

**Validation:**
- `go vet ./tui/`
- `staticcheck ./tui/`
- `gocognit -over 15 .` (no new violations; `emitViewportScroll` was refactored to stay within budget)
- `gocyclo -over 12 .` (no new violations; `emitViewportScroll` was refactored to stay within budget)
- `go test -count=1 -race -cover ./...` (all pass)
- `go test -count=1 -race ./tui/`
- New test as described above

### thinking loop false positive (fixed)
**Problem:** The loop detector emitted `⚡ [goa-system: warning] Reasoning is repeating — the model may be stuck in a thinking loop.` in session `goa-export-20260703-072830.zip` but the model was not actually in a repeat loop.

**Root cause:** `RecordThinkingDelta` counts every complete newline-terminated line longer than 40 characters. When the model iterates over code structure in the thinking pane, structural elements (function signatures, function calls, braces, JSON/XML tags) repeat legitimately and are counted as "reasoning repeats", causing a false positive.

**Fix applied:**
1. Inspected `goa-export-20260703-072830.zip` and replayed the thinking events through the detector. The false positive was caused by the line `func effectiveInline(skill *skills.Skill, cfg *config.Config) bool {` repeating 4 times, followed by other code-like lines such as `writeFmt(ctx, "Skill '%s' loaded into system prompt.\n", name)`.
2. Added `isStructuralLine` in `core/loopdetector.go` to exclude lines that look like code, JSON, or XML structural elements. The heuristic skips lines that start with:
   - Structural punctuation (`{}[]()<>"'`/`\/`)
   - Common programming-language keywords (`func`, `def`, `class`, `if`, `return`, `import`, etc.)
   - An identifier followed by code syntax (`word(...)`, `x := ...`, `x = ...`, `key: value`)
3. Called `isStructuralLine` in `RecordThinkingDelta` before a line is counted.

**Test approach:**
- Replayed the session export through the detector with the new heuristic; the warning no longer fires.
- Added `TestLoopDetector_ThinkingLoop_IgnoresStructuralLines` which feeds repeated code, JSON, and XML structural lines and verifies they never trigger a warning or interrupt.

**Validation:**
- `go vet ./core/`
- `staticcheck ./core/`
- `gocognit -over 15 .` (no new violations; the new helper was refactored to stay within budget)
- `gocyclo -over 12 .` (no new violations; the new helper was refactored to stay within budget)
- `go test -count=1 -race -cover ./...` (all pass)
- `go test -count=1 -race ./core/`
- New tests as described above
- Session export `goa-export-20260703-072830.zip` no longer triggers a false positive

### Spinner not removed at end (fixed)
**Problem:** The spinner seemed to be stuck at the bottom (not moving) after the end of a conversation — a display artifact that disappeared when the cursor moved in the input line.

**Root cause:** `StatusMsg.Show()` contained a guard that checked the closed `done` channel, but `StatusMsg.Clear()` immediately set `s.done = nil`, so the guard was dead code. Late events (e.g. `EventProgress` after `EventEnd`) could call `Show()` again and re-start the spinner after `handleSessionEnd` had already cleared it.

**Fix applied:**
1. Added a `cleared` sentinel to `StatusMsg` in `tui/status.go`. `Clear()` now sets `cleared = true`; `Show()` is a no-op while `cleared` is set. This makes the guard actually effective.
2. Added `StatusMsg.Reset()` to clear the sentinel and drain the closed `done` channel so a fresh spinner can be started on the next turn.
3. Called `subs.statusMsg.Reset()` in `internal/app/submithandler.go` before `showSendingStatus()` so that user submissions after a session end correctly start the status spinner again.
4. `handleSessionEnd` at `internal/app/stats.go` already calls `Clear()` then `RequestRender()`.

**Test approach:**
- Updated `TestStatusMsg_ShowAfterClear` to call `Reset()` before the second turn's `Show()`, confirming multi-turn status updates still work.
- Added `TestStatusMsg_ShowAfterClearWithoutResetIsNoOp` to verify that after `Clear()`, `Show()` is a no-op and `Render()` returns `nil` until `Reset()` is called.

**Validation:**
- `go vet ./tui/ ./internal/app/`
- `staticcheck ./tui/ ./internal/app/` (only pre-existing GlobalRegistry deprecation warnings)
- `gocognit -over 15 .` (no new violations)
- `gocyclo -over 12 .` (no new violations)
- `go test -count=1 -race -cover ./...` (all pass)
- `go test -count=1 -race ./tui/ ./internal/app/`
- New tests as described above

### Tool call duplicate (fixed)
**Problem:** The tool-call duplicate detector flagged the second occurrence of a call as a soft duplicate even when a different call occurred in between (e.g. bash call A, edit call B, bash call A). The user expected only truly consecutive duplicates to trigger the soft hint.

**Root cause:** `Agent.budgetOrRepeatSkipMessage` returned the soft-repeat hint when `consecutiveCount >= 2` OR `windowCount >= 2`. The rolling-window count (`windowCount`) therefore triggered the soft hint for non-consecutive repeats such as A, B, A.

**Fix applied:**
1. Changed the soft-repeat condition to `consecutiveCount >= 2` only. The rolling-window guard is now reserved for the hard-loop limit (`windowCount > MaxToolCalls`).
2. Updated the comment in `internal/agentic/agent_budget.go` to document that the soft hint is for consecutive duplicates only and that the rolling window is for the hard-loop guard.

**Test approach:**
- Added `sequenceToolProvider` to emit a configurable sequence of tool-call arguments in a single stream.
- Added `TestAgent_ToolBudget_NonConsecutiveDuplicateNotFlagged` which emits A, B, A and verifies that all three calls execute for real (no guardrail messages).

**Validation:**
- `go vet ./internal/agentic/`
- `staticcheck ./internal/agentic/`
- `gocognit -over 15 .` (no new violations; pre-existing over-budget functions unrelated)
- `gocyclo -over 12 .` (no new violations; pre-existing over-budget functions unrelated)
- `go test -count=1 -race -cover ./...` (all pass)

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

### Tool call color (fixed)
**Problem:** Tool call colors showed `TC:60` in orange even when there was no duplication — color should only be applied in case of actual loop detection.

**Root cause:** `handleToolResult` at `internal/app/stats.go` set `toolCallWarningLevel` to `ToolCallWarning` for ANY `[goa-system]` tool result that was not "budget exceeded" — including informational hints like "this exact tool call was already executed this turn". The level was never reset between turns: once set to `ToolCallWarning`, it persisted for the rest of the session.

**Fix applied:**
1. Reset `toolCallWarningLevel = ToolCallNormal` at the start of each turn in `handleSessionEnd` so TC color is fresh each turn.
2. Differentiated `[goa-system]` messages: "budget exceeded" → `ToolCallStopped` (red), "Loop guardrail" / "identical to the previous" → `ToolCallWarning` (orange), all other informational hints → `ToolCallNormal` (green).

**Validation:**
- `go vet ./internal/app/`
- `staticcheck ./internal/app/` (no new warnings)
- `gocognit -over 15 .` (no new violations)
- `gocyclo -over 12 .` (no new violations)
- `go test -count=1 -race ./internal/app/`

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

### Change of profile is not saved (fixed)
**Problem:** Changing the mode (e.g. coder → coding-posture) was not reflected after a restart — the footer reverted to the config default (`coder`) even though `state.json` correctly held the persisted mode.

**Root cause:** The startup path (`initAgentBundle`) correctly restored the mode from `.goa/state.json` into the live session state, but *every UI surface* (footer, prompt, status, submit/send handlers, workflow handler) read the static config default via `cfg.ActiveMajor()` / `cfg.DefaultModeState()` instead of the live `agentMgr.CurrentMode()`. So the restored mode was invisible. A secondary gap: the bash tool's `Jail` flag is initialised from the config default during tool registration (which runs before `state.json` is loaded), so a persisted SOLO session did not re-enable the jail on restart.

**Fix plan:**
1. Add `subsystems.effectiveModeState()` returning the live session mode (falling back to config when no session is active).
2. Replace every `cfg.ActiveMajor()` / `cfg.DefaultModeState()` read in footer/prompt/status builders (`tui.go`, `stats.go`, `submithandler.go`, `prompt.go`, `events.go`) with the live mode.
3. Re-apply the bash jail from the restored autonomy after the mode registry is wired in `InitSubsystems`.

**Validation:**
- `go vet ./...`
- `staticcheck ./...` (only pre-existing warnings)
- `gocognit -over 15 .` / `gocyclo -over 12 .` (no new violations in edited files)
- `go test -count=1 -race -cover ./internal/app/ ./core/commands/`
- New tests: `TestEffectiveModeState_PrefersLiveSessionMode`, `TestEffectiveModeState_FallsBackToConfig`, `TestInitAgentBundle_RestoresModeFromStateJSON`, `TestInitAgentBundle_FallsBackToConfigWhenNoState`.
- End-to-end PTY restart against the real project (state.json=coding-posture): footer now shows `coding-posture │ SOLO` (was `coder`).

### Local mode size (fixed)
**Problem:** The local model's effective max-tokens size was based on `max_context_length` (the unlikely configured max, e.g. 262144) instead of the real `loaded_context_length` (e.g. 32768). The detection raced with model loading: it was triggered on the state-change that merely marks the *start* of generation, before the model had finished loading, so LM Studio still reported `max_context_length`.

**Root cause:** `AgentManager.maybeRefreshContextWindow` fired on `EventStateChange` (start of generation) rather than on actual model output, and `loaded_context_length` is only reliable once the model is producing tokens.

**Fix plan:**
1. Trigger the one-shot local context refresh on the **first assistant content delta** of a session (`EventContent`, `Role == Assistant`, non-empty text) — the strongest signal that the model is fully loaded and generating. Bare state-change events no longer trigger it.
2. Confirm LM Studio (`loaded_context_length`), llama.cpp `/props` (`default_generation_settings.n_ctx`), and llama.cpp `/v1/models` (`meta.n_ctx`) all report the *loaded* context — they do, so the same "report only after the first delta" logic applies uniformly to all local providers.

**Validation:**
- `go build ./...`, `go test -count=1 -race ./core/ ./provider/`.
- Updated test `TestAgentManager_RefreshContextWindow_OnFirstAssistantDelta`: asserts a state-change does NOT fire the refresh, the first assistant delta DOES, and it is one-shot.

### Cursor input line position (fixed)
**Problem:** The cursor input line position was often incorrect — especially at end of line, where it jumped to the next physical line.

**Root cause:** goa renders the editor full-width and drives the hardware cursor via a zero-width marker. When the cursor sat at the end of a completely full wrapped line, its column equalled the terminal width, so the CUP sequence positioned it one column past the last cell and the terminal wrapped it onto the next line. (pi, by contrast, uses a *visible* highlighted cursor with horizontal padding, so its cursor never reaches the edge.)

**Fix plan:**
1. Add a `width` argument to `Compositor.positionHardwareCursor`.
2. Clamp the cursor column to `width-1` when it would equal `width`, so the hardware cursor stays on the current line (sitting on the last glyph) instead of wrapping.

**Validation:**
- `go vet ./tui/`, `staticcheck ./tui/`, `gocognit`/`gocyclo` (no new violations).
- `go test -count=1 -race ./tui/`.
- New test `TestCompositor_CursorClampedAtFullWidth` (cursor col==width is clamped to width-1).
- Reference cross-checked against `../pi`'s editor (visible-cursor + padding design).

### First long input / Scroll (fixed)
**Problem:** When a single large append exceeded the viewport (e.g. a long first input or a big tool/output block), the lines that scrolled past the top were never written to the terminal, so they were missing from scrollback and the user could not scroll back to them. (opencode avoids this entirely by delegating rendering to a library `CliRenderer`.)

**Root cause:** `Compositor.writeDifferential` advanced the viewport by emitting bare `
` newlines, which push only the previously-visible rows into scrollback. For a large append, the newly-added lines above the new viewport were never on screen, so the bare newlines pushed BLANK rows into scrollback — the gap content was lost.

**Fix plan:**
1. Extract the scroll emission into `Compositor.emitViewportScroll` (keeps `writeDifferential` within the complexity budget).
2. When the viewport advance exceeds one screen (`scroll > height`), after scrolling the previous viewport into scrollback, write every newly-added line above the new viewport (`canvas[firstChanged:newVtop]`) as real content (clear bottom row, write, newline to scroll it into scrollback) so the full transcript is recoverable. Starting at `firstChanged` preserves content that shifted into indices previously covered by stale on-screen rows.
3. The existing "large append must not erase scrollback" invariant (`TestChatLargeAppendScrollsWithoutErasingScrollback`) is preserved — no `\x1b[2J`/`\x1b[3J` is emitted on this path.

**Validation:**
- `go vet ./tui/`, `staticcheck ./tui/`, `gocognit`/`gocyclo` (no new violations after extracting the helper).
- `go test -count=1 -race ./tui/` (all existing scroll/scrollback tests pass).
- New regression test `TestChatLargeAppend_PopulatesScrollbackWithAllLines`: an early line that scrolled off must be present in emulated scrollback.
- Real-terminal (PTY) check: a 40-paragraph long input renders all 40 paragraphs into the terminal stream (visible + scrollback), where previously the middle content was absent.

### Loop catching in thinking (fixed)
**Problem:** The UI showed an obvious thinking loop (the assistant re-emitted the same reasoning paragraph 11+ times in a single turn) but the loop protection never fired.

**Root cause:** `LoopDetector` only tracked *tool-call* repeats (`RecordToolCall`). A thinking/reasoning loop invokes no tool, so it was invisible to the detector and would burn the entire context window.

**Fix plan:**
1. Add thinking-loop tracking to `LoopDetector`: accumulate streamed reasoning, hash each complete newline-terminated line, and count repetitions of significant lines (len ≥ `minThinkLineLen`=40, so short bullets/separators do not false-positive).
2. Add `RecordThinkingDelta(text)` returning `LoopWarning`/`LoopInterrupt` based on the highest line repeat count (defaults warn 4 / interrupt 6, configurable via `LoopDetectorConfig`).
3. Add `ResetThinking()` and call it from `AgentManager.finalizeTurn()` so each turn is evaluated independently.
4. Wire `RecordThinkingDelta` into `recordContentEvent` for thinking deltas, routed through a new `handleThinkingLoopWarning` that flashes a warning but interrupts (cancels the turn) at the interrupt level.

**Validation:**
- `go vet ./core/...`, `staticcheck ./core/...` (no new warnings), `gocognit`/`gocyclo` (no new violations).
- `go test -count=1 -race ./core/`.
- New tests: `TestLoopDetector_ThinkingLoop_DetectsRepeatedParagraph`, `TestLoopDetector_ThinkingLoop_IgnoresShortLines`, `TestLoopDetector_ThinkingLoop_StreamedAcrossDeltas`, `TestLoopDetector_ResetThinking_ClearsAccumulation`, `TestAgentManager_ThinkingLoopInterrupts`.

### SmartSearch should return matching lines of code (fixed)
**Problem:** SmartSearch returned ranked file candidates but not the matching lines of code, so the agent could not act on the results the way it can with normal `/search`.

**Root cause:** `formatResults` only printed `N. [score] path (lines)` — no source lines were ever read or shown. Additionally, the `SmartSearchRenderer.formatFileLines` regex only matched file-result lines and silently dropped the matching content lines.

**Fix applied:**
1. Tokenise the natural-language query with the same `bm25.CodeTokenizer` the index uses (`extractQueryTerms`), so the grep stage looks for the same units BM25 ranked on.
2. For each ranked candidate (highest score first) build a case-insensitive alternation regex of the query terms and grep the file (`buildMatchingLines` / `grepFile`), bounded by `linesPerCandidate` (3) and an overall `smartMatchBudget` (30).
3. Render the matches as `    <line>: <content>` under each result, mirroring the search tool's `formatFileContentLines`.
4. Update the tool description to advertise matching lines.
5. **Renderer fix:** Updated `SmartSearchRenderer.formatFileLines` to pass through matching content lines (indented `line: content` lines) with proper formatting, matching the search renderer's behavior.

**Validation:**
- `go vet ./tools/`, `staticcheck ./tools/` (no new warnings), `gocognit`/`gocyclo` (no new violations).
- `go test -count=1 -race ./tools/`.
- New tests: `TestSmartSearchTool_ReturnsMatchingLines`, `TestExtractQueryTerms_DedupesAndFilters`, `TestSmartSearchRenderer_MatchingLines`.
- Real output verified by running the tool against this repo: ranked files now show numbered matching lines.

### Scroll during streaming (fixed — see "First long input / Scroll" above)
The core symptom (scroll content not correctly populated) shared the same root cause as the large-append gap: newly-added lines above the viewport were never written, so scrollback was incomplete. The `emitViewportScroll` gap-content fix addresses it. Incremental line-by-line streaming already used the small-scroll differential path (no gap, no scrollback erase), and the large-append path no longer drops content. The pre-existing `TestChatLargeAppendScrollsWithoutErasingScrollback` invariant (no `\x1b[3J` scrollback wipe on appends) is preserved, so retries/re-renders during streaming do not erase history.
