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

## Teams feature: PHASE 4 status + remaining work (feature/team branch)

> **Parallel-work note:** phases 1–3 are committed. The Phase 4 edits below are
> UNCOMMITTED working-tree changes. A parallel agent may be advancing this; do
> NOT rebase/commit/clobber. If continuing here, build on the current tree.

### Current uncommitted state (Phase 4 — goal binding, `Team` field threading)

Files modified (all `+Team` field / mirror of `FreshContext`), NOT yet built/tested:
- `core/goal/model.go` — `GoalSnapshot.Team`, `UpcomingGoal.Team`, `UpcomingGoalInput.Team`, `goalStage.team`, `GoalEventRecord.Team` + doc comments
- `core/goal/mode.go` — `RestoreCreate` reads `record.Team`; `CreateGoal` stages `input.Team` + emits `GoalEventRecord{Team}`; `toSnapshot` carries `state.team`
- `core/goal_queue.go` — `insertGoal` sets `Team: strings.TrimSpace(input.Team)`
- `core/commands/goal.go` — `resumeFirstQueued` passes `Team: removed.Team` into resume CreateGoal
- `internal/app/events.go` — `promoteQueuedGoal` passes `Team: removed.Team`
- `bugs.md` — this entry (3 added lines of prior unrelated bugs)

### NOT yet done — remaining Phase 4 steps (per TEAMS-PLAN.md §Phase4, steps 20–24)

1. **Compile + test the current diff** — `go build ./...` then `go test -count=1 -race ./core/...`; fix any fallout (Team doesn't break snapshots — verify a golden snapshot round-trip test exists/still passes).
2. **Pass `Team` through `/goal` tool schema** (tools/goal/goal.go) — add `team` arg to `goalArgs`, schema property, and `CreateGoalInput{Team: …}` on create. (Current diff has only the command goal.go side, NOT the tool.)
3. **Queue append path** — ensure `AddUpcomingGoal`/queue-append (not just `insertGoal`) carries Team; check `goal_queue.go` add path uses `insertGoal` (file confirms it does — verify).
4. **Goal promotion contract** — when a queued goal with a `Team` promotes to active, apply the team overlay via TeamManager (mirror how FreshContext drives clean-context on promote). The promotion code (mode.go promote / events.go drive loop) must call `teamManager.Activate` for the bound team.
5. **Missing-team → paused** — on promote/via goals.go, resolve the bound team name; missing → goal pauses (blocked/requesting) like the `/team` missing-team contract.
6. **Overlay lifecycle on active-bound goal** — apply team overlay at `GoalActive` entry, restore on completion/pause/cancel (`teamManager.Deactivate`), mirroring drive-loop entry/exit.
7. **regression:** `/goal:new --team` flag or session-bound team — decide whether goal binding is per-team-name string (done) vs picker; add `--team` parse in `parseOptionalGoalArgs`/`splitGoalNextArgs` if opting for flag.
8. **Tests** — goal create with Team round-trips snapshot/restore; promotion applies overlay; missing team pauses; goal `/team` remember on queue.
9. **Quality gates** (run SEPARATELY, bugs.md guideline #6): `go vet ./...`, `staticcheck ./...`, `gocognit -over 15 .`, `gocyclo -over 12 .`, `go test -count=1 -race -cover ./...`.
10. **Commit** — `feat(teams): phase 4 — goal binding with team overlay` on `feature/team`.

### Phases already committed (don't redo)
- f963672 — phase 3: /team command (model-like), footer badge, /config CRUD
- 6681e1f — phase 2: TeamManager snapshot/restore + adapters
- c8661f0 — phase 1: teams config schema + validation
- f69048e / 78cc5af — docs: TEAMS.md spec + TEAMS-PLAN.md microsteps

### Open design decisions for the plan
- Whether goal-team binding is by name string only (current) or adds a `/goal:new:<team>` token/option. Recommend: name string + optional `--team` for now; interactive picker = follow-up.
- Overlay drifts the footer `~` badge like `/team` manual switch — confirm desired (likely yes: keep drift semantics uniform).
