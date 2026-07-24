<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Bug/Feature Tracking

## Guideline

1. Create a detailed fix plan for each bug/new feature - the plan must contain test approach and validation steps - execute the plan and validate the fix when all elements are in place.
2. Any issues found, even if not related to the bug/feature, must be fixed and the fix plan must be updated accordingly. You can add new items to the bug list as you find them.
3. Each item should be moved to archive when tested and closed as the associated plan.
5. Use filmstrip approach to validate the output of the tool - you must verify the actual terminal output.
6. Check code quality with each tool run separately (do not chain them with `;` or `&&`):
   - `go vet ./...`
   - `staticcheck ./...`
   - `gocognit -over 15 .`
   - `gocyclo -over 12 .`
   - `go test -count=1 -race -cover ./...`
   Fix any new issues introduced by the change. Pre-existing warnings are acceptable only if they are unrelated to the change and explicitly noted.
9. **Cache-hit-first design (CRITICAL for local models).** A cached prefix
   costs ~0; a full re-parse costs 40-100x more (measured 2026-07-21 on
   qwythos-9b-v2: 23.6 tok/s generation — a 20K-token re-parse is a 45-90s
   stall). Therefore every provider request must be **strictly append-only**:
   never move, rewrite, or re-project content mid-history; volatile
   per-request text may only ever be appended at the tail. The system prompt
   (byte 0) must stay byte-identical for the whole session. Anything that
   "decorates" messages per request (cache_control breakpoints, markers,
   wrappers) must be pinned to a fixed position — a marker that moves to the
   newest message each round rewrites history bytes and kills llama.cpp's
   longest-prefix cache match exactly where it lands. Validate any change to
   prompt/message construction with a proxy capture proving request N is a
   byte-prefix of request N+1, and by watching CH% climb in real sessions.

 *At the end of the session*: the list should be empty and this file should only contain the guidelines for bug reporting.
If new items are added, restart the process.

## STOP CONDITION (binding — an agent working this file must not stop early)
An agent working this file may ONLY stop when ALL of the following hold:
1. This file contains NO open items (every item is ✅/closed or moved to the archive).
2. Every item is tested and working (regression test green; filmstrip-validated where it is a UI behavior per guideline #5).
3. Any issue/problem discovered during the work has been ADDED to this file AND solved — nothing is deferred out-of-band.
A turn that ends with open items, an untested fix, or an unrecorded newly-found issue is a FAILED turn: continue working; do not summarize-and-stop.

## Workflow for bugs
1. Reproduce the failure before editing — ideally a command or script that triggers it on demand.
2. State the observed failure exactly (command + output).
3. Localize to the smallest region — ideally the specific lines — before editing. Precise localization is the strongest predictor of a correct fix.
4. Change one hypothesis at a time.
5. Prefer the minimal fix over a broad refactor.
6. Verify against the original failing command before declaring done.
7. Run the code-quality checks from guideline #6 separately and confirm the fix does not introduce new violations.
8. Move the bug list to `docs/archive/bugs.<fixdate>.md` when all items are closed.

---

## Open Items

### BUG: `/goal:resume` does not restart a paused goal — user must send a message

**Observed:** Running `/goal:resume` on a paused goal does not resume execution.
The goal stays paused until the user manually sends another message; only then
does the goal continue. `/goal:resume` should itself re-activate the goal and
kick off the next turn without requiring a manual nudge.

**Expected:** `/goal:resume` transitions the goal from paused to active and
immediately schedules continuation (the GoalDriver continuation turn), with no
user message required.

**Investigate:** the `/goal:resume` command handler (core/commands, goal
command) — whether it only flips goal state to active/resumed but fails to
trigger the GoalDriver / agent continuation that a normal user message would.
Confirm the resume path calls the same "schedule continuation" entry point used
when a user message arrives while a goal is active.

**Test approach:** unit/integration test that pauses a goal, invokes
`/goal:resume`, and asserts the goal becomes active AND a continuation turn is
scheduled without any user message. Validate the resume path emits the same
agentic events as a user-driven continuation.

---

### BUG: `/quota` request during streaming corrupts the TUI (duplicated/garbled frames)

**Observed:** Issuing `/quota` while a streaming block (e.g. "Thinking…") is
being written corrupts the display: the input box and trailing border lines
(`└───…───┘`) are repainted many times over, interleaved with the streaming
output, leaving a long run of duplicated box fragments and broken layout. See
the captured frame in the report: dozens of repeated
`└────…────┘` separators and a duplicated `(alt+e to edit)` input box.

**Expected:** `/quota` output renders cleanly even while a stream is in
flight — the streaming viewport, the quota modal/section, the footer, and the
input box must each repaint exactly once, in the correct z-order, with no
duplicated or orphaned border fragments.

**Investigate:** how `/quota` renders its table relative to the live stream —
whether it writes directly to the screen outside the TUI engine's differential
renderer / CSI-2026 synced-output region, bypassing the frame compositor and
racing the in-progress stream repaint. Check the TUI render path (tui/) for
the quota renderer and the streaming/stream-state wiring: a non-engine write
during a stream would produce exactly these duplicated frames.

**Test approach:** filmstrip validation (guideline #5) driving a `/quota`
request while a stream is active; assert the final composed frame contains
exactly one quota table, one input box, and no repeated border runs.
