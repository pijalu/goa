# Bug Fix Plan — Team delete leaves dangling `teams.active`

## Bug

When an active team is deleted, goa fails to start:

```
Config error: validation errors (1):
teams.active: team "Local" not defined in teams.definitions
```

Expected: the config is updated to remove the deleted team from `teams.active`.

## Root Cause Analysis

`teams.active` is a **project-local** setting (`.goa/config.local.yaml`), while
`teams.definitions` lives in the **home** layer (`~/.goa/config.yaml`). There
are three paths by which a team definition can disappear while `teams.active`
still points at it:

1. **`/team` picker delete** (`teamRemove` in `core/commands/team.go`): when the
   deleted team is the active team, the code clears `cfg.Teams.Active` in
   memory and deactivates the manager, but only persists `teams.definitions`
   via `persistTeamsDefinitions` (home layer). The cleared `teams.active` is
   **never persisted** to the local layer, so on the next start the stale local
   value resurfaces and validation fails. **This is the primary bug.**

2. **`/config → Teams → remove`** (`confirmRemoveTeam` in
   `core/commands/config_teams.go`): already clears and persists
   `teams.active` via `saveTeamsActive()`. Correct — needs a regression test.

3. **Manual config edits** (user deletes a team from `~/.goa/config.yaml` by
   hand while `.goa/config.local.yaml` still selects it): no code path fixes
   this. A startup hard failure for a self-healable condition is poor UX — the
   loader should clear the dangling reference with a warning instead of
   exiting.

## Fix Plan

### F1 — `teamRemove` persists the cleared active selection

In `core/commands/team.go`, after clearing `cfg.Teams.Active` for the deleted
active team, call `persistActiveTeam(ctx, "")` so the local layer is updated.
`persistActiveTeam` both sets `cfg.Teams.Active` and writes via
`SaveLocalFieldValue`, so the in-memory assignment it replaces can be dropped.

### F2 — Load-time auto-heal for dangling `teams.active`

In `config/loader.go` `Load()`, immediately before `cfg.Validate()`: if
`cfg.Teams.Active != ""` and the team is not in `cfg.Teams.Definitions`, clear
it and emit a warning to stderr (matching the existing `sanitizeSelectorSentinels`
cleanup pattern — stale persisted state is dropped at load, not fatal).
Validation then never sees the dangling reference, and goa starts cleanly.

Rationale for healing at load rather than only at deletion:
- Covers manual edits and cross-layer desync (home/local files edited by
  different runs or tools).
- A missing team selection is safe to drop: the session simply starts with no
  active team, identical to `/team:off`.
- Hard-failing the whole application for a recoverable state violates the
  principle of graceful degradation.

### Tests

| # | Test | File | Covers |
|---|------|------|--------|
| T1 | `TestTeamCommand_PickerDeleteActiveClearsPersistedActive` | `core/commands/team_test.go` | F1: picker-delete the active team → local layer `teams.active` cleared, reload through the cascade loads with no active team and no validation error |
| T2 | `TestConfigMenu_RemoveActiveTeamClearsPersistedActive` (or extend existing) | `core/commands/config_teams_test.go` | F-path 2: `/config` menu removal of the active team persists the clear (regression) |
| T3 | `TestLoadClearsDanglingTeamsActive` | `config/loader_teams_heal_test.go` (or `teams_test.go`) | F2: home config defines no teams, local layer sets `teams.active: ghost` → `Load()` succeeds with `Teams.Active == ""` |

### Validation steps

1. Unit tests T1–T3 pass.
2. Manual/interactive validation: create team, activate it, delete it via the
   picker, restart goa — must start cleanly with no active team.
3. Quality gates (each run separately):
   - `go vet ./...`
   - `staticcheck ./...`
   - `gocognit -over 15 .`
   - `gocyclo -over 12 .`
   - `go test -count=1 -race -cover ./...`

## Execution log

Status: **verified closed**.

### Changes

1. `core/commands/team.go` — `teamRemove`: when the deleted team is the
   active team, persist the cleared selection via `persistActiveTeam(ctx, "")`
   (writes `teams.active: ""` to `.goa/config.local.yaml`). Previously the
   clear was in-memory only and the stale local value hard-failed the next
   start.
2. `config/loader.go` — new `sanitizeDanglingActiveTeam()` invoked in `Load()`
   right before `Validate()`: clears a `teams.active` that names no defined
   team and warns on stderr. Heals desync from manual edits or older builds
   instead of refusing to start (equivalent to `/team:off`).

### Tests

- `TestTeamCommand_PickerDeleteActiveClearsPersistedActive`
  (`core/commands/team_test.go`) — picker-deletes the active team; asserts the
  local layer carries `active: ""` and a cascade reload starts with no active
  team. Verified to FAIL without fix 1 (local file kept `active: alpha`).
- `TestConfigMenu_RemoveActiveTeamClearsPersistedActive`
  (`core/commands/config_teams_test.go`) — regression coverage for the
  `/config → Teams → remove` path (already persisted correctly).
- `TestLoadClearsDanglingTeamsActive`, `TestLoadKeepsDefinedTeamsActive`,
  `TestSanitizeDanglingActiveTeamWarns` (`config/loader_teams_heal_test.go`) —
  heal behavior, no-overreach guard, stderr warning.

### Terminal validation (guideline #5)

Built the real binary against a fixture reproducing the bug (home config with
`teams.definitions: {}`, local layer with `teams.active: Local`):

- **Before fix** (`config/loader.go` stashed): `Config error: validation
  errors (1): teams.active: team "Local" not defined in teams.definitions` —
  exact reproduction of the reported failure, process exits.
- **After fix**: stderr warning `teams.active "Local" is not defined in
  teams.definitions — clearing (team deleted?)`, then goa starts normally
  (TUI splash rendered; headless `--prompt` path also works).

### Quality gates (each run separately)

1. `go vet ./...` — clean
2. `staticcheck ./...` — clean
3. `gocognit -over 15 .` — 3 pre-existing warnings in unrelated test files
   (`agentmanager_compression_test.go`, `goal_command_manage_reorder_test.go`,
   `config_test.go`)
4. `gocyclo -over 12 .` — pre-existing warnings in unrelated test files only
5. `go test -count=1 -race -cover ./...` — all packages pass (config 80.4%,
   core/commands 61.6%)
