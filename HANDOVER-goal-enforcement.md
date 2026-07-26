<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Handover — Goal Enforcement & Machine-Checked Done-Gate

**Branch:** `feature/goal-enforcement` (merged with main @ `60ff0d0`, all tests green)
**Spec:** `docs/GOALS.md` is the authoritative subsystem spec — keep it in sync with behavior.
**Rule:** Do NOT commit `bugs.md` on this branch (bug-tracker workflow lives on main).

## What's done (3 commits + merge)

| Commit | Content |
|--------|---------|
| `7f401c0` | Done-gate (`goals.done_gate`: verify/evidence/off, default verify); terminal-answer contract (model `paused` requires `reason`; `blocked` requires `reason`+`expectation`, persisted in event log + TUI marker + reminders); proactive-resume notes; queue preserves criterion + alias; dead `ReminderFn` path removed; `docs/GOALS.md` rewritten as spec. |
| `e51859e` | **Phase 1.** `verifyCommand` machine-checked done-condition (plumbed: tool arg → `CreateGoalInput` → `goalStage` → event log → snapshot → `UpcomingGoal` → promotion); todo-consistency check (gated completion rejected with open todos); escalation bound → auto-block at `goals.max_verify_failures` (default 3) with user-review expectation; `GoalJudge` interface (fail-open on error); `RequestComplete` redesigned (`CompleteOutcome`: Closed/Challenged/VerifyFailed/NoGoal — slow verification runs **unlocked** with a state-change guard); `mergeGoals` added to `DeepMerge` (**fixes latent bug: user-set `goals.*` keys were silently dropped**); config keys `verify_commands`/`max_verify_failures`/`stall_turns`/`default_turn_budget`/`judge` (+validation, embedded defaults in `config/configs/default.yaml`); `GoalMode.RunVerifyCommand` + `EventLog` audit surfaces; telemetry: `goal_challenged`, `goal_verify_failed`, `goal_judge_error`, `goal_auto_blocked`, `goal_stall_detected`. |
| `2c5a3a8` | Checkpoint: `internal/app/goal_verifier.go` — exec `CommandVerifier` (`$SHELL`/bash fallback, project-dir cwd, 2-min hard timeout, 4KB ANSI-sanitized cap). **Not yet wired.** |

## Remaining work

### Phase 2 — wiring, stall watchdog, handoff

1. **Wire verifier + config** in `initGoalSystem` (`internal/app/subsystems.go:470`):
   - `manager.Mode.SetVerifier(newExecCommandVerifier(projectDir), cfg.Goals.VerifyCommandsEnabled())`
   - `SetMaxVerifyFailures(max(cfg.Goals.MaxVerifyFailures, 0))` — config `-1` = no cap maps to mode `0`.
   - `SetDefaultTurnBudget(max(cfg.Goals.DefaultTurnBudget, 0))` — `-1` = unlimited maps to `0`.
   - Driver stall config (item 2) needs `stall_turns` here too; judge (Phase 3) needs `providerMgr` threaded in (available as `subs.providerMgr` at the call site, `subsystems.go:230` — change signature).
2. **Stall watchdog** in `GoalDriver` (`core/goal_driver.go`, `Drive` loop at :92):
   - New fields: `Probe func() string` (progress fingerprint), `Remind func(string)` (wire to `agentMgr.InjectSystemMessage`), `StallTurns int` (0/-1 disables).
   - After each turn, if active goal is unmanaged (`ManagedBy == ""`): `fp := Probe()`; same as previous → `stale++` else reset `stale` and `stallChallenges`.
   - `stale >= StallTurns` → inject challenge via `Remind` ("no measurable progress in N turns: make measurable progress, revise todos, or block with reason+expectation"), telemetry `goal_stall_detected`, `stallChallenges++`, `stale = 0`. `stallChallenges >= 2` → `MarkBlocked` (actor system, reason "no measurable progress", expectation "user direction on how to proceed").
   - Probe impl in `internal/app` (needs `projectDir` + `Mode`): fingerprint = todo statuses hash + `git status --porcelain` output hash (skip git part when not a repo — todos-only then; document).
   - Tests exist pattern: `core/goal_driver_test.go` (fake agents + real `GoalMode`).
