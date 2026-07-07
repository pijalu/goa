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

# Open Bugs

## 1. Orchestration spinner stays visible after the run finishes

**Observed:** After the orchestration banner `orchestration daring.hawk finished` appears, the status spinner still shows `◠ orchestrator answering...` (or `◡ ...`). It should be cleared when the run is complete.

**Reason:** `StatusMsg` is driven by per-agent `EvAgentMessage`/`EvAgentThinking` chunks in `internal/app/agent_streams.go`. When an agent finishes, `handleOrchViewEvent` calls `endAgentStream()` but never clears the shared `statusMsg`. `EvSourceFinished` also only updates the persistent view, not the spinner. The `SessionEnd()` guard that normally clears the main-agent spinner is only invoked on the foreground agent path (`handleSessionEnd`), not on orchestration runs.

**Fix plan:**
1. In `internal/app/orchestrator_view_forwarder.go`, extend `handleOrchViewEvent` for `EvAgentFinished` to clear the spinner when the finished agent is the one currently referenced by the status text.
2. In the `EvSourceFinished` case, unconditionally clear the status message and reset the spinner guard so the next run can start fresh.
3. Keep the guard: do not let late `EvAgentStats` events re-start the spinner after the source finished.
4. Add a helper `statusMsg.ClearIfTextContains(label)` or similar so we don't clear unrelated status.

**Test plan:**
1. Add a filmstrip test in `internal/app/orchestrator_tabs_filmstrip_test.go` that replays `lifecycleEvents()` and asserts `StatusTrace()` ends with an empty string after `EventRunFinished`.
2. Assert that the final visible frame contains the finished banner but NOT a spinner line containing `answering`.
3. Run the full existing orchestrator test suite to ensure no regression in mid-run spinner behavior.

---

## 2. Refresh jitters / input zone jumps up and down

**Observed:** During streaming and tab switches the screen "tears": the input zone and footer move up and down. Complete redraws seem to fire too often.

**Reason:**
1. `StatusMsg.Render()` returns `[]string{"", line, ""}` (two blank lines around the status). When `Show`/`Clear` toggles, the height of the status layer changes by two rows, pushing the input editor and footer up and down.
2. The `Compositor` currently does a full redraw on terminal shrink (`clearOnShrink`), on width/height changes, and whenever an overlay is open and content changes above the viewport (`needsFullRedrawForChange`). This produces visible full-screen flashes.
3. Switching between Conversation and Stats tabs changes the height of the `AgentContent` layer (nil vs. a full table), which also shifts the input zone.
4. **MVC violation:** The model (chat conversation, stats rows, agent streams) is updated while the corresponding view is hidden, and those hidden-view updates are still transformed into `Scene` layers and diffed by the `Compositor`. The Compositor should never turn a model into a view layer unless that view is active.

**Fix plan:**

The guiding principle is strict MVC separation for tabbed multi-agent views:
- **Model** may change at any time (chat entries appended, stats rows updated, agent streams accumulated).
- **View rendering** only happens when the view is active/visible.
- The **Compositor** must not receive a layer for a hidden view and therefore must not spend work diffing or emitting escape sequences for it.

1. Make `StatusMsg.Render()` always return a fixed-height layer. Use a single-line compact render and, if the design needs vertical padding, reserve that padding permanently rather than toggling it. Remove the `compact` branch so the height is stable.
2. **Enforce MVC culling in tabbed views:**
   - `ChatViewport` already returns `nil` when `suppressed`, but its internal render cache and `generation` counter still mutate on every chat event while hidden. Change it so that while `suppressed` is true, model updates are still accepted, but the **view state** (render cache, dirty flags, generation) does not advance. The view cache is only rebuilt when the tab becomes visible again.
   - `AgentContent` returns `nil` for the Conversation tab and renders the Stats tab. For the Stats tab, it should only invalidate when `EvAgentStats`, `EvAgentStarted`, or `EvAgentFinished` events arrive—not when hidden chat messages or thinking chunks are processed.
   - Generalize: add a `Visible() bool` or `Active() bool` method on components; the TUI engine's `buildScene` should still call `Render()` (so a component can decide to return nil), but hidden components must also avoid marking themselves dirty and avoid propagating `RequestRender` when their model changes while hidden.
3. In `tui/compositor.go`, audit the full-redraw triggers:
   - Keep full redraw on terminal size change (unavoidable) but avoid re-emitting `\x1b[2J` when the canvas shrank by only a few rows; instead use absolute CUP per line to erase stale trailing rows.
   - Remove or tighten `clearOnShrink` default; shrink should be handled by clearing trailing rows with `\x1b[K` at the new bottom, not a full screen wipe.
   - For overlays, only force a full redraw when the overlay itself changes, not when base content above the viewport changes.
