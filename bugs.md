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

_All bugs opened as of 2026-07-07 have been closed and archived in
`docs/archive/bugs.2026-07-07.md` (root-cause verification + regression tests
for each). Add new bugs below following the workflow above._

<!-- New bugs should be added above following the workflow above. -->

## TUI position stability regression
The chat viewport's stable-height padding causes the conversation to slowly move down at each refresh instead of staying anchored. The footer (input + status) is pushed down continuously. The expected behavior is that the viewport height is stable when the visible content is not growing; it should only grow when new content is added.

**Research / root cause:** `tui/chat_viewport.go` `padToStableHeight` tracks `layoutStableHeight` and pads the rendered slice to that height. The intention was to prevent the viewport from shrinking and pulling the input/status lines up. In practice the stable height is updated whenever the raw rendered lines exceed the previous value, and the raw lines are rebuilt every time the status spinner invalidates running tool widgets (via `InvalidateRunningToolWidgets`). This means any jitter in a tool widget's rendered height or any repeated invalidation causes the stable height to grow monotonically, pushing the footer down. The top-down component stacking in `tui/tui.go` `buildScene` then propagates that downward movement to every component below the chat viewport. The "fix" is therefore a band-aid that trades upward jumps for continuous downward drift.

**Reproducer:** Not yet automated. Live run with a multi-agent orchestration shows the chat viewport slowly moving down at each status-spinner tick while the orchestrator is answering. The existing unit tests (`TestChatViewport_PositionStability_InputLineDoesNotJumpUp`) only assert "no upward movement", not "no downward drift".

**Critical implication:** The TUI layout model is fundamentally wrong for a chat interface. Anchoring should be at the bottom (input/status stay at the bottom, content scrolls above it), not at the top with stable-height padding. Any solution that grows monotonically will eventually consume the whole screen and push the footer off-screen.

## Orchestrator spinner stuck / history not refreshing
The orchestrator spinner is stuck and the conversation history does not refresh. Running tool widgets / streaming content do not update the display correctly. This may be the same root cause as the position-stability regression or a separate invalidation issue.

**Research / root cause:** `tui.StatusMsg` animates via a goroutine that calls `TUI.Apply` on every frame. `StatusMsg.SetOnFrameChange` is wired to `ChatViewport.InvalidateRunningToolWidgets`, which marks every `ToolExecutionComponent` with status `ToolRunning` as dirty and increments `ChatViewport.generation`. This triggers a full `ChatViewport` rebuild on every spinner frame. Two problems follow:
1. The rebuild itself may be the source of the "moving down" symptom if the rebuilt widget has a slightly different height or if the stable-height padding grows.
2. The status text (e.g. "orchestrator answering...") is not part of the chat viewport; it lives in `StatusMsg`. If the chat is suppressed (Stats tab) or the per-agent filter hides the agent blocks, the conversation appears frozen even though the status bar is updating.

**Reproducer:** Live run with hub topology shows "◠ orchestrator answering..." stuck while the orchestrator is actually generating events. The events are fed to the chat viewport but the visible pane may not refresh because the Stats tab is active and the Conversation tab is suppressed, or because the per-agent filter is hiding the blocks.

## Widget ordering rules are not respected
The current layout does not implement the documented widget-ordering rules:
- A widget should only remain at the bottom of the conversation while it is actively receiving streamed content.
- Completed/open tool calls should keep their creation order and move down only when new content pushes them up (FIFO).
- A widget should never move below another widget that is currently streaming.

**Research / root cause:** `tui/chat_viewport.go` was changed to render all entries in chronological order and drop the active/inactive two-zone sort. This is the simpler alternative the user accepted ("widgets are not changing order"). However, the requirement is ambiguously satisfied: a completed tool widget does stay in creation order, but it also stays at the bottom if it was the last created entry. New content appended after it is below it, so the "widget never moves below a currently streaming widget" rule is violated because the FIFO order does not distinguish active from completed streams. The previous two-zone sort was explicitly removed because it pinned open tool calls to the bottom, which was also wrong. The correct behavior is either:
1. Two zones: completed widgets in chronological order, then active streaming widgets at the bottom; or
2. Pure chronological order with a visual indicator for "active" but no reordering.

