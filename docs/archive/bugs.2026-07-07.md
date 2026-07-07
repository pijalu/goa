<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Archived Bugs — closed 2026-07-07

Six bugs closed in this session. Each was reproduced/verified with a
regression test that fails without the fix, and the full quality gate
(`go vet`, `staticcheck`, `gocognit -over 15`, `gocyclo -over 12`,
`go test -count=1 -race -cover ./...`) passes with no new violations.

## 1. Orchestration spinner stays visible after the run finishes — FIXED

**Root cause (verified):** `EvSourceFinished` (translated from
`EventRunFinished`) fell through to the `default` case in
`handleOrchViewEvent`, which only updates the persistent view and never
clears the shared `statusMsg`. The per-agent `EvAgentFinished` clear
proposed in the original plan was unnecessary and would have flickered
with concurrent agents.

**Fix:** Added an explicit `case orchpanel.EvSourceFinished` in
`internal/app/orchestrator_view_forwarder.go` that calls
`statusMsg.Clear()` (no `SessionEnd()` guard — `EvSourceFinished` is the
last event of a run and `EvAgentStats` does not touch the spinner, so a
plain `Clear()` suffices without requiring a matching `Reset()`).

**Tests:** `TestOrchestratorTabs_SpinnerClearsOnRunFinish` (fails without
the `Clear()`).

## 3. Pending-input message box missing on non-Conversation tabs — FIXED

**Root cause (verified):** The prompt was added only as a chat system
message (suppressed on Stats/agent tabs) and as the editor title
(`updateOrchInputPrompt` overwrote it on every orchestration event
without checking `pendingInput`). A dormant `pendingMsgs *tui.StatusMsg`
slot existed but was never shown.

**Fix:** Introduced a minimal `PendingInputBox` component (single static
line, nil/zero-height when empty — no spinner, no blank-line padding) in
`tui/pending_input_box.go`, wired into the tree in place of the dormant
slot. `requestMainInputWithCancel` sets the box prompt; `clearMainInputRequest`
clears it and restores the title via `updateOrchInputPrompt`. `updateOrchInputPrompt`
early-returns while `pendingInput != nil` so the title is not clobbered.

**Tests:** `TestOrchestratorTabs_PendingInputBoxSurvivesTabSwitch` (asserts
the `PendingInputBox` node shows the prompt on Conversation AND Stats tabs,
that `ChatViewport` is absent on Stats, that the title is not clobbered, and
that cancel removes the box; fails without the box).

## 4. Orchestrator stats wrong / missing cache hit / header not padded — FIXED

**Root cause (verified):** `cacheField` rendered only `CacheRead` as a raw
count (so providers reporting only `CacheCreation` showed `-`); `truncField`
used `len()` instead of the existing `visibleLen` (same file, unused); the
header/footer were `clip`ped but not padded to width; `AgentEnhancedRow`
lacked a `CacheCreation` field.

**Fix:**
- Added `CacheCreation int` to `AgentEnhancedRow` and populated it from
  `AgentStatsDelta.CacheCreation` in `applyRowEv`.
- Extracted the cache-hit formula to a new `internal/metrics` package
  (`CacheHitPct`) as the single source of truth (avoids the
  `internal/app` ↔ `tui/orchestrator` import cycle); `internal/app/stats.go`
  now calls `metrics.CacheHitPct`.
- `cacheField` renders a cache-hit **percentage** (`-` only when no cache
  activity; `0%` for writes-without-reads).
- The aggregate footer shows `CH=<pct>%`.
- `truncField` now uses `visibleLen` and truncates at rune boundaries
  preserving ANSI.
- Added `padToWidth`/`fit` helpers; every stats line is fitted to exactly
  `width` (header, objective, rows, footer, nav hint).

**Tests:** `TestRenderStatsTable_CacheHitPercentage`,
`TestAgentContent_HeaderPaddedToWidth`, `TestStatsTable_TruncFieldUsesVisibleWidth`,
`TestStatsTableHelpers` (cacheField cases), `TestCacheHitPct`.

## 6. Completed tool calls stay pinned to the bottom — FIXED

**Reproduction finding:** the normal sequential flow already worked
chronologically (a completed tool is above a subsequently-appended
assistant message). The genuine gap was the **interleaved** case: a
running tool that is not the last entry should pin to the bottom.

**Fix:** Two-zone render in `ChatViewport.fullRebuild` — inactive entries
first (chronological), then active entries (running/pending tools,
chronological). `isEntryActive` checks **only** tool status
(`ToolRunning`/`ToolPending`); streaming thinking/assistant blocks are
deliberately NOT active so concurrent streams stay in chronological order
(the existing `TestOrchestratorConversation_TwoAgentsConcurrentThinking`
behavior). The streaming fast path (`updateLastEntry`) is preserved via a
new `lastEntryAtBottom` guard that forces a full rebuild whenever the last
entry would not render at the bottom. `fullRebuild` was refactored to
extract `appendEntry` to stay under the cognitive-complexity budget.
Implemented entirely within `ChatViewport` — no app-layer changes.

**Tests:** `TestChatViewport_ActiveItemsPinnedToBottom`,
`TestChatViewport_RunningToolPinnedToBottomWhenNotLast` (fails without the
active-zone logic), `TestChatViewport_StreamingFastPathPreserved`.

