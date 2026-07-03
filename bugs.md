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

# TODO

## Spinner not removed at end
**Problem:** The spinner seems to be stuck at the bottom (not moving) after end of the conversation - likely a display artifact - moving the cursor in the input make it disappear.

**Root cause hypothesis:** `handleSessionEnd` at `internal/app/stats.go:374` calls `subs.statusMsg.Clear()`, but the `Clear()` only sets `s.spinning = false` and stops the ticker. If a race exists between the ticker goroutine calling `tickFrame` on the command loop and the TUI render cycle (which reads `frameIdx` via `SpinnerText()`), the last frame may render the spinner character instead of the static `◆`. Alternatively, `IsVisible()` returns `true` as long as `s.text != ""`, and `Clear()` sets `s.text = ""` — but if something sets the status text again AFTER `handleSessionEnd`, the spinner would reappear.

**Fix plan:**
1. Add a guard in `handleSessionEnd` that also resets the footer activity to "" and explicitly sets `SetModelBusy(false)` (already done).
2. Check if any other code path calls `statusMsg.Show()` after `handleSessionEnd` — e.g., `handleProgressEvent`, `handleStateChange` with a late state.
3. Add a `done` channel check in `StatusMsg.Show()` to prevent re-starting the spinner after Clear.
4. Inspect the render path: if `s.text != ""` but `s.spinning == false`, `Render` returns `"◆"` prefix — the fixed diamond, not a spinner frame. The diamond could be confused with a frozen spinner if the text lingers.

**Test approach:**
- Unit test: verify that after `Clear()`, `SpinnerText()` returns `"◆"` (not a spinner frame).
- Unit test: verify that `Show()` after `Clear()` restarts the spinner correctly.
- Simulate: render a `StatusMsg` post-`Clear()` and confirm the prefix is `"◆"` not a frame character.

**Validation:**
- `go vet ./tui/ ./internal/app/`
- `staticcheck ./tui/ ./internal/app/`
- `go test -count=1 -race ./tui/ ./internal/app/`
- New tests as described above
- PTY check: after conversation ends, the bottom line no longer shows a spinner frame

## Tool call color
**Problem:** Tool call colors show 60 in orange (`TC:60`) but there didn't seem to be duplication so it should stay in a normal color — color should only be applied in case of loop detection.

**Root cause:** `handleToolResult` at `internal/app/stats.go:251-257` sets `toolCallWarningLevel` to `ToolCallWarning` for ANY `[goa-system]` tool result that is not "budget exceeded" — including informational hints like "this exact tool call was already executed this turn". The level is never reset between turns: once set to `ToolCallWarning`, it persists for the rest of the session. The actual issue is twofold:
  1. Some `[goa-system]` hints are informational (e.g. `toolRepeatedMessage` — a soft hint about same args) and should NOT raise the warning color to orange
  2. `toolCallWarningLevel` should be reset at the start of each turn, not persist across the session

**Fix plan:**
1. In `handleSessionEnd` (or a new `handleSessionStart`/`handleStateChange` for the first content state in a turn), reset `a.toolCallWarningLevel = ToolCallNormal`.
2. Differentiate `[goa-system]` messages: budget-exceeded → `ToolCallStopped`, loop/interrupt → `ToolCallWarning`, informational hints (repeated call, truncated result) → keep `ToolCallNormal`.

**Test approach:**
- Unit test: verify `toolCallWarningLevel` resets to `ToolCallNormal` after `handleSessionEnd`.
- Unit test: verify an informational `[goa-system]` result does NOT set `ToolCallWarning`.
- Unit test: verify a loop-warning `[goa-system]` result DOES set `ToolCallWarning`.

**Validation:**
- `go vet ./internal/app/`
- `staticcheck ./internal/app/`
- `gocognit -over 15 .` (no new violations in edited files)
- `gocyclo -over 12 .` (no new violations in edited files)
- `go test -count=1 -race ./internal/app/`
- New/updated tests as described above

## thinking loop false positive
**Problem:** The loop detector emitted `⚡ [goa-system: warning] Reasoning is repeating — the model may be stuck in a thinking loop.` in session `goa-export-20260703-072830.zip` but the model was not actually in a repeat loop.

**Root cause hypothesis:** `RecordThinkingDelta` counts line repeats by hashing complete newline-terminated lines. A false positive can occur when:
  1. The model emits long structured output (e.g., code blocks, JSON, XML) in the thinking pane, where structural elements repeat legitimately (same function signature, same field name).
  2. Streaming causes partial lines that are < `minThinkLineLen` (40) to be skipped, but when the next chunk arrives, the accumulated line crosses the threshold and gets counted — potentially multiple times if it's split across deltas.
  3. The `ThinkingLoopWarning` threshold is 4 — too low for certain thinking patterns (e.g. a model that iteratively refines code in the thinking pane, emitting the same function name 4+ times).

