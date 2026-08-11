<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Teams of Agents — Specification (DRAFT for review)

Status: **proposal — not implemented**. This document specifies named, reusable
agent *teams* that bind a main model, a companion/reviewer model, and a review
policy into one selectable unit. Goals and orchestration runs may be bound to a
team so the whole workflow executes with the chosen main↔companion pairing.

## 1. Problem statement

Today Goa has the individual pieces of a "team":

- **Main agent** — the interactive session model (`active_provider`/`active_model`).
- **Companion** — a second model (`multiagent.companion_model/companion_provider`)
  wired into the agent pool as role `companion`, running **post-turn reviews**
  (`/companion:agent` — the main agent requests reviews via `request_review`;
  `/companion:framework` — a review runs after every main turn via
  `ForegroundOrchestrator.AfterMainTurn`).
- **Modes** — behavioral profiles (coder/planner/reviewer/custom) with
  per-mode autonomy, tool allowlists, and guards.
- **Orchestrator roles** — `orchestrator.roles.<name>` maps a role name to a
  model/provider/toolset for **runs** (`/orchestrate`).
- **Goals** — autonomous units of work driven by the main agent (or by an
  orchestrator run via `GoalBinder`).

What is missing:

1. There is **no named, reusable bundle** of "main model + companion model +
   review policy". Configuring a pairing means editing
   `multiagent.companion_model` + switching the active model + choosing
   `/companion:agent|framework` — three loosely coupled knobs with no single
   name.
2. **A goal cannot declare which pairing it runs with.** The goal driver always
   continues with whatever the session's active model/companion happen to be.
   A user cannot say "run this goal with the *strong-coder + fast-reviewer*
   team and that goal with the *local-only* team".
3. Review behavior (off / agent-driven / framework / gated) is a session-global
   toggle, not a property of the work being done.
4. Orchestrator **runs** have per-run role models but no relationship to the
   interactive session's main/companion pair.

**Teams** close this gap: one named object that defines the pairing and the
review approach, selectable per session **and** per goal/run.

## 2. Definitions

| Term | Meaning |
|------|---------|
| **Team** | A named, durable configuration object: a set of members + review policy + defaults. |
| **Member** | A named model binding: `model` (config model ID), optional `provider`, `mode` (behavioral mode name), `thinking_level` (§3.6), and a **role tag** (`main`, `reviewer`, or none). |
| **Main member** | Exactly one member per team, tagged `role: main`. Drives conversation turns and goal continuation turns. |
| **Reviewer member** | A member tagged `role: reviewer`. Reviews the main member's output per the review policy. A team may have **several** (see quorum, §3.3). The v1 "companion" is the single-reviewer special case. |
| **Worker member** | A member with no role tag. A delegatable specialist: registered in the agent pool under its member name, spawnable on demand by the main member (§3.4). Workers never drive turns and never review. |
| **Review policy** | When/how reviewer members review: `off`, `agent`, `framework`, `gated`. |
| **Quorum** | With multiple reviewers, how verdicts combine on synchronous gates: `all` (every reviewer must PASS) or `any` (first PASS suffices). |
| **Active team** | The team applied to the interactive session (exactly one, or none). |
| **Team binding** | An association of a goal or an orchestrator run to a team. Overrides the session's active team for that unit of work. |

### 2.1 Design invariants

1. **Teams are configuration, not runtime objects.** Activating a team is
   *applying* its members to existing subsystems (active model, agent pool
   role configs, companion mode). No new agent type is introduced.
2. **Reuse, don't fork.** Teams resolve through the same model factory
   (`providerMgr.ResolveModelByID`), the same agent pool
   (`multiagent.AgentPool.SetConfig`), and the same companion coordinator
   (`core.CompanionCoordinator`) already in place.
3. **Single owner of companion mode.** The team activation path — not the
   user toggling `/companion` manually afterward — sets the companion mode.
   Manual `/companion:*` after activation is an *override* and is surfaced
   as such (see §8).
4. **Goal/Run bindings are durable.** A team binding survives queueing,
   promotion, and session restart, exactly like `freshContext` and
   `verifyCommand` do today.
5. **No behavior change without a team.** When no team is active and no
   binding exists, Goa behaves exactly as today (backward compatible).

## 3. Configuration schema

New top-level section `teams`, following the existing cascade
(embedded → home → project → local → env → flags):

The canonical member form is a **`members` map** with role tags — this is what
the internal model uses from day one, so widening beyond two models is a config
change, not a redesign. The common 2-model case keeps the `main:`/`companion:`
keys as **shorthand** that normalizes into the members map at load time.