4. Make `AgentContent` return a stable height when attached (e.g., a blank placeholder line) even on the Conversation tab so the total base-layer height does not change on tab switch. Alternatively, keep the tab bar immediately above the input and render the stats panel as an overlay rather than a replacement layer.
5. Ensure every layer is padded/truncated to exactly its declared width so the Compositor does not need to emit extra line clears.

**Test plan:**
1. **MVC validation test:** switch to the Stats tab, then feed a series of `EventAgentMessage`/`EventAgentThinking` events for the hidden Conversation tab. Assert that:
   - `ChatViewport` model state grew (entries added) but `Render()` returns the same cached lines and does not recompute them.
   - The `Compositor.FullRedrawCount()` does not increase.
   - The visible `AgentFrame` diff shows no added/removed lines from the chat content.
2. Add a `TestCompositor_NoUnnecessaryFullRedraws` unit test that feeds a streaming sequence and asserts `FullRedrawCount()` is only incremented on first frame and size changes.
3. Add a `TestStatusMsg_StableHeight` test that checks `len(Render())` is identical for hidden, shown, and cleared states.
4. Add a filmstrip regression test in `internal/app/orchestrator_tabs_filmstrip_test.go` that captures frames across tab switches (Conversation → Stats → coder) and asserts the input editor node's `Rect.Y` does not change by more than one row between frames.
5. Verify the exported session in the user's log (`goa-export-20260707-073733.zip`) shows no `\x1b[2J` after the first frame; add a golden/byte check if needed.

---

## 3. Pending-input message box is missing on non-Conversation tabs

**Observed:** The "Describe the issue (optional), then press Enter:" box appears in the chat on the Conversation tab but disappears when the user switches to Stats or an agent tab.

**Reason:** `requestMainInput()` in `internal/app/app.go` adds the prompt as a system message via `chat.AddSystemMessage(prompt)` and sets the editor title. The system message lives inside `ChatViewport`, which is `SetSuppressed(true)` on the Stats tab. The editor title alone is not the same as the message box.

**Fix plan:**
1. Introduce a small persistent `PendingInputBox` component (or reuse a lightweight banner) that renders the current pending prompt above the input editor whenever `a.pendingInput != nil`, independent of the chat viewport.
2. Keep the chat system message for historical context, but make the visible prompt box come from the new component so it survives tab switches and chat suppression.
3. Clear the banner in `clearMainInputRequest()` alongside the existing cleanup.
4. Ensure the banner is part of the base layer tree so it participates in the normal layout but does not shift other components when the prompt is empty (return nil when no pending input).

**Test plan:**
1. Add a filmstrip test that:
   - Registers a pending input with `app.requestMainInput("Describe the issue...", func(string){})`.
   - Captures a frame on the Conversation tab and asserts the prompt text is visible.
   - Switches to the Stats tab (`selectAgentTab("stats")`) and asserts the prompt text is still visible and the `ChatViewport` node is absent.
2. Verify that cancelling the pending input removes the banner on all tabs.

---

## 4. Orchestrator stats are wrong / missing cache hit / header not padded / table formatting off

**Observed:**
- The `CH` column is `-` for every row and the aggregate footer shows `CH=0` even when the provider reports cache usage.
- The header `orchestration · complete` is not padded to the full panel width.
- The table layout is requested but column widths and truncation look off (uses `len()` instead of visible width).

**Reason:**
1. In `core/orchestrator/handle.go`, `AgentStats.AddUsage()` receives `cacheRead` and `cacheCreation` correctly from the adapter, but the displayed aggregate in `AgentContent.renderStats()` prints `CH=%d` using `CacheRead`. If the provider only reports `CacheCreation` (write) tokens and not read tokens, `CacheRead` is zero.
2. `tui/orchestrator/stats_table.go` renders `CH` as a raw token count and shows `-` when zero. The user expects a cache *hit* metric, which should be a percentage or at least include both read and creation.
3. `AgentContent.renderStats()` builds the header with `headerLine()` but does not pad it to `width`, so the background/border does not span the panel.
4. `truncField()` in `tui/orchestrator/render_helpers.go` uses `len(s)` instead of `visibleLen(s)`, so ANSI-bearing and multi-byte strings are truncated too early.

