<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<!-- Copyright (C) 2026 Pierre Poissinger -->

# Archived: runaway-loop guardrail latches on empty turn endings (repeat of 2026-08-24)

Closed 2026-10-15. Evidence: `.goa/exports/goa-export-20261015-170840.zip`
(issue.md: goal auto-paused with `runaway loop detected: the assistant repeated
the same response 3 consecutive times without progress (repeated: "(empty
response)")`).

## Observed

Goal-driven session, 7 continuation turns / 1h51m, provider `openai-codex`
(`gpt-6-luna`), context 46%. The guardrail stopped the session at
`agent.log: 16:08:01.529 [WARN] Loop guardrail: assistant message repeated
2 time(s) (limit 2)` and the goal paused with
`Paused after detecting a runaway response loop`.

Evidence chain from the bundle:

- `session/events.jsonl` — two visible warnings, both
  `Runaway-loop warning: … (repeated: "(empty response)")`: line 6678 (end of
  turn 3) and line 21046 (end of the turn starting at line 20820).
- Turn boundaries: user prompts at lines 15880 (turn 5), 20820 (turn 6),
  21049 (turn 7). Each of turns 5/6/7 ended with a round that emitted NO
  content, NO thinking and NO tool calls — only `token_stats`,
  `context_stats`, `end` (lines 20812-20817, 21041-21045, 25076-25080) —
  followed by an empty assistant message (`progressLoopSample` →
  `(empty response)`).
- Each of those turns executed dozens of successful tool calls (read / search /
  bash / edit) before that final empty response. The earlier same-shaped pause
  (recovered via `/goal:resume`, line 15875) shows the failure is periodic, not
  one-off.
- `agent.log` — `premature stop detected (empty stop right after tool results);
  auto-continuing turn (attempt 1/3 … 3/3)` (15:56:19, 16:01:27, 16:02:00) shows
  goa recovering from these empty stops until its per-turn budget is spent; the
  turn then finalizes with the empty assistant message.
- The empty reply was finalized with no log line and no notice at all (the
  silent-stop notice only fired for `thinking != ""`), and the empty round
  re-emitted the previous round's usage (`in=1375 out=183` twice), so the
  bundle's `[stats] turn 46` line is stale.

## Root cause

The 2026-08-24 fix (see
`fix-runaway-loop-tool-progress-false-positive.2026-08-24.md`) added the
progress gate `turnHadSuccessfulToolResult(idx, msg)` — but it inspects the
tool calls of the LAST message only. A turn that ends with an empty provider
reply (the empty-stop quirk after tool results) has no tool calls on that
message, so the gate answers "no progress" and the empty message becomes the
turn's fingerprint. Three such turn endings in a row — spread over three goal
continuation turns, whose guardrail state is deliberately carried across
(`core/agentmanager_turns.go`) — latch the session stop. The model repeated
nothing: an empty response has no content to repeat.

## Fix

1. `internal/agentic/agent_loop_progress.go` — an empty last message is judged
   against the TURN: `turnWindowHadSuccessfulToolResultLocked` scans the turn
   window (`turnStartHistoryLen..idx`) for any tool call whose result landed
   without an execution error. Progress ⇒ no strike. Turns that produced
   nothing at all (no tool work) still strike and latch (plan rule 3).
   `isEmptyAssistantMessage` centralizes the "no content, no thinking, no
   tool calls" test.
2. `internal/agentic/agent_turn_lifecycle.go` — a reply empty in every
   dimension now reports a silent stop (`markSilentStop`, shared with the
   reasoning-only branch) and surfaces a non-transient system notice, so an
   empty provider reply is never finalized silently again. The goal driver
   pauses the goal with the matching reason instead of blaming a runaway loop.
3. `core/goal_driver.go` — `PauseSilentStop` wording generalized to
   `Paused after model stopped without producing a reply (send continue to
   resume)`; it no longer claims "mid-reasoning" for a reply that contains no
   reasoning at all. The precise distinction lives in the per-case notice.

## Recreation + tests

`internal/agentic/agent_runaway_empty_after_tools_test.go`:

- `TestCheckProgressLoop_EmptyEndingAfterToolWorkDoesNotLatch` — three turns of
  [successful tool call + empty assistant message]; pre-fix it latches at turn 3
  with the export's exact error text, post-fix no strike.
- `TestRun_GoalTurnsEndingEmptyAfterToolWorkDoNotStopSession` — end-to-end with
  `toolThenEmptyProvider` (tool round, then empty rounds): three goal
  continuation turns must complete and report `LastTurnSilentStop()`.
- `TestRun_EmptyReplyAfterToolWorkSurfacesNotice` — the notice must be a
  non-transient `system-notification`.
- `TestCheckProgressLoop_EmptyNoToolTurnsLatchUnchanged` /
  `TestCheckProgressLoop_EmptyEndingAfterFailedToolsStillLatches` — the designed
  latch for turns with no (or only failing) tool work is unchanged.
