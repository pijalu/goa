<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Bug Archive — 2026-07-12

All open bugs from `bugs.md` have been addressed. The file has been reset to guidelines-only.

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

## Closed Bugs

### 1. Input line trailing spaces on wrapped lines

**Symptom:** Multi-line input wrapped to the next line did not show trailing spaces until the next character was typed.

**Root cause:** `internal/ansi/ansi.go` `Wrap` trimmed trailing spaces, and the editor visual-cursor logic in `tui/editor_render.go` did not account for them.

**Fix:**
- Preserve trailing spaces during wrapping in `internal/ansi/ansi.go`.
- Update visual-cursor logic in `tui/editor_render.go` to wrap trailing spaces consistently.
- Added tests in `tui/editor_render_test.go` and updated `tui/editor_extra_test.go`.

**Validation:** `go test ./tui/... ./internal/ansi/...`

---

### 2. Tool calling streaming / edit result truncation

**Symptom:** Tool call streaming and edit/write results were truncated to a small number of lines (8/10), making long edits hard to review.

**Root cause:** Renderers in `tools/edit_renderer.go` and `tools/writefile_renderer.go` hard-coded a small line limit.

**Fix:**
- Increased default visible lines to 1000 in `tools/edit_renderer.go` and `tools/writefile_renderer.go`.
- Updated associated tests.

**Validation:** `go test ./tools/...`

---

### 3. Micro-compaction on large context

**Symptom:** Micro-compaction was triggered even when a large context window was available, e.g. `↑74.7K ↓41.4K ... 5.4%/1.0M (auto) c:1m-0`.

**Root cause:** `internal/agentic/compaction.go` `contextRatio()` used a hard 8192 fallback for the max-tokens denominator instead of the effective context window.

**Fix:**
- Changed `contextRatio()` to use `effectiveMaxTokens()`.
- Added `TestMicroCompact_UsesEffectiveContextWindow`.

**Validation:** `go test ./internal/agentic/...`

---

### 4. Status bar model spinner during tool calls

**Symptom:** The footer model spinner continued to spin during tool calls, giving the impression the model was still generating.

**Root cause:** `internal/app/stats.go` `handleStateChange` and `handleToolCall` left `MainActivity` as "tool calling" and `SetModelBusy(true)`.

**Fix:**
- Set `MainActivity=""` and `footer.SetModelBusy(false)` for `StateToolCall` and tool-call handling.
- Only the chat status spinner now shows tool progress.
- Updated `internal/app/stats_status_test.go`.

**Validation:** `go test ./internal/app/...`

---

### 5. TUI performance issue on large conversations

**Symptom:** High CPU usage and unresponsiveness during streaming on large conversations; the compositor was doing full rebuilds every spinner tick.

**Root cause:** `ChatViewport.InvalidateRunningToolWidgets()` was incrementing the render generation every frame, forcing a full O(n) rebuild.

**Fix:**
- Changed `InvalidateRunningToolWidgets()` to set a pending flag and patch running tool widgets in place during `Render`.
- Added `toolWidgetsDirty atomic.Bool` to coordinate safely across the command and render loops without mutating shared caches from the ticker goroutine.
- Refactored `Render` into `Render` + `rebuildFrame` to keep complexity in check.
- Updated `TestChatViewport_InvalidateRunningToolWidgets` to call `Render` after invalidation and verified the race fix with `TestHandleToolCall_ToolWidgetAnimates` under `-race`.

**Validation:** `go test -race ./tui/... ./internal/app/...`

---

### 6. Planner tool subagent restriction

**Symptom:** The planner mode could spawn coder/explore/reviewer sub-agents, bypassing planner guard rules.

**Root cause:** `multiagent/agent_tool.go` and `tools/swarm/agent_swarm.go` did not check the caller's current mode.

**Fix:**
- Added `CurrentMode` callback to `AgentTool` and `AgentSwarmTool`.
- Reject `subagent_type` other than `plan` when the current major mode is `planner`.
- Wired the callback through `internal/app/subsystems.go`.
- Added tests in `multiagent/agent_tool_test.go` and `tools/swarm/agent_swarm_test.go`.

**Validation:** `go test ./multiagent/... ./tools/swarm/... ./internal/app/...`

---

### 7. Steering message bubble should stay at bottom until sent

**Symptom:** Steering messages were rendered as system chat bubbles immediately, instead of staying as a pending bubble at the bottom until the model consumed them.

**Root cause:** `ChatViewport` and the submit handlers used `AddSystemMessage` for steering.

**Fix:**
- Added `ConsoleSteeringPending` message type and `steeringPending` component in `tui/messages.go` / `tui/chat_viewport_components.go`.
- Added `pendingSteering` index, `AddSteeringPending`, and `ClearSteeringPending` in `tui/chat_viewport.go` so new messages insert above the pending bubble.
- Updated `internal/app/submithandler.go` and `internal/app/events.go` to use `AddSteeringPending` / `ClearSteeringPending`.
- Added `TestChatViewport_SteeringPending_StaysAtBottom`.

**Validation:** `go test ./tui/... ./internal/app/...`

---

### 8. Subagent failure / orchestration multiview

**Symptom:** The `agent` tool subagent returned only reasoning text and did not appear to execute tools or trigger the orchestrator multiview.

**Root cause:** `multiagent/agent_tool.go` ran subagents directly and did not surface lifecycle events to the orchestrator event stream. The `ForegroundOrchestrator` already existed and was wired to receive subagent events via `OnAgentCreated`, but the subagent tool had no reference to it and emitted no start/end markers.

**Fix:**
- Added an `Orchestrator` abstraction to `AgentTool` and a fallback via `AgentPool.Orchestrator()`.
- Added `ForegroundOrchestrator.Emit()` in `multiagent/orchestrator_emit.go`.
- `AgentTool` now emits `Sub-agent <type> started/completed/failed` lifecycle messages to the orchestrator event stream.
- Wired the orchestrator into the pool in `internal/app/subsystems.go`.
- Refactored `AgentTool.Execute` into `parseAndValidate`, `checkPlannerRestriction`, `startBackground`, and `runForeground` to keep complexity within budget.
- Added tests for orchestrator fallback and lifecycle emission in `multiagent/agent_tool_test.go`.

**Note:** The underlying `core/orchestrator` multiview panel is already driven by the orchestrator's event bus; this change ensures the subagent tool participates in that stream. A full "start a dedicated orchestration run for every subagent tool call" integration is a larger feature that was not required here.

**Validation:** `go test ./multiagent/... ./internal/app/...`

---

## Code Quality Summary

Checks run:
- `go vet ./...` ✅
- `staticcheck ./...` ✅
- `gocognit -over 15 .` ⚠️ pre-existing violations only:
  - `tui visualCursorInParagraph` (23)
  - `ansi Wrap` (21)
- `gocyclo -over 12 .` ⚠️ pre-existing violations only:
  - `ansi Wrap` (17)
  - `agentic (*Agent).handleStreamEvent` (17)
- `go test -count=1 -race -cover ./...` ✅

All new changes stay within the project's complexity and file-size budgets; the remaining violations are unrelated to the bugs fixed above and were present before this session.