## 7. Input editor title should not end with ':' — FIXED

**Fix:** `Editor.SetTitle` now normalizes via `normalizeEditorTitle`
(trims whitespace and strips a single trailing colon). The orchestration
steer prompts (`orch_tabs.go`) no longer carry a trailing colon; the chat
system message and `pendingInput.prompt` retain the full text.

**Tests:** `TestEditor_SetTitle_StripsTrailingColon`,
`TestEditor_Render_TitleHasNoTrailingColon`; updated
`TestOrchestratorViewForwarder_SteerPromptReflectsActiveTab` and the
picker test to expect colon-free titles.

## 8. Orchestrator tool widgets/status only show "run tool" — FIXED

**Root cause (verified, plan corrected):** The original plan proposed
building a descriptive label inside `genericRenderer.RenderCall`, but
`RenderContext` carries no tool name, so that approach is impossible
without an interface change. The correct minimal fix: the generic renderer
returns `""`, which lets the existing `ToolExecutionComponent.updateBox`
fallback render `<bold>toolName</bold> <formatted-args>` (via the existing
`FormatToolArgs`, which already produces clean summaries like `foo.go`,
`npm test`, `pattern in path`). Dedicated renderers (read/write/bash/etc.)
were already fine.

**Fix:** `genericRenderer.RenderCall` returns `""`; `handleAgentToolCall`
sets the status to `state.label + " tool calling: " + name`.

**Tests:** `TestToolExecution_GenericRendererShowsNameAndArgs`,
`TestOrchestratorTabs_ToolCallShowsNameInStatusAndWidget`.

## 2. Refresh jitters / input zone jumps up and down — FIXED

**Reproduction (workflow step 1):** `TestUI_NoJitter_StatusToggleDoesNotShiftInput`
drove `EventStateChange(Thinking)` → `EventEnd` and showed the input editor's
`Rect.Y` shift from 15 (status shown) to 12 (status cleared) — a 3-row jitter
caused by `StatusMsg.Render` returning `["", line, ""]` (3 lines) when shown and
`nil` (0 lines) when cleared.

**Root cause (verified):** the visible jitter was the StatusMsg height toggle
(original plan point 1). The "excessive full redraws" (point 2) were a
*consequence* of that toggle: the 3→0 row shrink at every turn end tripped
`clearOnShrink`, emitting a full `\x1b[2J` wipe each turn. The MVC culling
(point 4) is a perf optimization, not a visible-jitter cause — hidden views
already return `nil` (no layer), so the compositor never diffs them.

**Fix:** `StatusMsg.Render` now reserves a stable single-line slot — the spinner
line when active, a blank line when idle — so `Show`/`Clear` never changes the
layer height. This removes the turn-boundary shrink, which also eliminates the
`clearOnShrink` full redraws. The `clearOnShrink` safety net itself was left in
place (it now only fires on genuine shrinks, e.g. tall→short content, which is
correct). `TUI.FullRedrawCount()` was exposed for diagnostics.

**Tests:** `TestUI_NoJitter_StatusToggleDoesNotShiftInput` (fails without the
fix: 15→12), `TestStatusMsg_StableHeight`,
`TestUI_NoJitter_NoExcessiveFullRedrawsDuringStreaming`,
`TestUI_NoJitter_TabSwitchNoExcessiveFullRedraw`,
`TestUI_NoJitter_TabSwitchFromTallChat` (all confirm ≤1 full redraw). Existing
status-render tests updated for the single-line format.

**Deferred (documented, not visible-jitter causes):** MVC culling of hidden
ChatViewport cache mutations (point 4) and pinning the input editor to the
bottom across tab switches (point 3 — different tabs show different-height
content, which is expected). The AgentContent height difference on tab switch
does not trigger excessive redraws (verified).

## 5. Reviewer output is not consumed by the orchestrator — FIXED (robustness)

**Diagnosis corrected (original was disproven by tracing the byte path):**
`OrchestratorDelegateTool.ExecuteContext` returns the specialist's `Message()`
as the tool result; `Agent.completeStreamTurn` feeds it back and streams
another round (`turnContinues`); `hub_orchestrator.md` already instructs
synthesis; `Delegate` stores outputs via `MessageFor`. So the orchestrator
*does* see specialist outputs within its turn. The observed "no synthesis" is
model behaviour (small local models stop after delegating).

**Fix:** added a guaranteed **synthesis turn** for hub topology. After the
orchestrator's delegation turn, `runHub` calls `synthesize`, which collects
every specialist's `MessageFor` output into a `hub_synthesis` prompt and
re-drives the orchestrator. It is skipped when the orchestrator already
produced a final message (the model followed the prompt) — so capable models
are not double-run. `driveOne` was refactored into `renderRolePrompt` +
`acquireAndRun` (shared lifecycle for both turns), keeping resume / goal-token
/ cancellation semantics intact.

**Tests:** `TestRuntime_HubSynthesizesSpecialistOutputs` (synthesis prompt
inlines both specialists' outputs; a synthesis `EventAgentMessage` is emitted;
orchestrator started twice; fails without the `synthesize` call),
`TestRuntime_FanoutDoesNotSynthesize` (fanout never synthesizes),
`TestRuntime_DelegateRoundTrip` updated (fake orchestrator delegates on turn 1,
synthesizes on turn 2; coder runs once).