```yaml
teams:
  active: pair-strong            # team applied to the session at startup (optional)

  definitions:
    # ── 2-model shorthand (the requested main/companion pair) ──────────
    pair-strong:                 # team name (map key; matches [a-z0-9][a-z0-9-]*)
      description: "Strong coder + fast reviewer"
      main:                      # ≡ members.main + role: main
        model: default           # model ID from `models:` (required)
        provider: ""             # optional provider override
        mode: coder              # behavioral mode name (optional; default = current mode)
        thinking_level: ""       # off|minimal|low|medium|high|xhigh — per-member override (§3.6)
      companion:                 # ≡ members.companion + role: reviewer
        model: fast-reviewer     # model ID from `models:` (required when review != off)
        provider: ""
        mode: reviewer           # default: reviewer
        thinking_level: ""
      review: agent              # off | agent | framework | gated (default: agent)
      review_gates:              # only for review: gated (see §3.2)
        triggers: [goal_complete, file_commit, run_complete]
      defaults:
        autonomy: ""             # optional autonomy applied on activation
        turn_budget: 0           # optional per-goal turn budget default (0 = inherit goals.default_turn_budget)

    local-pair:
      main:    { model: llama-3.2 }
      companion: { model: llama-3.2 }
      review: framework

    solo-fast:
      main: { model: fast-model }
      review: off                # no reviewer — pure solo team

    # ── N-member canonical form (extension beyond two models) ─────────
    full-house:
      description: "Planner + coder + two reviewers with quorum"
      members:
        lead:                    # member name = pool role name
          model: claude-sonnet
          mode: coder
          role: main             # exactly one main per team
        architect:
          model: o4-mini
          mode: planner          # worker: no role tag → delegatable specialist
        style-review:
          model: gpt-4o-mini
          mode: reviewer
          role: reviewer         # participates in the review policy
        sec-review:
          model: claude-opus
          mode: security-reviewer # custom mode (.goa/prompts/mode/…)
          role: reviewer
      review: gated
      review_gates:
        triggers: [goal_complete, file_commit]
        quorum: all              # all | any — verdict combination on synchronous gates (default: all)
      delegation: agent          # agent | off — may main spawn worker members on demand (default: agent)
```

**Normalization:** `main: {…}` ≡ `members: {main: {…, role: main}}`;
`companion: {…}` ≡ `members: {companion: {…, role: reviewer}}`. Mixing both
forms in one team is a validation error. The runtime only ever sees the
normalized members list.

**Same model, different roles via thinking:** because every member carries its
own `thinking_level` (and `mode`), one physical model can play several roles
in a team with different reasoning budgets — e.g. `lead` on
`claude-sonnet/high` while `qa` is `claude-sonnet/low` (§3.6, example §10.6).

### 3.1 Review policies

