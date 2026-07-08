<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Archived Bugs — 2026-07-08

These bugs were reported from the diagnostic export
`/Users/muaddib/dev/goatest2/.goa/exports/goa-export-20260708-073144.zip`
and were closed during the 2026-07-08 session.

## Redraw artefacts / tearing

**Observation:** The UI was being redrawn while no real output changed,
producing tearing and vertical jumps. The input line and status line were
redrawn at higher positions, causing content to jump up and down.

**Root cause:** `tui/buildScene` stacks children from the top. When the chat
viewport or editor shrank, the components below them were pulled up the
screen. The compositor's `clearOnShrink` path then performed a full
clear/redraw, producing tearing and jumps.

**Fix:**
- `tui/chat_viewport.go` tracks `layoutStableHeight` and pads the rendered
  slice so the chat viewport never shrinks between terminal resizes.
- `tui/editor_render.go` tracks `stableMaxLines` and reserves the largest
  editor height seen since the last terminal resize, so the editor also never
  shrinks unless the terminal is resized.

**Tests:**
- `tui/chat_viewport_height_test.go`
- `tui/chat_viewport_position_stability_test.go`
- `tui/editor_height_stability_test.go`
- `tui/chat_viewport_scrollback_after_shrink_test.go`

## Processing stall

**Observation:** The LLM appeared to stop generating with no activity. The
report wondered whether goa was not consuming tokens and needed a timeout to
retry, or whether there was a starvation/lock.

**Root cause:** The "no activity" symptom in the export was the orchestrator
conversation stall: the reviewer asked for the `index.html` file and the
orchestrator did not continue the dialogue, so the run appeared to hang.

**Fix:**
- Updated `prompts/orchestrate/hub_orchestrator.md` to instruct the orchestrator
to list files (not dump contents) and to continue the conversation when a
specialist asks for clarification.
- Added capped per-role conversation history to `OrchestratorDelegateTool` so
follow-up delegate calls carry the previous task/response context.
- The provider layer already wraps streaming responses with an idle timeout
(`internal/agentic/provider/idle_timeout.go`, default 2 minutes) to detect
a true LLM connection stall.

**Tests:**
- `internal/app/orchestrator_delegate_conversation_test.go`
- `internal/agentic/testutil/real_llm_test.go`

## Orchestration tab order

**Observation:** Tab order should be:
1. Stats
2. Conversation
3. Orchestrator
4. <other>

The code originally created `[Conversation, Stats, <agents>]`.

**Fix:** Reordered the bookend tabs in `MultiAgentView.ensureBookendTabs` to
`[Stats, Conversation, ...]` and made `Stats` the default active tab.

**Tests:**
- `tui/orchestrator/view_tab_order_test.go`
- Updated existing tests in `tui/orchestrator/view_test.go`,
  `tui/orchestrator/view_agent_tab_test.go`, and
  `internal/app/orchestrator_adapter_integration_test.go`

## Orchestration tool calls stuck at bottom

**Observation:** Tool calls (`orchestrator delegate`, `coder write`) remained
stuck at the bottom of the conversation even after completion. The requested
behavior was a FIFO list of open items, ordered by occurrence.

**Fix:** Removed the two-zone active/inactive sort in `tui/chat_viewport.go`;
all entries now render in chronological FIFO order.

**Tests:**
- `internal/app/orchestrator_tool_active_zone_test.go`

## No tool calls visible in dedicated mode view

**Observation:** In a dedicated mode view (e.g. coder tab), only text was
visible; tool calls were not shown.

**Fix:** `AddAgentToolExecution` was using `LastWhere` which returned a copy,
so the `Meta["agent"]` stamp never persisted. Switched to `UpdateLast` which
modifies the entry in place.

**Tests:**
- `tui/chat_viewport_filter_test.go`
- `internal/app/orchestrator_per_agent_tab_test.go`

## Scroll does not work / creates artefacts

**Observation:** Conversation scroll did not work or created artefacts; it
did not fully redraw.

**Root cause:** The chat viewport shrank and the compositor emitted a full
screen/scrollback erase (`\x1b[2J` / `\x1b[3J`), corrupting the terminal
scrollback.

**Fix:** The scroll approach was unchanged (terminal native scrollback). The
artefacts were eliminated by enforcing a monotonically non-decreasing chat
viewport height, removing the spurious full clears.

**Tests:**
- `tui/chat_viewport_scrollback_after_shrink_test.go`
- `tui/streaming_scroll_test.go`

## Cursor shown on switch tab list

**Observation:** The hardware cursor was visible on the switch tab overlay
(`AgentTabPicker`). It should be hidden or shown after the list.

**Fix:** Added `OverlayCapturesInput` flag to `Scene`; when a capturing
overlay is open and has no cursor, the base editor cursor is hidden.

**Tests:**
- `internal/app/orchestrator_tab_picker_cursor_test.go`
- `tui/orchestrator/tabpicker_test.go`

## Incomplete global conversation (UI)

**Observation:** The global Conversation tab did not show all orchestrator
messages; the orchestrator's final summary was visible in the orchestrator
pane but not in the global conversation.

**Root cause:** The chat viewport was either suppressed or corrupted by
redraw artefacts. The export's `session.md` showed the full summary was
capable of being rendered.

**Fix:** Resolved by the tab-order and position-stability fixes. With the
default tab now `Stats` and the conversation tab visible on switch, the global
conversation renders completely.

**Tests:**
- Existing orchestrator integration tests

## Orchestration "conversation" not happening

**Observation:** The reviewer asked for the `index.html` file, but the
orchestrator did not pass it and just returned "ok".

**Fix:** Same as the processing-stall conversation fix. The orchestrator is
now explicitly instructed to be the communication layer and to continue the
dialogue until each specialist has what it needs. The harness keeps a capped
per-role conversation history.

**Tests:**
- `internal/app/orchestrator_delegate_conversation_test.go`

## Additional quality work

- Fixed cognitive/cyclomatic complexity violations in several touched test
  files.
- Removed dead code flagged by `staticcheck` (U1000/SA4006) in multiple
  packages.
- Fixed a deprecated-API bug in `internal/netutil/fetcher.go` by replacing
  `netErr.Temporary()` with `netErr.Timeout()`.
- Made the real-LLM integration test opt-in via `K8_LLM_URL` so it does not
  cause flaky failures in the default test suite.