**Fix plan:**
1. Change the aggregate footer to compute and display a cache-hit percentage: `CH = (CacheRead + CacheCreation) / (CacheRead + CacheCreation + net prompt tokens) * 100` or use the same formula as `internal/app/stats.go` (`computeCacheHitPct`).
2. Render the `CH` column as a percentage as well (e.g., `CH 45%`) or as read+creation with the percentage in the footer. Keep the existing `-` placeholder when truly zero.
3. In `AgentContent.renderStats()`, pad every output line to `width` using the existing `padToWidth` style helper so the header spans the panel.
4. Fix `truncField()` to use `visibleLen(s)` for the width check and truncate at grapheme boundaries.
5. Add a `RenderStatsTable` test with rows containing `CacheRead` and `CacheCreation` to verify both appear in the aggregate and percentage calculation.

**Test plan:**
1. Add `TestRenderStatsTable_CacheHitPercentage` in `tui/orchestrator/content_test.go` that feeds rows with `CacheRead=500`, `CacheCreation=100`, `TokensIn=400` and asserts the output contains `CH` with a non-zero value and the footer contains a percentage > 0.
2. Add `TestAgentContent_HeaderPaddedToWidth` that renders the Stats tab and asserts the header line's visible length equals the requested width.
3. Add `TestStatsTable_TruncFieldUsesVisibleWidth` with ANSI-colored strings to ensure truncation is width-aware.
4. Run the existing orchestrator view tests and update golden assertions if the `CH` format changes.

---

## 5. Reviewer output is not consumed by the orchestrator

**Observed:** The `reviewer` agent produced a detailed table of actionable feedback. After it finished, the orchestrator simply reported `■ orchestrator ok` and the run ended. There is no visible orchestrator thinking or follow-up delegation acting on the review.

**Reason:** In hub topology (`core/orchestrator/runtime.go` `runHub`), the orchestrator role runs a single turn and uses `DelegateTool` to dispatch work. The orchestrator's context does not include the streamed outputs of the delegated specialists by default, so it cannot synthesize or act on them. In pipeline topology, the pipeline only carries the immediately preceding role's output forward; there is no final synthesis step. The fanout topology runs roles in parallel and never feeds their outputs back anywhere.

**Fix plan:**
1. Extend `Runtime` with a `Synthesize` phase after all managed agents complete:
   - Collect the `Message()` of every finished non-orchestrator agent into a structured context block.
   - If an `orchestrator` role is configured, run a second orchestrator turn with a prompt that includes the objective + each specialist's output + a system prompt asking it to produce a plan or further delegations.
   - If no orchestrator role is configured, append a system synthesis message to the run events so the user sees the combined output.
2. For pipeline topology, optionally add a final synthesis stage after the last configured stage.
3. Surface this synthesis turn in the TUI as `orchestrator` thinking/content events so the user sees it is working.
4. Ensure the synthesis turn respects the same token budget and cancellation semantics as normal turns.

**Test plan:**
1. Add an integration test in `core/orchestrator/runtime_test.go` with a hub topology that has `orchestrator`, `coder`, and `reviewer` roles. Use fake agents whose `Run` returns review text. Assert that the orchestrator's second prompt contains the review text and that the run emits a synthesis `EventAgentMessage` or `EventAgentThinking` after the reviewer finishes.
2. Add a filmstrip test in `internal/app/` that drives a hub run to completion and asserts the visible sequence includes `[reviewer] ...` followed by `[orchestrator] ...` synthesis content, not just `■ orchestrator ok`.
3. Verify the existing `Runtime` tests still pass and that the synthesis step does not run for fanout topology unless explicitly configured.

---

## 6. Completed tool calls stay pinned to the bottom of the conversation

**Observed:** In a conversation, tool widgets and streaming blocks remain at the bottom even after they complete, instead of moving into the scrollback and letting new content appear at the bottom.

**Reason:** `ChatViewport` appends every entry in chronological order and never re-orders. A `ToolExecutionComponent` is inserted when `EventToolCall` arrives and stays at that index even after `EventToolResult` finalizes it. The user wants the *active* items (streaming thinking, streaming assistant message, running/pending tool calls) to always render at the bottom, while completed items remain in historical order.

**Fix plan:**
1. Introduce a concept of "active" entries in `ChatViewport`/`Conversation`:
   - An entry is active if it is a thinking block whose content is still being updated, an assistant message whose content is still streaming, or a tool widget whose status is `ToolRunning`/`ToolPending`.
   - Completed entries become inactive.
2. Split the render into two zones:
   - Historical zone: all inactive entries in insertion order.
   - Active zone: all active entries rendered after the historical zone, also in their relative order (oldest active first).
3. When an entry transitions from active to inactive, it moves from the active zone to the historical zone. This must preserve the per-entry render cache and line offsets.
4. Ensure the status spinner still reflects the topmost active item.
5. Keep the existing `AgentFilter` and `Suppressed` behavior working; the active zone should respect the filter too.

