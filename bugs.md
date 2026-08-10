# Bug and feature Tracking

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

## Must fix

_(All must-fix items as of 2026-08-10 were closed and archived in
`docs/archive/bugs.2026-08-10.md`. This section is empty until new items are
added.)_

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

## Teams feature: PHASE 4 — goal binding (feature/team branch)

Phase 4 threads the `Team` field through goal create/queue/promote and applies a
team overlay for the duration of a team-bound goal (TEAMS.md §5.1–5.2).

### Implemented
- `core/goal/model.go` — `CreateGoalInput.Team`, `GoalSnapshot.Team`,
  `UpcomingGoal.Team`, `UpcomingGoalInput.Team`, `goalStage.team`,
  `GoalEventRecord.Team`.
- `core/goal/mode.go` — `CreateGoal` stages `input.Team`; `RestoreCreate` reads
  `record.Team`; `toSnapshot` carries `state.team`.
- `core/goal_queue.go` — queue insert/append/prepend carry `Team`.
- `tools/goal/goal.go` — `/goal` tool `team` arg + schema property; create +
  enqueue pass `Team` into `CreateGoalInput`/`UpcomingGoalInput`.
- `core/commands/goal.go` + `internal/app/events.go` — resume/promote pass the
  queued goal's `Team` into the promoted `CreateGoalInput`.
- `core/goal_driver.go` — `TeamOverlayManager` interface; `syncTeamOverlay`
  applies the bound team's overlay while a team-bound goal is active and removes
  it when the goal clears (mirrors FreshContext per-goal tracking). Wired to the
  TeamManager in `subsystems.go`.

### Remaining (follow-up, non-blocking)
- `/goal:new --team` CLI flag (binding is by name string + tool arg today; an
  interactive picker is a follow-up).
- Missing-team → paused: today an undefined team name is a logged no-op (the goal
  runs session-default); a hard pause-on-undefined-team can be added if desired.
- Tests for the missing-team path once that contract is finalized.

### Phases already committed
- f963672 — phase 3: /team command (model-like), footer badge, /config CRUD
- 6681e1f — phase 2: TeamManager snapshot/restore + adapters
- c8661f0 — phase 1: teams config schema + validation
- f69048e / 78cc5af — docs: TEAMS.md spec + TEAMS-PLAN.md microsteps
