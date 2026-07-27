<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Goals

Goa supports **autonomous goals**: long-running, multi-turn tasks that the agent pursues on your behalf. This document is the goal subsystem **specification**: lifecycle, enforcement contracts, queue semantics, budgets, and persistence.

## Concepts

- **Active goal** — exactly one per session. The runtime drives continuation turns until the goal ends.
- **Queue** — a durable FIFO of upcoming goals (`upcoming-goals.json`). The head auto-promotes when the active goal completes.
- **Completion criterion** — an optional, recorded done-condition. It gates completion (see *Done-gate*).
- **Event log** — `goal-events.jsonl`, the source of truth. The TUI and session reload are projections of it.

## Starting a goal

```text
/goal                          # interactive: prompts for the objective
/goal:new:fix the failing checkout tests
/goal:new:implement OAuth2 login with unit tests
```

A goal needs a verifiable end state. If your request is vague, the model will ask for a completion criterion before creating the goal. Each goal gets a **friendly alias** (e.g. `happy.fox`) shown in status and the queue manager. The model can also create goals itself with the `goal` tool (`create` action), including several at once via the `objectives` array — the first starts active, the rest queue FIFO.

## Lifecycle contract

| Status   | Meaning                                            | Allowed transitions |
|----------|----------------------------------------------------|---------------------|
| active   | The agent is working autonomously on the goal.     | → paused, blocked, complete |
| paused   | Goal is parked; not pursued.                       | → active |
| blocked  | Goal hit an external blocker; not pursued.         | → active |
| complete | Terminal. The record is cleared; the queue head promotes. | — |

- Only the **active** goal consumes continuation turns, budget, and tokens.
- `complete` is the only terminal status; the goal record is cleared after the completion event is emitted.
- `cancel` (`/goal:cancel`) discards the goal at any status without a completion event.

Control commands:

```text
/goal:status   # show current goal
/goal:pause    # pause active goal
/goal:resume   # resume paused/blocked goal
/goal:cancel   # discard current goal
/goal:replace <objective>   # ask confirmation then abandon current goal
/goal:log      # show recent goal event records
/goal:verify   # run the recorded verify command now
```

## Terminal-answer contract (enforcement)

Model-initiated transitions out of `active` **must be justified**. This exists because unexplained pauses forced users to say "please continue", defeating the purpose of autonomous goals.

| Transition | `reason` | `expectation` | Notes |
|------------|----------|---------------|-------|
| `paused`   | **required** | optional | Why the goal must yield. The model is instructed to keep working whenever progress is possible and to use `blocked` (not `paused`) when waiting on input. |
| `blocked`  | **required** | **required** | The concrete blocker, plus exactly what input/change unblocks it. Rendered in the TUI marker and the blocked reminder so the user knows what to provide. |
| `complete` | required when the done-gate asks (see below) | — | Carries the verification evidence; persisted as the terminal reason. |
| `active` (resume) | optional | — | |

Missing justifications are rejected with a model-actionable error; the goal stays active.

**Proactive resume:** when a goal is paused or blocked, the per-turn reminder instructs the model to resume immediately — in the same turn — when the user's message asks to continue (any phrasing) or supplies the recorded unblock condition. The user never has to issue an exact command.

## Done-gate (`goals.done_gate`)

How strictly model-initiated completion is checked before the goal may close. Configurable:

```yaml
goals:
  done_gate: verify    # verify (default) | evidence | off
```

- **`verify` (default)** — when a completion criterion is recorded, the model's first `complete` call is *intercepted*: the goal stays active and the tool returns a **verification challenge** restating the criterion (without stopping the turn). The model self-audits, then calls `complete` again with `reason` citing concrete evidence (commands run, outputs observed, tests passing). The second call runs machine verification (below) and closes the goal when it passes.
  - If the audit fails, the model simply keeps working — the goal was never closed.
  - Any other transition (pause/blocked/resume) re-arms the challenge.
  - Session restart mid-verification re-arms the challenge (the pending flag is in-memory only).
- **`evidence`** — single-call variant: `complete` must carry `reason` with the validation evidence. No challenge round-trip; machine verification still applies.
- **`off`** — legacy behavior: `complete` closes immediately (no challenge, no machine verification).

The gate applies only to **model-initiated** completions of goals **with a recorded criterion**. User completions (`/goal` flows), runtime completions (driver, orchestrator binders), and orchestrator-managed goals bypass it.

### Machine verification (`verifyCommand`)

A goal may record a **verify command** — a machine-checkable done-condition supplied at creation (goal tool `create` arg `verifyCommand`, preserved through queueing and promotion). After the model confirms completion (challenge answered, evidence given), the done-gate executes the command through the system shell (`$SHELL`, bash fallback) in the project directory:

- **exit 0** → completion proceeds; **non-zero** → completion is rejected with the output tail (last 10 lines) as the failure detail.
- Execution is bounded by a **2-minute hard timeout** and the returned output is ANSI-sanitized and capped at 4 KB. The call runs unlocked, so it cannot deadlock goal state — but ESC does not interrupt it (the goal tool has no cancellable context; documented limitation).
- `goals.verify_commands: false` disables execution globally: the gate then skips the command (judge/streak logic still runs), and `/goal:verify` errors.
- **Todo consistency:** a gated `complete` is rejected while any goal todo is not `done` — finish the checklist or update it first.
- **Escalation:** consecutive verification failures increment an in-memory streak; at `goals.max_verify_failures` (default 3) the goal is **auto-blocked** for user review (reason: "verification failed N times consecutively"). `-1` removes the cap (not recommended). Any transition out of `active` or a session restart resets the streak.
- `/goal:verify` runs the recorded command on demand and prints its output plus PASS/FAIL.