| Policy | Behavior |
|--------|----------|
| `off` | No reviewer is configured. Equivalent to `/companion:off`. The team is main (+ optional workers) only. |
| `agent` | Agent-driven (today's `/companion:agent`): the main agent decides when to call `request_review`; with multiple reviewers the request names the reviewer (default: the first `role: reviewer` member, also addressable as `companion` for backward compatibility). Requires `tools.enabled.request_review`. |
| `framework` | Framework-driven (today's `/companion:framework`): after **every** main turn, reviewer members review the turn output, bounded by `max_companion_cycles`. With one reviewer this is exactly today's behavior. With several, reviews run **in parallel** and each verdict renders separately. |
| `gated` | Framework-driven, but reviews run **only on defined triggers** (§3.2) instead of every turn. New policy introduced by teams. |

### 3.2 Gated review triggers (new)

`gated` fires the companion review only when at least one trigger fires:

| Trigger | Fires when |
|---------|-----------|
| `turn_end` | Every main turn ends (≡ `framework`; useful for explicit configs). |
| `goal_complete` | The main agent requests `goal` → `complete` (review runs *before* the done-gate challenge is issued; a FAIL verdict rejects the completion like a judge FAIL). |
| `goal_turn` | Every continuation turn of an **active goal** ends (skip interactive turns). |
| `file_commit` | A successful `write`/`edit` tool call lands during the turn (review at turn end, once per turn). |
| `run_complete` | An orchestrator run bound to the team reaches synthesis (the orchestrator's final answer is reviewed before being delivered). |

Semantics: at turn end the framework evaluates triggers; if any matched during
the turn, **one review round** executes — never more than one round per turn,
regardless of how many triggers matched. Within a round, every reviewer member
reviews in parallel (§3.3). `goal_complete` is *synchronous*: the combined
verdict gates the transition (reuses the goal judge plumbing — see §6.3). All
other triggers are asynchronous like today's framework mode (verdicts are
advisory and rendered in the transcript).

### 3.3 Multi-reviewer quorum

When a team has several `role: reviewer` members, a review round runs all of
them **concurrently** (bounded by `orchestrator.pool` caps) and combines
verdicts:

| Quorum | Combined verdict |
|--------|------------------|
| `all` (default) | PASS only if **every** reviewer returns PASS. |
| `any` | PASS as soon as **one** reviewer returns PASS (remaining reviews may be cancelled). |

- **Fail-open per reviewer**: a reviewer that errors (model failure, timeout,
  unparseable verdict) is excluded from the quorum and logged
  (`team_review_error`); it never blocks a gate by itself. If **all** reviewers
  error, the gate passes (consistent with the judge's fail-open contract) and
  the incident is rendered visibly.
- Asynchronous rounds (non-gating triggers) render every reviewer's verdict
  individually — no combination needed.
- Synchronous rounds (`goal_complete`, §5.3) combine per the quorum; a combined
  FAIL rejects the completion with every FAILing reviewer's rationale.

### 3.4 Worker members and delegation

Worker members (no role tag) are **delegatable specialists**: at activation
they are registered in the agent pool under their member name
(`pool.SetConfig(memberName, …)`, same path as §4.2 step 4), making them
addressable by the main agent's delegation tools:

- `agent` tool / `delegate_to` with `role: "<member-name>"` (requires
  `tools.enabled.delegate_to` or the agent tool; respects the member's mode
  toolset and guards like any sub-agent).
- `delegation: off` on the team removes workers from the delegation surface
  (they stay pool-registered for orchestrator runs, §7, but the main agent is
  not told about them).

Workers are deliberately **not** an automatic pipeline: teams define *who* is
available and *when reviews happen*; multi-stage execution flow
(plan → code → review handoffs) remains orchestrator-run territory (§7). The
main agent decides if and when to consult a worker.

### 3.5 Validation rules (`config/config_validate.go`)

1. Team name matches `[a-z0-9][a-z0-9-]{0,63}`; duplicate after merge → higher-priority source wins (normal cascade).
2. Exactly **one** member with `role: main` per team (after shorthand normalization); zero or two+ → error. Member names match the team-name charset and must not collide with reserved pool roles (`orchestrator`) unless intentional for run mapping (§7).
3. Every member's `model` must exist in `models:`; `provider`, when set, must exist in `providers:`.
4. `mode` names must resolve (built-in or user-defined `prompts/mode/<name>/definition.md`); unknown mode → error.
5. `review` must be one of `off|agent|framework|gated` (default `agent` when a reviewer exists). `review != off` requires **at least one** `role: reviewer` member; `review: off` with reviewer members present → error (pointless config).
6. `review_gates.triggers` entries must be known trigger names; `gated` with an empty trigger list → error. `quorum` ∈ `all|any` (default `all`).
7. `delegation` ∈ `agent|off` (default `agent`); `off` with worker members is allowed (workers only serve runs) but warned.
8. `teams.active`, when set, must name a defined team → error otherwise.
9. Mixing `members:` with `main:`/`companion:` in the same team → error.
10. Member `thinking_level`, when set, must be one of
    `off|minimal|low|medium|high|xhigh`.

Unknown-team references anywhere (goal binding, `/team:use`, run binding)
produce a model-actionable error listing defined teams.

### 3.6 Per-member thinking levels

Every member may set `thinking_level`, decoupling reasoning effort from the
model choice — the **same model can serve different roles with different
thinking budgets** inside one team (deep reasoning for the lead, cheap fast
passes for reviewers, a mid setting for a worker).

**Resolution order** for a member's effective thinking level (first wins):

1. **Member `thinking_level`** — the team definition (highest priority).
2. **Pool-role model entry** — for the first reviewer, the existing
   `thinking_levels.companion` config still applies (backward compatibility;
   teams do not break today's per-role thinking config).
3. **Model's own `thinking_level`** — the `models:` entry (runtime `/thinking`
   changes are saved here per model).
4. **Global `thinking_levels.default`** → built-in `medium`.

**Wiring** (existing mechanisms, no new subsystem):

- *Main member*: applied via the same path as a `/thinking:<level>` change on
  the session — takes effect at the next turn boundary and is part of the
  activation snapshot (deactivation restores the previous level).
- *Reviewer/worker members*: passed as `AgentConfig.ReasoningEffort` in
  `pool.SetConfig(memberName, …)` at activation — exactly how
  `configureRoleModels` applies `GetThinkingLevel("companion"/"planner"/"coder")`
  today, so per-role thinking already works end-to-end through the streaming
  architecture (`thinking_budget`, level mapping, and provider compat are
  inherited unchanged).

**Semantics notes:**

- `thinking_level` is per **member**, not per model ID: two members referencing
  the same `models:` entry with different levels produce two independently
  configured pool agents (per-role model isolation already exists via
  `ModelFactory`/`ProviderModelFactory`).
- The member override does **not** mutate the model's saved `thinking_level` —
  it is an overlay applied for the team's lifetime; deactivation / goal-overlay
  teardown restores the prior state (§4.2 snapshot).
- The mode-level `temperature`/`max_tokens` from the member's `mode` definition
  combine orthogonally with `thinking_level` (mode = behavior, thinking =
  reasoning budget).

## 4. Runtime model

### 4.1 TeamManager (new, `core/team.go` + `core/team/` package)

```
TeamManager
  ├── Registry        — merged team definitions from config (read-only)
  ├── Active() *Team  — the session's applied team (nil = none)
  ├── Activate(name)  — apply a team to the session (§4.2)
  ├── Deactivate()    — restore pre-team session state (§4.2)
  ├── Resolve(name)   — look up a definition (for bindings)
  └── events          — team.activated / team.deactivated (event log §9)
```

The manager holds no agents. It *applies* definitions to existing subsystems
and remembers the previous session state for deactivation.

### 4.2 Activation semantics

`Activate(name)` performs, atomically (all-or-nothing; on any failure the
previous state is restored and the error returned):

1. **Normalize + resolve models.** Expand shorthand into the members list;
   every member's `model` → `providerMgr.ResolveModelByID` (or provider
   override). Unknown model aborts.
2. **Snapshot current session state**: active provider/model, mode, companion
   pool config **and every pool role config the team will touch**, companion
   workflow mode, autonomy.
3. **Apply main member**: switch session model (same path as `/model:<id>` —
   the model switch takes effect at the next turn boundary, announced
   immediately); if the main member's `mode` is set, switch mode (same as
   `/mode:<name>`); if the main member's `thinking_level` is set, apply it
   (same path as `/thinking:<level>`, §3.6); if `defaults.autonomy` set,
   apply autonomy.
4. **Apply reviewer + worker members**: for the first reviewer,
   `pool.SetConfig("companion", …)` (backward-compatible pool role); every
   member is additionally registered under its member name
   (`pool.SetConfig(memberName, AgentConfig{ModelName, ProviderID,
   ReasoningEffort-from-§3.6, AllowedTools-from-mode})`) — same code path as
   today's `configureRoleModels`. Cached pool agents for touched roles are
   invalidated so the next use builds with the new model **and thinking
   level**.
5. **Apply review policy**:
   - `off` → companion mode inactive (`SetMinorMode("companion", false)` +
     orchestrator `WorkflowInactive`).
   - `agent` → `/companion:agent` path (`SetMinorMode` + `InjectCompanionReview(true)` + orchestrator `WorkflowAgentDriven`).
   - `framework` → `/companion:framework` path (orchestrator `WorkflowCompanionMinor` + `InjectCompanionReview(false)`); N reviewers fan out per §3.3.
   - `gated` → new `WorkflowCompanionGated` mode on the foreground
     orchestrator + trigger registry (§6.2).
6. **Record binding**: `Active = team`; emit `team.activated` with the full
   resolved definition (members, policy, quorum) for audit.

`Deactivate()` restores the snapshot from step 2 (model/mode/companion
config/companion mode/autonomy) and emits `team.deactivated`.

**Re-activation** (`Activate` while a team is active) deactivates first
(restore) then applies the new team — the snapshot is always taken from the
*pre-team* baseline so nested activation cannot leak state.

### 4.3 Session persistence

The active team name is persisted in the session state
(`core/sessionstate.go`) alongside the active model. On resume:

- The team is re-applied (models resolved again — a model removed from config
  since last session → activation fails loudly, session falls back to no team
  with a visible warning).
- Goal/run bindings are re-read from their own durable records (§5, §7).

### 4.4 Manual overrides while a team is active

After activation the user can still `/model`, `/mode`, `/companion:*`
manually. Semantics:

- The team stays "active" but is marked **drifted** (footer shows
  `team:pair-strong*`).
- `/team:sync` re-applies the team definition (clears drift).
- `/team:off` deactivates (restores the pre-team snapshot, not the drifted
  state).
- `/team:use <name>` replaces the team (re-activation path).

This preserves today's freedom while making the team's intent explicit.

## 5. Goal integration

### 5.1 Team binding on goals

The `goal` tool `create` gains an optional `team` parameter; commands gain
optional flags:

```
/goal:new:implement OAuth2 login --team pair-strong
/goal:next:refactor auth module --team local-pair
goal tool: create(objective=…, team="pair-strong")
```

The binding is stored on the goal record (event-sourced: `goal.create`
payload gains `team`; queue items in `upcoming-goals.json` gain `team`), so it
survives queueing, **promotion** (like `freshContext`), and session restart.

### 5.2 Execution semantics

| Situation | Behavior |
|-----------|----------|
| Goal with `team` binding becomes **active** | The team is applied **for the duration of that goal**: if the bound team differs from the session state, the goal driver applies it before the first continuation turn and restores the prior state when the goal leaves `active` (complete/pause/block/cancel/postpone). Nested team application uses the same snapshot/restore discipline as §4.2 (per-goal snapshot). |
| Goal without binding | Runs with the session state (active team if any, else current model/companion) — today's behavior. |
| Bound team missing at promotion time | Goal is created **paused** with a visible reason (`team "x" not defined`), mirroring the resume-demote pattern; user fixes config and `/goal:resume`. |
| Goal paused/blocked → resumed | The bound team is re-applied on resume (it may have changed in config since — resolution is always at apply time). |
| Queue head promotes after completion | Its own binding applies; the completed goal's team overlay is torn down first. |

**Precedence:** goal binding > run binding (§7) > session active team >
session model state. Orchestrator-managed goals bound to a run always inherit
the **run's** team (§7.3) — an explicit per-goal `team` on a run-managed goal
is rejected with a model-actionable error.

### 5.3 Review interplay with the done-gate

With `review: gated` + trigger `goal_complete` (or `review: framework` when
the goal completes on a turn boundary):

1. Model calls `goal` → `complete` (first call).
2. **Before** the done-gate challenge is issued, the team's reviewers run a
   synchronous review round on the claimed completion (objective + criterion +
   evidence, read-only — same input contract as `goals.judge`).
3. Combined verdict per the quorum (§3.3): `PASS` → done-gate proceeds
   normally (challenge → verify command → judge). `FAIL` → completion rejected
   with every FAILing reviewer's rationale, exactly like a judge FAIL
   (fail-open per reviewer, §3.3).

This makes the companion a **first-class completion auditor**, complementary
to `goals.judge` (which remains available independently; both may run — judge
config is unchanged).

### 5.4 Turn budget default

`teams.definitions.<name>.defaults.turn_budget`, when > 0, is applied to goals
created with that team binding (explicit `set_budget` always wins). 0 =
inherit `goals.default_turn_budget`.

## 6. Companion/review architecture changes

### 6.1 New workflow mode

`multiagent.WorkflowCompanionGated` added next to
`WorkflowCompanionMinor`/`WorkflowAgentDriven`. `/companion` status renders it
as `enabled (gated: goal_complete,file_commit)`.

### 6.2 Trigger evaluation (foreground orchestrator)

The foreground orchestrator gains a `GateTriggers` bitmask + per-turn match
set. Hooks (all existing emission points):

| Trigger | Hook |
|---------|------|
| `turn_end` / `goal_turn` | Turn completion; `goal_turn` additionally requires an active unmanaged goal. |
| `file_commit` | `write`/`edit` tool success event (already observable via the tool event stream). |
| `goal_complete` | Goal tool `complete` action intercepted in the goal driver (synchronous path §5.3). |
| `run_complete` | Orchestrator runtime synthesis step (`RecordAgentMessage` final answer point). |

`AfterMainTurn` is generalized to `RunGatedReview(ctx, output, matchedTriggers)`:
today's `WorkflowCompanionMinor` behaves as `gated` with only `turn_end`.

### 6.3 Synchronous completion review

New narrow interface (dependency inversion, mirrors `GoalBinder`):

```go
// CompletionReviewer reviews a claimed goal completion before the gate.
type CompletionReviewer interface {
    ReviewCompletion(ctx context.Context, in ReviewInput) (verdict Pass/Fail, rationale string, err error)
}
```

Production implementation runs a **review round**: every `role: reviewer`
member of the active/bound team (single reviewer = today's `companion` pool
role) with the reviewer-mode prompt + the `VERDICT: PASS/FAIL` output
contract (reuses `goals.judge`'s parsing), fanned out in parallel and combined
per the team's quorum (§3.3). Wired into `GoalMode`/driver next to the judge.
Fail-open per reviewer (telemetry `team_review_error`).

### 6.4 Per-member prompt/mode resolution

The companion's mode (`companion.mode`, default `reviewer`) resolves through
`ModeRegistry` exactly like today's `resolveAllowed(MajorReviewer)` —
allowed tools, temperature, guard. Custom modes (`.goa/prompts/mode/…`) are
valid member modes, enabling e.g. a `security-reviewer` companion.

## 7. Orchestrator integration

### 7.1 Run binding

`/orchestrate new` gains `--team <name>`; the run record (`RunStarted` event
payload) stores the team name. Resolution at run start:

1. Team's **main member** → the run's **`orchestrator` role** model (hub
   topology) — the orchestrator agent reasons with the team's main model.
2. Team's **reviewer members** → the run's **`companion`/`reviewer` role**
   (first reviewer) plus same-named roles for additional reviewers,
   registered via `pool.SetConfig` before the run starts.
3. Team's **worker members** → same-named run roles (`architect` → role
   `architect`), delegatable by the orchestrator agent.
4. Explicit `orchestrator.roles.*` config still wins for *named specialist
   roles* (coder, explorer…). The team fills the orchestrator slot and any
   member-named slots that have no explicit role entry; existing entries are
   **not** overridden (team ≤ explicit role config).

### 7.2 Review inside runs

- `review: framework|gated(run_complete)` → the orchestrator's synthesis is
  reviewed by a review round (all reviewer members, quorum per §3.3) before
  `RunFinished` (advisory; failures are annotated on the run record, not
  blocking).
- `review: off` → no reviewer role is configured for the run.

### 7.3 Run-managed goals

A run created with `--goal` and `--team` binds **both** to the run; the goal
inherits the run's team (no separate goal-level application — the goal is
driven by the run, not the goal driver).