3. **Handoff on promotion** (`internal/app/events.go`):
   - `handleGoalUpdate` (:882): on `GoalChangeCompletion`, stash `update.Snapshot` (or just its `TerminalReason` + `Name`) in a new App field.
   - `promoteNextQueuedGoal` (:931): pass `Handoff: <stashed reason>` + `VerifyCommand: removed.VerifyCommand` into `CreateGoalInput`; clear stash. (Criterion/name already forwarded.)
   - Render handoff in `BuildStaticGoalReminder` (`core/goal/injection.go`): `<untrusted_handoff>` block (escaped, same pattern as criterion) — static reminder is rebuilt per turn, so the handoff survives fresh-context `SetHistory(nil)` resets, unlike a history message. Extend the byte-stability test (`injection_test.go`).

### Phase 3 — judge, audit commands

4. **Judge** (`internal/app/goal_judge.go`, new):
   - Implements `goal.GoalJudge`: `Judge(ctx, JudgeInput) (JudgeVerdict, error)`.
   - Config `goals.judge`: `off` (default) | `same` → `providerMgr.ResolveActiveModel()` | `model:<id>` → `providerMgr.ResolveModelByID(id)` (`provider/manager.go:288,:691`).
   - One-shot call: `provider.StreamSimple(mdl, provider.Context{Context: ctx, SystemPrompt: judgePrompt, Messages: [user case]}, opts)` then accumulate `Delta` over `stream.SeqCtx(ctx)` (pattern: `internal/agentic/agent_streaming.go:327`); `providerMgr.BuildStreamOptions()` (`provider/manager.go:775`) for base opts.
   - Judge prompt: independent auditor, read-only, given objective + criterion + claimed evidence; must end with `VERDICT: PASS` or `VERDICT: FAIL` + one-paragraph rationale. Parse last line; unparseable → error (gate treats errors as fail-open, already implemented in `runVerification`).
   - Wire in `initGoalSystem`: `manager.Mode.SetJudge(judge)` unless `off`.
   - Case text cap (~8KB) to bound cost. Tests: verdict parsing unit test with a canned stream (see `internal/agentic/testutil/simulated.go` for a fake provider).
5. **Audit commands** in `core/commands/goal.go`:
   - `/goal:log` — `Mode.EventLog()` → render last ~20 records (time, type, actor, status, reason, expectation). Register in `goalSubcommandKinds` (:121, `subNone`) + `goalDispatch` (:59).
   - `/goal:verify` — `Mode.RunVerifyCommand(context.Background())` → print output + PASS/FAIL.
   - Command tests pattern: existing `core/commands/goal*_test.go`.

### Then

6. **Prompts:** `prompts/goal/goal.md` (verifyCommand arg, escalation, stall awareness, judge mention), `BuildStaticGoalReminder` (verifyCommand + handoff blocks), `ContinuationPrompt` (`core/goal_driver.go:16`) sync.
7. **Spec:** update `docs/GOALS.md` (verifyCommand lifecycle, watchdog, judge, `/goal:log`/`/goal:verify`, new config keys table).
8. **Gates (must all pass before merge):** `go vet ./...` · `go test -count=1 -race -cover ./...` · `gocognit -over 15 ./...` · `gocyclo -over 12 ./...` · `staticcheck ./...` (project rule: fix root causes, never nolint/suppress). Keep new production functions within complexity budgets (config 20/12, TUI 18/12, else 15/12).
9. Commit per phase with conventional messages; keep `docs/GOALS.md` in sync.

## Key invariants (do not break)

- **Locking:** `GoalMode` exported methods take `m.mu` for their whole body; helpers named `*Locked` assume it held. Slow work (verify exec, judge) must run **unlocked** — follow `RequestComplete` begin/run/finish pattern with the state-unchanged re-check.
- **Gate scope:** only actor=model + unmanaged + criterion recorded. User/runtime/orchestrator paths bypass (orchestrator completes via `MarkComplete` actor=runtime — untouched).
- **Fail-open judge:** judge/provider errors never block completion (telemetry `goal_judge_error` only).
- **`-1` = disable** for the int config keys; `0` = inherit embedded default (mergeIntIfSet semantics in `mergeGoals`).
- **No `ContextResultTool`** exists in the agent tool interfaces — the goal tool calls `RequestComplete` with `context.Background()`; verify exec is bounded by its own 2-min timeout (documented limitation, ESC won't kill it).
- **`verifyCommandsEnabled` gate:** when disabled, `RunVerifyCommand` errors and the gate skips command execution but judge/streak logic still runs.
- **`pendingVerification`/`verifyFailures` are in-memory** by design (session restart re-arms challenge / resets streak) — documented in `docs/GOALS.md`.
- **Escalation reset:** any transition out of `active` resets both (see `applyStatus`).
