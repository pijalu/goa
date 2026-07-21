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

*At the end of the session*: the list should be empty and this file should only contain the guidelines for bug reporting.
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

# Open TODO
## CRITICAL: /goal destroy leaves stale goal content in the cached prompt

**Reported 2026-07-21.** Destroying a goal (`/goal cancel`, queue `delete`,
`/goal replace`) does not fully clear goal content from what the model sees:
goal text is cached into the prompt in two places, and at least one of them
appears to survive the destroy.

### Investigation so far (localized)
- Static reminder: `Agent.buildSystemPrompt`
  (internal/agentic/agent_streaming.go:1264-1271) prepends
  `GoalInjector.ActiveGoalReminder()` to the system prompt as the *cacheable
  prefix*. The injector reads live state (`GoalMode.GetGoal` → fresh snapshot
  each call, core/goal/mode.go:384-390) — so the static prefix itself
  correctly disappears/changes on destroy (busting the provider prompt cache
  from byte 0 — expensive but correct).
- Queue store: `GoalQueueStore` (core/goal_queue.go) re-reads
  `upcoming-goals.json` on every op — no in-memory cache; a queue `delete`
  persists immediately.
- **Prime suspect (to verify):** `mergeGoalProgress`
  (agent_streaming.go:1285+) injects the *dynamic* per-turn goal progress
  ("Status: active, Progress: N turns…") as a dedicated system message before
  the last user message. If those progress messages persist into `a.history`
  (not just the outgoing provider slice), then after a destroy the
  conversation permanently carries "active goal, N turns" steering text for a
  goal that no longer exists — the model keeps working a dead goal.

### Fix plan
1. Confirm whether `mergeGoalProgress` mutates `a.history` or only the
   per-request slice.
2. Repro: `/goal create` → one turn → `/goal cancel` → inspect next request
   → assert no goal reminder/progress text present.
3. Fix per finding: strip goal-progress slot messages from history on goal
   destroy, or make the slot strictly per-request.
4. Regression test: destroy → next `buildProviderHistory` contains zero
   goal-progress messages.
5. Validate: guideline #6 gates separately.