## 8. Command & TUI surface

### 8.1 Commands (`core/commands/team*.go`, self-registered)

The command behaves **like `/model`**: bare `/team` opens an interactive
selector over the defined teams (active team highlighted, plus a
`— none —` entry to deactivate); `/team:<name>` switches directly with an
announcement, persistence of `teams.active`, and a footer refresh.

| Command | Action |
|---------|--------|
| `/team` | Interactive team selector (like `/model`'s picker): team names with main/companion models + review policy as descriptions, active team preselected, `— none —` deactivates. |
| `/team:<name>` | Activate a team directly (like `/model:<id>`): apply §4.2, persist `teams.active`, announce, refresh footer. |
| `/team:off` | Deactivate; restore pre-team session state. |
| `/team:status` | Status: active team (+drift marker), resolved models, review policy, bindings (goals/runs using it). |
| `/team:list` | All defined teams with their main/companion models and review policy (non-interactive render). |
| `/team:sync` | Re-apply the active team definition (clear drift). |
| `/team:show:<name>` | Render a team definition (resolved models/providers, effective mode toolsets). |

`/team:use:<name>` is kept as an alias of `/team:<name>` for script
readability. Completions for team names from the merged registry.
`/goal:new` and `/goal:next` accept `--team <name>` (completion-aware).
`/orchestrate new` accepts `--team <name>`.

### 8.2 Footer/status

Footer gains a team segment when active: `⛃ pair-strong (review:agent)` —
drifted shown as `pair-strong*`. The existing goal panel shows the goal's
bound team when different from the session team.

### 8.3 Config menu — full team-definition CRUD

`/config` gains a **Teams** root item with **full add/edit/remove of team
definitions** (not just active-team selection), mirroring the existing
`/config → Orchestrator → Roles` flow (`core/commands/config_orchestrator.go`):
list → detail → field edit / `— add —` / remove-confirm, with every mutation
persisted via `saveHomeSection([]string{"teams"}, cfg.Teams)` and revalidated
per §3.5.

```
/config → Teams
├── Active team            → selector over defined teams + "(none)"; sets teams.active
├── Team definitions       → N defined
│   ├── <team-name> …      → Team detail (below)
│   └── — add team —       → wizard
└── (footer shows active team + drift marker)
```

**Team detail** (`/config → Teams → <name>`):

| Item | Behavior |
|------|----------|
| Description | text input |
| Review policy | selector: `off / agent / framework / gated` |
| Gated triggers | multi-select of §3.2 trigger names + `quorum: all/any` selector (only when `gated`) |
| Delegation | `agent / off` toggle (only when worker members exist) |
| Members | list → member detail; `— add member —`; remove member (guard: cannot remove the last `role: main`; cannot end with 0 or 2+ mains — validation runs on save) |
| Defaults | `autonomy` selector, `turn_budget` numeric input |
| Remove team | confirm dialog; refused with explanation while the team is `teams.active` or bound to any queued goal (must unbind first) |

**Member detail** (`… → Members → <member>`): `model` (selector over
`models:`), `provider` (selector + "(default)"), `mode` (selector over
built-in + custom modes), `thinking_level` (selector over
`off|minimal|low|medium|high|xhigh|— inherit —`), `role` (selector
`main / reviewer / worker`; choosing `main` on one member demotes the
previous main to worker after confirmation), remove member.

**Add-team wizard**: name → main member (model/mode/thinking) → review
policy → if policy ≠ off: reviewer member(s) (model/mode/thinking, repeatable
"add another reviewer") → optional worker members → save + validate. The
wizard only asks for the shorthand-shaped team; N-member canonical form is
reachable by adding members afterwards in the detail view.

**Live-effect semantics:** editing a definition does **not** retroactively
re-apply to running goals/runs (their overlays were resolved at apply time);
editing the definition of the *active* team marks it drifted (`team*`) until
`/team:sync` re-applies. All edits are home-level persists
(`~/.goa/config.yaml`), consistent with the orchestrator-roles editor.

### 8.4 TUI rendering of reviews

Unchanged: gated/framework reviews render as today (companion messages in the
transcript, `show_inter_agent_messages` honored). A synchronous
`goal_complete` review renders as a marker: `⚑ team review: PASS/FAIL —
<rationale>` (same style as the judge verdict marker).

## 9. Persistence & telemetry

- `team-events.jsonl` (append-only, project `.goa/`): `team.activated`,
  `team.deactivated`, `team.review` (verdict, trigger, goal/run id, elapsed).
  Follows `goal-events.jsonl` conventions. Retention piggybacks on
  `goals.retention`.
- Goal records: `team` field on `goal.create` payload + queue items
  (versioned; old files without `team` load fine — backward compatible).
- Run records: `team` in `RunStarted` payload.
- Session state: active team name.
- Telemetry events: `team_activated`, `team_deactivated`,
  `team_review_verdict{pass|fail}`, `team_review_error`,
  `team_goal_bound` — same client as goal telemetry.

## 10. Worked examples

### 10.1 Define and use a pair for the session

```yaml
# .goa/config.yaml
teams:
  definitions:
    deep-work:
      main:      { model: claude-sonnet }
      companion: { model: gpt-4o-mini }
      review: agent
```

```
/team:use:deep-work        → team active: main=claude-sonnet, companion=gpt-4o-mini, review=agent
/goal:new:implement OAuth2 → goal runs under deep-work; main agent may request reviews
```

### 10.2 Per-goal teams

```
/team:use:deep-work
/goal:new:fix flaky tests --team local-pair    # this goal uses local models only
/goal:next:polish docs                          # queued goal inherits session state (deep-work)
```

When `fix flaky tests` completes, its local-pair overlay is torn down and
`polish docs` runs under deep-work.

### 10.3 Gated review as a completion auditor

```yaml
teams:
  definitions:
    audited:
      main:      { model: claude-sonnet }
      companion: { model: o4-mini, mode: reviewer }
      review: gated
      review_gates: { triggers: [goal_complete, file_commit] }
```

The companion reviews every turn that touched files, and **must PASS** before
any goal completion reaches the done-gate.

### 10.4 Team-bound orchestration run

```
/orchestrate:new:hub --team deep-work --goal "migrate auth to OIDC"
```

Run's orchestrator agent = claude-sonnet; reviewer role = gpt-4o-mini;
synthesis reviewed before `RunFinished`; goal completes via the run.

### 10.5 Multi-member team with quorum (extension beyond two models)

```yaml
teams:
  definitions:
    release-crew:
      members:
        lead:      { model: claude-sonnet, mode: coder, role: main }
        architect: { model: o4-mini, mode: planner }          # worker
        qa:        { model: gpt-4o-mini, mode: reviewer, role: reviewer }
        sec:       { model: claude-opus, mode: security-reviewer, role: reviewer }
      review: gated
      review_gates: { triggers: [goal_complete, file_commit], quorum: all }
```

```
/goal:new:harden the payment endpoint --team release-crew
```

The lead codes; it may delegate design questions to `architect` on demand;
every file-touching turn gets parallel qa+sec reviews; goal completion is
rejected unless **both** reviewers PASS.

### 10.6 Same model, different thinking per role

```yaml
teams:
  definitions:
    solo-grades:
      members:
        lead:   { model: claude-sonnet, mode: coder,    role: main,     thinking_level: high }
        checks: { model: claude-sonnet, mode: reviewer, role: reviewer, thinking_level: low }
      review: gated
      review_gates: { triggers: [goal_complete] }
```

One physical model, two reasoning budgets: the lead thinks deeply while the
reviewer runs cheap fast passes — per-member `thinking_level` (§3.6) overrides
the model's saved level for the team's lifetime without mutating it.

## 11. Non-goals (v1)

- **Automatic multi-stage pipelines at session level** (planner → coder →
  reviewer handoffs without the main agent's initiative): workers are
  on-demand delegation targets (§3.4); scripted flow stays with orchestrator
  runs (§7).
- **Reviewer-to-main conversational loops** (back-and-forth beyond one
  verdict round per trigger): bounded by design; `max_companion_cycles`
  governs agent-driven exchanges.
- **Per-goal autonomy/mode overrides** beyond what the team defines.
- **Cross-provider failover** inside a team (model fallback stays a
  provider-manager concern).
- **Import/export of team definitions** (shareable team files) — YAML editing
  plus `/config` CRUD (§8.3) covers authoring in v1.
- **Team-scoped token budgets** (goal/run budgets already exist; aggregate
  team budget is a later addition).

## 12. Implementation plan (phases, each independently shippable)

| Phase | Scope | Key files |
|-------|-------|-----------|
| **1. Schema** | `teams` config section, structs, shorthand normalization, merge, defaults, validation (§3.5), config completion | `config/config.go`, `config/defaults.go`, `config/config_merge.go`, `config/config_validate.go`, `core/commands/config_completion.go` |
| **2. TeamManager** | Registry, Activate/Deactivate with snapshot/restore (N members), worker pool registration, drift detection, session persistence | `core/team.go`, `core/team/manager.go`, `core/sessionstate.go`, `internal/app/subsystems.go` |
| **3. Commands + footer** | `/team*` commands, footer segment, `/config` section, status rendering | `core/commands/team*.go`, `tui/footer_data.go`, `core/commands/config_menu*.go` |
| **4. Goal binding** | `team` on goal create/queue/promote, driver apply/restore overlay, missing-team pause contract | `core/goal/model.go`, `core/goal/store.go`, `core/goal_queue.go`, `core/goal_driver.go`, goal tool schema |
| **5. Gated review + quorum** | `WorkflowCompanionGated`, trigger hooks, `RunGatedReview` fan-out, quorum combination, synchronous `CompletionReviewer` + done-gate wiring | `multiagent/foreground_orchestrator.go`, `core/companion.go`, `core/goal/`, `core/goal_driver.go` |
| **6. Worker delegation** | Member-name pool roles surfaced to `agent`/`delegate_to`, `delegation: off` filtering, mode toolset enforcement | `multiagent/agent_tool.go`, `multiagent/agent_driven_tools.go`, `core/team/` |
| **7. Orchestrator binding** | `--team` on runs, member→role fill-in (orchestrator/reviewers/workers), synthesis review round, run record | `core/orchestrator/runtime.go`, `core/commands/orchestrator*.go` |
| **8. Docs + QA** | USER-GUIDE, CONFIGURATION reference, e2e scenarios (activate/bind/gated-review/quorum/worker-delegation/run) | `docs/`, `.goa/skills/qa-e2e` scenarios |

### 12.1 Test approach (per Hard Rule 3)

- **Config**: table-driven parse/merge/validate — every §3.5 rule has a
  failing-case test; unknown model/provider/mode/team references error;
  shorthand↔members normalization; both-forms mixing rejected; exactly-one-main
  enforced.
- **TeamManager**: activation applies expected pool configs + model switch
  (fake provider manager) for 1-reviewer and N-member teams; failure
  mid-activation restores snapshot; re-activation does not leak; drift
  detection on manual `/model`; worker members pool-registered under member
  names.
- **Thinking levels**: member `thinking_level` lands in
  `AgentConfig.ReasoningEffort` per role; two members sharing one model ID
  with different levels get independently configured agents; main-member level
  applied/restored with activation snapshot; member override never mutates the
  model's saved `thinking_level`; invalid level rejected by validation; legacy
  `thinking_levels.companion` still applies when the team member sets no level.
- **Goal binding**: event-sourced round-trip (create→queue→promote→restart→
  binding intact); overlay applied on activation and restored on
  complete/pause/postpone; missing team → created paused.
- **Gated review + quorum**: fake reviewer models scripted with PASS/FAIL —
  trigger matrix (each trigger fires exactly one review round per turn);
  `quorum: all` FAIL when one reviewer FAILs; `quorum: any` PASS on first PASS;
  reviewer error excluded from quorum; all-reviewers-error is fail-open;
  `goal_complete` combined FAIL rejects completion.
- **Worker delegation**: main can spawn a worker member via `agent`/`delegate_to`
  with its mode toolset; `delegation: off` hides workers from the tool surface;
  worker runs respect pool caps.
- **Orchestrator**: run with team → orchestrator agent uses team main model,
  reviewer + worker roles configured, synthesis review round recorded; explicit
  roles not overridden.
- **Regression**: no-team session behaves byte-identically to today
  (existing companion/goal/orchestrator test suites must pass unchanged).
- Gates: `go vet`, `staticcheck`, `gocognit -over 15`, `gocyclo -over 12`,
  `go test -count=1 -race -cover ./...` — run separately.

## 13. Alternatives considered

| Alternative | Rejected because |
|-------------|------------------|
| Extend `multiagent.*` keys instead of a new section | No naming, no review policy, no per-goal binding possible; keeps the three-loose-knobs problem. |
| Reuse `orchestrator.roles` as the team definition | Roles are per-run worker bindings with pool caps; they have no session-level main-model semantics and no review policy. Teams *consume* roles for runs (§7) rather than duplicate them. |
| A "reviewer mode" toggle without model binding | Modes deliberately have no model binding (they're behavioral). The requested feature is precisely the model+review pairing. |
| Implement via orchestrator only (goals always run inside runs) | Heavyweight for the common case (pair programming with review); the goal driver is the right executor for single-threaded autonomous goals. |
| Gated review via `goals.judge` only | Judge only audits completion; teams also need per-turn / per-file review, and the same companion should serve both. |

## 14. Open questions for review

1. **Drift semantics** — is the "active but drifted" state acceptable, or
   should manual `/model`/`/companion` changes auto-deactivate the team?
   (Spec chooses drift-marking as least surprising.)
2. **`gated` trigger set** — is `file_commit` too noisy in practice? Should
   it be rate-limited (e.g. max 1 review per N minutes)?
3. **Team switching mid-turn** — activation applies at the next turn boundary
   (consistent with model/mode switches); acceptable?
4. **`main.mode` switching** — should activating a team switch the session
   mode at all, or only apply mode-derived toolsets to the companion?
   (Spec switches the session mode when set; reviewer may prefer companion-only.)
5. **Queue-scope** — should `/goal:queue:manage` show/edit team bindings
   (spec: display only in v1)?
6. **Naming** — `teams.definitions.<name>` vs `teams.<name>` (spec avoids the
   ambiguity with `teams.active` by nesting under `definitions`).
7. **Quorum default** — `all` is the safe default (a FAIL from any reviewer
   blocks); is `any` (first-PASS wins) actually useful beyond cost savings,
   or should it be dropped?
8. **Worker visibility** — should worker members appear in the main agent's
   system prompt roster automatically (spec: yes, via the delegation tool
   descriptions when `delegation: agent`), or only on explicit `/team:show`?
9. **Framework fan-out cost** — with `review: framework` and N reviewers,
   every turn costs N review calls. Acceptable, or should framework mode be
   limited to the first reviewer with quorum reserved for gates?
10. **Thinking drift** — manual `/thinking:<level>` while a team is active:
    drift-marked like `/model` (spec's current behavior, consistent), or
    exempt since thinking is cheaper to toggle than a model switch?