The user is open to option 2. The current code implements option 2 but does not add a clear active indicator and may still interact badly with the stable-height padding.

## Footer stats visibility
If the multi-pane conversation layout cannot be made to scroll and position correctly, the fallback is: keep only the global conversation (all agents) and add a clear, compact stats widget to the footer, above the input line, showing the agent pool snapshot.

**Research / root cause:** The current design tries to reuse the same `ChatViewport` for three different views (Conversation, per-agent TabAgent, and suppressed Stats). The viewport is suppressed/unsuppressed and filtered on tab switches. This adds complexity to the position-stability and scrollback logic because the same rendered height must satisfy different content sets. The per-agent tabs also duplicate the conversation concept without providing a clear benefit over the global conversation. A simpler design would be:
- One global conversation pane (always visible during a run).
- A compact stats strip rendered above the input line (like a status bar extension) showing agent roles, models, and token counts.
- No separate stats tab, no per-agent conversation tabs. The user uses the terminal's scrollback to read history and a key to scroll the in-app conversation if needed.

## TUI architecture review required
The TUI layout, scroll, and conversation-pane approach needs a complete critical review to ensure it is sound and does not introduce non-sensical UI behavior (jumping widgets, stuck spinners, unbounded viewport growth, etc.).

**Research / critical review:**
1. **Top-down stacking is wrong for a chat UI.** `tui/tui.go` `buildScene` stacks components from top to bottom: header, chat viewport/agent content, status bar, tab bar, editor, footer. Any change in the chat/agent-content height shifts the input/status lines up or down. The chat interface should be anchored at the bottom: input and status are fixed at the bottom, and the conversation area is above them, limited to available height. When the conversation grows beyond its allocated height, the oldest lines scroll into the terminal scrollback (or an in-app scrollback), not the entire layout.
2. **Stable-height padding is a downward-drift hack.** The `padToStableHeight` approach prevents the chat viewport from shrinking, which prevents upward jumps, but it makes the viewport height monotonically non-decreasing. Any jitter or repeated invalidation increases the height and pushes the footer down. This is not acceptable for a long-running UI.
3. **Terminal scrollback is fragile.** The compositor relies on emitting newlines to populate the terminal scrollback. This is coupled with the differential renderer and the top-down layout. If any component changes height, the differential scroll math can leave blank lines or corrupt the scrollback. Manual scroll-up within the app is not implemented, so the only way to read history is the terminal's scrollbar, which is not reliable across terminals and not testable.
4. **Status spinner invalidates the whole chat viewport on every frame.** `StatusMsg` ticks at ~10Hz and calls `ChatViewport.InvalidateRunningToolWidgets` via `SetOnFrameChange`. This forces a full `ChatViewport` rebuild on every frame. Even if the rebuild is correct, it is expensive and can interact with the stable-height padding. The spinner should only invalidate the tool widgets that need to be redrawn, not the whole conversation.
5. **Multi-pane conversation is not worth the complexity.** The global conversation + per-agent tabs + stats panel share a single `ChatViewport` with suppression and filtering. This makes position stability and scroll correctness harder because the same viewport must maintain stable height across different content sets. The simpler fallback (global conversation + footer stats) is preferable.

**Recommended direction:**
- Abandon the top-down, stable-height-padding model.
- Redesign the layout as bottom-anchored: input line is at the bottom, status/stats are just above it, and the conversation area fills the remaining space above, clamped to available height.
- When the conversation grows beyond its allocated height, the oldest lines are pushed into the terminal scrollback (current behavior) but the visible region stays at the same Y, and the footer/input do not move.
- Implement a manual scroll-up key for the chat viewport so the user can scroll without depending on terminal scrollback.
- If the above is too large, implement the fallback: global conversation + compact footer stats strip above the input line, remove per-agent tabs and the stats panel.