**Fix plan:**
1. Inspect the session export to determine whether the false positive was caused by (a) code-block structure repeating, (b) streaming split artifacts, or (c) a threshold too low for the model's thinking style.
2. If (a) or (b): exclude lines that match code/JSON/XML structure patterns (heuristic: lines starting with common language keywords or brackets).
3. If (c): increase the default `ThinkingLoopWarning` threshold from 4 to 6 (matching the interrupt threshold spread).

**Test approach:**
- Replay the session export through the loop detector to reproduce the false positive.
- Unit test with a repeating code-structure pattern to verify the heuristic excludes structural repeats.
- Unit test with streaming split lines to verify no double-count.

**Validation:**
- `go vet ./core/`
- `staticcheck ./core/`
- `gocognit -over 15 .` (no new violations)
- `gocyclo -over 12 .` (no new violations)
- `go test -count=1 -race ./core/`
- New tests as described above
- Verify the session export no longer triggers a false positive

## Scroll after first message does not work
**Problem:** After the first message in a new session, content above the current viewport is not available in scrollback — only blank lines appear. User cannot scroll back to see earlier content.

**Root cause hypothesis:** The `Compositor.writeDifferential`/viewport advance code handles scrolling for large appends (fixed in the archived "First long input / Scroll" bug), but the first message in a session starts with an empty terminal. The initial content is written directly without a preceding scroll context. If the first message exceeds the viewport height, the content that scrolls off the top may never have been written as physical terminal lines — only as canvas rows. The `emitViewportScroll` fix assumed there was always a prior viewport state to scroll from, but on the very first render the viewport advances from row 0, so there's no previously-shown content to push into scrollback.

**Fix plan:**
1. In `Compositor.writeDifferential` (or `emitViewportScroll`), detect the initial-render case where the previous viewport top was 0 and no content has been scrolled yet.
2. When advancing the viewport for the first time (firstEverRender or previous vtop was 0), write the scrolled-off canvas rows directly as terminal content rather than relying on bare `\n` newlines to push existing on-screen rows into scrollback — because there are no prior on-screen rows.
3. Add a sentinel to `Compositor` tracking whether any scroll has occurred yet.

**Test approach:**
- Reproduce: render a first message that exceeds viewport height, capture the terminal output, check that the scrolled-off lines are present in the scrollback.
- Unit test: `TestChatFirstMessage_ExceedsViewport_PopulatesScrollback` — verify the screen emulator's scrollback contains the early lines.
- Unit test: `TestCompositor_FirstScroll_InitialRender` — verify that the first viewport advance writes rows into scrollback.

**Validation:**
- `go vet ./tui/`
- `staticcheck ./tui/`
- `gocognit -over 15 .` (no new violations in edited files)
- `gocyclo -over 12 .` (no new violations in edited files)
- `go test -count=1 -race ./tui/`
- New tests as described above
- PTY check: a long first message renders all lines accessible via scrollback

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

**Root cause:** `formatResults` only printed `N. [score] path (lines)` — no source lines were ever read or shown.

**Fix plan:**
1. Tokenise the natural-language query with the same `bm25.CodeTokenizer` the index uses (`extractQueryTerms`), so the grep stage looks for the same units BM25 ranked on.
2. For each ranked candidate (highest score first) build a case-insensitive alternation regex of the query terms and grep the file (`buildMatchingLines` / `grepFile`), bounded by `linesPerCandidate` (3) and an overall `smartMatchBudget` (30).
3. Render the matches as `    <line>: <content>` under each result, mirroring the search tool's `formatFileContentLines`.
4. Update the tool description to advertise matching lines.

**Validation:**
- `go vet ./tools/`, `staticcheck ./tools/` (no new warnings), `gocognit`/`gocyclo` (no new violations).
- `go test -count=1 -race ./tools/`.
- New tests: `TestSmartSearchTool_ReturnsMatchingLines`, `TestExtractQueryTerms_DedupesAndFilters`.
- Real output verified by running the tool against this repo (query "loop detector thinking repeat"): ranked files now show numbered matching lines.

### Scroll during streaming (fixed — see "First long input / Scroll" above)
The core symptom (scroll content not correctly populated) shared the same root cause as the large-append gap: newly-added lines above the viewport were never written, so scrollback was incomplete. The `emitViewportScroll` gap-content fix addresses it. Incremental line-by-line streaming already used the small-scroll differential path (no gap, no scrollback erase), and the large-append path no longer drops content. The pre-existing `TestChatLargeAppendScrollsWithoutErasingScrollback` invariant (no `\x1b[3J` scrollback wipe on appends) is preserved, so retries/re-renders during streaming do not erase history.