### Independent judge (`goals.judge`)

An optional second, semantic auditor after the verify command (or alone when no command is recorded):

```yaml
goals:
  judge: off        # off (default) | same | model:<id>
```

- `same` audits with the active model; `model:<id>` uses a configured model.
- The judge is read-only and sees only the objective, the recorded criterion, and the claimed evidence (capped ~8 KB). It must end with `VERDICT: PASS` or `VERDICT: FAIL`; FAIL rejects the completion with the judge's rationale.
- **Fail-open:** judge errors (model resolution, stream failure, unparseable verdict) never block completion — they emit `goal_judge_error` telemetry only.

### Stall watchdog (`goals.stall_turns`)

The goal driver fingerprints progress after every continuation turn of an **unmanaged** goal: the hash combines the goal's todo list (ID+status) with `git status --porcelain` of the workspace. Outside a git repository the git half degrades to a constant, leaving a todos-only fingerprint.

- An unchanged fingerprint for `goals.stall_turns` (default 5) consecutive turns injects a **stall challenge** ("make measurable progress, revise todos, or block with reason+expectation") and emits `goal_stall_detected`.
- A changed fingerprint resets the streak and the challenge count; a new goal resets both.
- After **2 unanswered challenges** the goal is **auto-blocked** (reason "no measurable progress", expectation "user direction on how to proceed").
- `stall_turns: -1` (or 0) disables the watchdog. Orchestrator-managed goals are never watched (the orchestrator supervises them).

### Default turn budget (`goals.default_turn_budget`)

When > 0 (embedded default 50), every newly created goal gets that hard turn ceiling unless a budget is set explicitly later. `-1` = unlimited.

## Queued goals

```text
/goal:next refactor the auth module
/goal:next                    # interactive: prompts for the objective
/goal:manage                  # open the queue manager
/goal:reorder:1B,2C,3A        # reorder by letter mapping
```

**Promotion contract:** when the active goal completes, the queue head promotes automatically and preserves its **objective, completion criterion, verify command, fresh-context flag, and friendly alias** (traceability). The promoted goal also inherits the predecessor's completion evidence as a **handoff** note, rendered as an untrusted block in the per-turn goal reminder (rebuilt every turn, so it survives fresh-context resets). On promote failure the item is restored to the front of the queue. The model's batched `create` applies the call's `completionCriterion` to every objective, including queued ones.

## Budgets

The model can set hard limits with `goal` action `set_budget`:

```text
stop after 20 turns
use no more than 500k tokens
finish within 30 minutes
```

Supported units: `turns`, `tokens`, `milliseconds`, `seconds`, `minutes`, `hours`. When a budget is exceeded, the goal is marked **blocked** by the system (reason: budget reached).

## Todo lists

A goal can carry a framework-managed todo list (`add_todo` / `update_todo`). The list is persisted in the event log and surfaced to the model every turn in the dynamic progress reminder, which directs it to work the next pending item — the framework owns the follow-up turns.

## Fresh context

`freshContext: true` runs a goal's continuation turns on a clean context (objective + system prompt only). The prior conversation is preserved in the durable transcript and is visible on reload, but is not sent to the agent for that goal; a visible boundary marker is injected at the switch. The flag survives queueing and promotion.

**Default: ON.** New goals start on a clean context unless opted out — per goal (`freshContext: false` in the goal tool, `/goal:new:reuse <text>` or `/goal:next:reuse <text>` on the command line) or globally via `goals.fresh_context: false` (/config → Goals → "Fresh context for new goals"). `/goal:new:fresh <text>` forces a clean context even when the configured default is `reuse`.

## Persistence & audit

- `goal-events.jsonl` — append-only event log (`goal.create`, `goal.update`, `goal.clear`). Status updates record actor, reason, expectation, and counters. `/goal:log` renders the last ~20 records (time, type, actor, status, reason, expectation).
- `upcoming-goals.json` — the durable queue (versioned, backward compatible).
- `/goal:verify` — run the active goal's recorded verify command on demand; prints output + PASS/FAIL.
- On session resume, an `active` goal is demoted to `paused` ("Paused after agent resume") so it never silently runs without the user present.
- `goals.retention` controls how long terminal records are kept.
- In-memory by design (reset on session restart): the pending done-gate challenge, the verification-failure streak, and the stall-watchdog streak.

### Configuration reference

| Key | Default | Meaning |
|-----|---------|---------|
| `goals.done_gate` | `verify` | Completion gate: `verify` (challenge round-trip), `evidence` (reason required), `off`. |
| `goals.verify_commands` | `true` | Execute recorded verify commands at completion. |
| `goals.max_verify_failures` | `3` | Consecutive verification failures before auto-block. `-1` = no cap. |
| `goals.stall_turns` | `5` | Stale turns before a stall challenge. `-1`/`0` = disabled. |
| `goals.default_turn_budget` | `50` | Implicit hard turn ceiling for new goals. `-1` = unlimited. |
| `goals.judge` | `off` | Independent completion auditor: `off`, `same`, `model:<id>`. |

Telemetry: `goal_challenged`, `goal_verify_failed`, `goal_judge_error`, `goal_auto_blocked`, `goal_stall_detected`.

## Events in the TUI

- A bordered **Goal** panel appears while a goal is active.
- Lifecycle transitions render as low-profile markers (blocked markers show the unblock condition: `needs: …`).
- Completion renders a summary card with stats.
- Goal status appears in the footer status bar.

## Autonomy and starting goals

In non-yolo autonomy modes, starting a goal shows a permission dialog. You can switch to auto/yolo mode or proceed in the current mode.