**Test plan:**
1. Add a `TestChatViewport_ActiveItemsPinnedToBottom` unit test in `tui/chat_viewport_test.go` that:
   - Adds a completed user message.
   - Adds a tool call widget.
   - Asserts the tool widget is rendered at the bottom of the visible frame.
   - Finalizes the tool with `SetStatus(ToolSuccess)` and asserts the widget moves up into the historical zone and a subsequent assistant message appears at the bottom.
2. Add a filmstrip test in `internal/app/ui_scenario_regression_test.go` that replays a real `EventToolCall` → `EventToolResult` → `EventContent` sequence and asserts the diff shows the tool result moving up and the new assistant content at the bottom.
3. Verify active streaming thinking blocks stay at the bottom until `EventEnd` or a state change finalizes them.

---

## 7. Input editor title should not end with ':'

**Observed:** When `requestMainInput("Describe the issue (optional), then press Enter:", ...)` is used, the editor title renders as `┨ Describe the issue (optional), then press Enter: ┠`. The trailing colon conflicts visually with the separator brackets.

**Reason:** `requestMainInputWithCancel` passes the raw prompt to `inp.SetTitle(prompt)` without trimming trailing punctuation. The editor title renderer (`renderTitledBorder`) always adds the brackets.

**Fix plan:**
1. In `internal/app/app.go` `requestMainInputWithCancel`, normalize the prompt before passing it to `SetTitle`:
   - Strip trailing whitespace.
   - Strip a single trailing colon (`:`) or a trailing colon+space (`: `) if present.
2. Alternatively, do the normalization inside `tui.Editor.SetTitle` so all callers benefit.
3. Keep the full prompt text in the chat system message and in the `pendingInput.prompt` field; only the visual title is normalized.

**Test plan:**
1. Add a unit test in `tui/editor_render_test.go` or `tui/editor_extra_test.go` that calls `SetTitle("issue:")`, renders the editor, and asserts the visible title does not end with `:`.
2. Add an integration test in `internal/app/app_test.go` that calls `requestMainInput("Describe the issue:", ...)` and asserts `inp.Title()` equals `"Describe the issue"`.
3. Verify existing tests with prompts like `"Replace active goal with objective (ctrl-c to cancel)"` still pass.

---

## 8. Orchestrator tool widgets/status only show "run tool" with no tool name or details

**Observed:** When a sub-agent in an orchestration run calls a tool, the widget header shows the generic `run tool` label and the spinner status shows `coder tool calling`. The actual tool name (e.g., `read`, `write`, `bash`) and key arguments (path, command, etc.) are not visible until the tool finishes and expands.

**Reason:**
1. `tui/tool_renderer.go` `genericRenderer.RenderCall()` always returns the literal string `run tool` instead of deriving a label from the tool name and arguments.
2. `internal/app/agent_streams.go` `handleAgentToolCall()` sets the status message to `state.label + " tool calling"` and never appends the tool name or argument summary.
3. `ToolExecutionComponent` already receives `toolName` and `toolArgs` in `NewToolExecution`, but the generic renderer ignores them.

**Fix plan:**
1. Update `genericRenderer.RenderCall()` to build a descriptive label from the actual tool name and arguments:
   - Use `args["path"]`, `args["command"]`, `args["pattern"]`, etc. to produce a short summary.
   - Keep it under ~40 visible columns when collapsed; truncate with `…` if needed.
   - Format examples: `read src/main.go`, `write README.md`, `bash npm test`, `search TODO in src/`.
2. In `handleAgentToolCall`, include the tool name in the status message: `state.label + " " + name + " ..."` or `state.label + " tool calling: " + name`.
3. Ensure the change applies to both orchestration agents (`handleAgentToolCall`) and the main-agent path (`handleToolCall` in `stats.go`), if the main-agent path has the same generic fallback.
4. Keep the existing per-tool renderers (bash, read, write, etc.) unchanged; they already provide custom labels. Only change the generic fallback.

**Test plan:**
1. Add a unit test in `tui/tool_execution_test.go` that creates a `ToolExecutionComponent` with `name="read"` and `argsJSON='{"path":"foo.go"}'` and asserts the rendered header contains `read` and `foo.go` (not just `run tool`).
2. Add a filmstrip test in `internal/app/orchestrator_tabs_filmstrip_test.go` (or `orchestrator_conversation_render_test.go`) that replays an `EventAgentToolCall` with `tool=bash` and `input='{"command":"go test"}'` and asserts the visible frame contains `bash` and `go test` in the tool widget and the status trace contains `bash`.
3. Verify the main-agent tool path still renders tool-specific labels correctly and only the generic fallback changes.

---

<!-- New bugs should be added above following the workflow above. -->
