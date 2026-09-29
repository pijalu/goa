<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Bug archive — 2026-09-29

## goals.verify_timeout not settable at runtime — verify-gate timeouts unfixable mid-session (goa-export-20260929-123516)

**Observed.** Goal `fair.jay` (creaves.project) failed the done-gate 3× with
`[verify command timed out]`: the recorded verify command needs ~295s (full race
suite) but the gate enforced the 2m default. Attempted remediations both failed:

1. `/config:set goals.verify_timeout 10m` → `Invalid value for
   goals.verify_timeout: unknown config key` — the key is absent from
   `configSetters` (core/commands/config_cli_setters.go), which only lists
   `goals.default_turn_budget` and `goals.stall_turns`.
2. Direct edit of `.goa/config.yaml` (adding `goals: verify_timeout: 10m`) did
   not take effect in the running session: `configureGoalMode`
   (internal/app/subsystems_goal.go:37) resolves
   `cfg.Goals.VerifyTimeoutOr(defaultGoalVerifyTimeout)` to a concrete
   `time.Duration` once at startup and `execCommandVerifier` holds it in an
   immutable field. No live-sync path exists (contrast:
   `syncGoalLimits` pushes turn budget/stall turns live). The 3rd verify
   failure auto-blocked the goal, requiring user intervention for a
   config-only problem.

Also: the goal tool's completion-progress display already reads the timeout
live (`tools/goal` `VerifyTimeout func() time.Duration`), so UI and gate could
disagree after a live change — the gate must follow the same live-read pattern.

**Expected.** `goals.verify_timeout` settable via `/config:set`, persisted, and
applied to the running session's verify gate immediately. Startup behavior
unchanged.

**Requirement added during the fix:** the value is tweakable upward for long
test suites, capped at a max of **1h** (`config.MaxGoalVerifyTimeout`).

## Fix plan (executed)

1. `config`: export `DefaultGoalVerifyTimeout = 2 * time.Minute` and
   `MaxGoalVerifyTimeout = time.Hour` next to `GoalsConfig`;
   `VerifyTimeoutOr` clamps > 1h values to the max so a hand-edited config
   cannot pin the gate above the ceiling; shared `ParseGoalVerifyTimeout`
   helper. `internal/app.defaultGoalVerifyTimeout` is now an alias of the
   config constant.
2. `core/goal`: `GoalMode.SetVerifyTimeout(d)` pushes the bound into the wired
   verifier via an optional interface (`SetVerifyTimeout(time.Duration)`);
   no-op when the verifier does not support it or none is wired.
3. `internal/app`: `execCommandVerifier.SetVerifyTimeout` — ≤ 0 selects the
   default, > 1h clamps to the max; `Verify` reads `v.timeout` per call.
4. `core/commands`: `configSetters["goals.verify_timeout"]` (parse duration,
   require > 0 and ≤ 1h, store canonical `d.String()`); `syncRuntimeConfig`
   routes the key through `syncGoalLimits`, which now also pushes
   `Mode.SetVerifyTimeout(cfg.Goals.VerifyTimeoutOr(config.DefaultGoalVerifyTimeout))`;
   completion entry added (config_completion.go).
5. Docs: GOALS.md (verify section + config reference) and LIMITS.md updated —
   default 2m, max 1h, runtime-settable with live effect.

## Validation evidence

- `go build ./...` → BUILD_OK.
- Targeted: `go test -count=1 -race` for config / core/goal / core/commands /
  internal/app — all ok, including:
  - `TestGoalsVerifyTimeoutOr` (extended: 24h clamps to 1h, negative falls
    back), `TestSetVerifyTimeout_ForwardsToVerifier`,
    `TestSetVerifyTimeout_ThenVerifyUsesVerifier` (core/goal),
    `TestExecCommandVerifier_SetVerifyTimeout` (raise/clamp/default-reset,
    bound observed in `VerifyOutcome.TimeoutMs`),
    `TestSyncRuntimeConfig_GoalVerifyTimeoutCLI` (end-to-end: apply → live
    verifier bound 10m → rejected `0`/`-5s`/`abc`/`2h` leave state untouched →
    `1h` boundary accepted; handler echo
    `Set goals.verify_timeout = 10m` captured via `OutputBuffer`).
- Gates (run separately): `go vet ./...` OK; `staticcheck ./...` OK;
  `gocognit -over 15 .` OK; `gocyclo -over 12 .` reports two pre-existing,
  unrelated test functions (skills-default integration tests, untouched by
  this change — noted per guideline); `go test -count=1 -race -cover ./...`
  RC=0, 87 packages ok.

## Residual note

The 1h ceiling is deliberate: an unbounded verify run would stall goal
completion forever on a command that never exits. The live value is also what
the completion-evidence display shows (`tools/goal` already reads it live), so
UI and gate stay in sync.

User-facing remediation for the original incident, with this fix shipped:
`/config:set goals.verify_timeout 10m` then `/goal:resume` — no restart needed.
