<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Plan Mode Specification

This document defines the complete design for `/plan`: a dedicated planner mode
with a structured plan tool, an annotation-driven review UX, and orchestrated
sequential execution by limited-context sub-agents. It consolidates all
clarifications from the planning discussion and is intended to be the
implementation reference.

Source idea: `TODO.md` (2026-07-16). Confirmed decisions:

- New `/plan` command + plan mode; reuse the review-pager UX for annotation
  and the orchestrator hub for execution. The legacy PLAN.md `plan_mode` tool
  is deprecated (not removed) once `/plan` ships.
- The plan is a **structured item list** (source of truth) plus a **generated
  Markdown document** rendered from it for the review UI.
- Review UX is a **full-screen pager** like `tui/review_pager.go`.
- Execution is **sequential**: the orchestrator dispatches one plan item at a
  time; the user watches each delegation in the conversation view.
- Limited-context workers via **new `context_window` / `max_tokens` fields on
  orchestrator roles**; the existing compression machinery handles the rest.
- Execution visibility includes a **dedicated plan-status overlay**.

## 1. Scope and Goals

- Replace freeform Markdown planning (PLAN.md) with a structured, tool-managed
  plan that a planner agent creates and refines.
- Let the user review and annotate the plan in a full-screen pager, anchored
  to plan items, with annotations flowing back to the planner for rework.
- On approval, execute the plan through the existing orchestrator hub: the
  orchestrator dispatches one item at a time to small-context worker agents.
- Give workers a typed outcome channel (done / needs-clarification / blocked)
  so clarification flows worker → orchestrator → user → orchestrator → worker.
- Persist plans as event-sourced, resumable entities independent of runs.
- Keep every phase green under the standard gates (`go vet`,
  `go test -count=1 -race -cover`, `gocognit -over 15`, `gocyclo -over 12`).

Non-goals:

- Parallel/DAG-concurrent execution (dependencies are validated, but dispatch
  is strictly one item in flight). May be revisited after v1.
- Removing the legacy `plan_mode` tool or PLAN.md flows (deprecation only).
- Plan templates, plan sharing/export formats beyond the rendered Markdown.

## 2. Terminology

| Term | Meaning |
|------|---------|
| **Plan** | A persisted, event-sourced work plan under `.goa/plans/<plan-id>/`. |
| **Plan Item** | One unit of work: stable ID, title, self-contained description, status, dependencies, suggested role, result. |
| **Revision** | A numbered snapshot of the plan each time the planner submits it for user review. |
| **Comment / Annotation** | A user note anchored to a plan item ID (or to the plan itself), stable across revisions. |
| **Planner** | The agent that drafts and refines the plan. Uses the orchestrator role (large context). |
| **Worker** | A sub-agent executing one plan item with a limited context window and no cross-item memory. |
| **Planning Phase** | Plan status `draft`/`in_review`: the planner owns the `plan` tool's structural actions. |
| **Execution Phase** | Plan status `approved`/`executing`: the orchestrator owns the `plan` tool's execution actions and delegates items. |
| **Run** | The orchestrator run (`core/orchestrator`) bound to a plan during execution. |

## 3. Existing Building Blocks (verified)

- `core/orchestrator` — hub topology, `delegate` (async, `new_agent` flag),
  `rework`, `Runtime.AskUser` (pauses the loop and prompts the user),
  event-sourced runs, pool caps, steering. Execution reuses all of this.
- `internal/app/orchestrator_adapter.go` — agent factory wiring tools per
  role; the place where worker context limits and the worker outcome tool are
  injected.
- `internal/agentic/agent_context.go` — `SetContextWindow` and
  `ContextCompression.MaxTokens` already bound an agent's effective window;
  compression summarizes overflow. Role-level limits are pure wiring.
- `tui/review_pager.go` + `internal/review` — the annotation UX reference:
  pure renderer/key-router pager, all text entry on the main input line,
  `MarkdownSummary` submitted to the agent via `ctx.SubmitToAgent`.
- `core/plan/mode.go` + `tools/plan/plan_mode.go` — legacy PLAN.md plan mode.
  Untouched except for deprecation docs.
- `internal.PrefixedHexID`, `internal.FriendlyNameUnique` — ID and friendly
  name generation, same pattern as orchestrator runs.

## 4. Data Model

Package `core/plan` gains new files (`model.go`, `store.go`, `render.go`,
`annotations.go`); `mode.go` (legacy PLAN.md state) stays as-is.

### 4.1 Types

```go
type PlanStatus string

const (
    PlanDraft     PlanStatus = "draft"      // planner is building
    PlanInReview  PlanStatus = "in_review"  // submitted; user annotating
    PlanApproved  PlanStatus = "approved"   // user confirmed; not yet started
    PlanExecuting PlanStatus = "executing"  // orchestrator dispatching items
    PlanDone      PlanStatus = "done"       // all items done/skipped
    PlanBlocked   PlanStatus = "blocked"    // unrecoverable item failure
    PlanFailed    PlanStatus = "failed"     // run error / abort
)

type ItemStatus string

const (
    ItemPending    ItemStatus = "pending"
    ItemInProgress ItemStatus = "in_progress"
    ItemDone       ItemStatus = "done"
    ItemBlocked    ItemStatus = "blocked"
    ItemSkipped    ItemStatus = "skipped"
)

type PlanItem struct {
    ID          string    `json:"id"`                     // stable slug, e.g. "item-1"
    Title       string    `json:"title"`
    Description string    `json:"description"`            // self-contained brief for a small-context worker
    DependsOn   []string  `json:"depends_on,omitempty"`   // item IDs
    Role        string    `json:"role,omitempty"`         // suggested worker role; empty = config default
    Status      ItemStatus `json:"status"`
    Result      string    `json:"result,omitempty"`       // worker summary on completion
}

type PlanComment struct {
    ID        string    `json:"id"`
    ItemID    string    `json:"item_id"`   // empty = plan-level comment
    Content   string    `json:"content"`
    Revision  int       `json:"revision"`  // revision the comment was made on
    Resolved  bool      `json:"resolved"`
    CreatedAt time.Time `json:"created_at"`
    UpdatedAt time.Time `json:"updated_at"`
}

type Plan struct {
    ID        string       `json:"id"`      // internal ID: plan-<hex> (directory name)
    Name      string       `json:"name"`    // friendly alias, e.g. "happy.hare"
    Objective string       `json:"objective"`
    Status    PlanStatus   `json:"status"`
    Revision  int          `json:"revision"`
    Items     []PlanItem   `json:"items"`
    Comments  []PlanComment `json:"comments,omitempty"`
    RunID     string       `json:"run_id,omitempty"` // bound orchestrator run (execution)
    CreatedAt time.Time    `json:"created_at"`
    UpdatedAt time.Time    `json:"updated_at"`
}
```

### 4.2 Persistence

Event-sourced NDJSON under `.goa/plans/<plan-id>/events.jsonl`, mirroring
`core/orchestrator/store.go` and `core/goal/store.go`. A `plan.json` snapshot
is written on every mutation for fast load; the event log remains the source
of truth for replay.

All store mutations serialize through a single in-process mutex around
load–mutate–append–snapshot, and every writer (planner/orchestrator tool
calls, pager comment saves, run-event callbacks) shares one store handle per
plan. This is what makes the replan window safe: while a replan pauses at the
item boundary, the finishing worker's `item_completed` write and the reopened
pager's `comment_added` write cannot interleave (§16).

Event types:

```
plan_created        {objective, name}
item_added          {item}
item_updated        {item_id, fields}
item_removed        {item_id}
items_reordered     {ids}
revision_submitted  {revision}
comment_added       {comment}
comment_updated     {comment_id, content}
comment_removed     {comment_id}
comment_resolved    {comment_id, note}
plan_approved       {}
execution_started   {run_id}
item_started        {item_id, role, agent_id}
item_completed      {item_id, result}
item_blocked        {item_id, reason}
item_skipped        {item_id, reason}
clarification       {item_id, question, answer}   // both directions recorded
plan_completed      {}
plan_failed         {reason}
```

`Resume(planID)` replays the log to rebuild state. The plan store — not the
run log — is the recovery source of truth for execution progress (see §9.4).

### 4.3 Rendered Markdown

`core/plan/render.go` deterministically renders a `Plan` to Markdown for the
pager and for prompt injection:

```markdown
# Plan: <Name> (revision N)
**Objective:** ...
**Status:** in_review

## 1. <Item title>            <!-- anchor: item-1 -->
<description>
_Depends on: — | Role: coder_

## 2. ...
```

The renderer also returns a line→anchor map (`[]LineAnchor{Line, ItemID}`) so
the pager can attach comments to item IDs instead of line numbers. Anchors are
stable across revisions; line numbers are not (unlike the diff pager, where
the diff is frozen per base ref).

### 4.4 Annotation Summary

`core/plan/annotations.go` provides `AnnotationsSummary(plan)` — the
plan-mode analog of `review.Session.MarkdownSummary`: objective, revision,
open comments grouped by item (with a short excerpt of each commented item),
and resolved comments from the current round. Submitted to the planner as a
user-role message (prompt-cache-safe, B3a pattern) via `ctx.SubmitToAgent`.

## 5. The `plan` Tool

New tool `plan` in `tools/plan/plan.go` (same package as the legacy
`plan_mode` tool), implementing `agentic.Tool` + `ResultTool`
(`ExecuteWithResult` for `StopTurn` on `submit_review`). Errors use
`internal.ToolError`. Docs via embedded `plan.short.md` / `plan.long.md`;
TUI renderer in `tools/plan/plan_renderer.go` registered through
`tui/register_renderers.go`.

Single tool, `action` enum (matches `plan_mode` and keeps the tool surface
small for the models that carry it):

| Action | Input | Phase | Effect |
|--------|-------|-------|--------|
| `add_item` | `{title, description, depends_on?, role?, after?}` | planning | Append/insert item; returns generated ID. |
| `update_item` | `{id, title?, description?, depends_on?, role?}` | planning | Patch fields. |
| `remove_item` | `{id}` | planning | Remove; must not be depended on by others. |
| `reorder` | `{ids}` | planning | Set explicit order; must list all IDs. |
| `get` | `{}` | any | Return rendered Markdown + statuses. |
| `submit_review` | `{}` | planning | revision++, status→`in_review`, `StopTurn`, host opens the pager. |
| `resolve_comment` | `{id, note?}` | planning | Mark comment resolved. |
| `start_item` | `{id}` | execution | Validate + mark `in_progress`; returns the worker task brief. |
| `complete_item` | `{id, result}` | execution | Mark `done`, store result. |
| `block_item` | `{id, reason}` | execution | Mark `blocked`. |
| `skip_item` | `{id, reason?}` | execution | Mark `skipped`; result lists the item's dependents (their dependency is now satisfied — `start_item` rule 4 treats `skipped` like `done`). No auto-cascade: dependents are never skipped implicitly. |

Phase enforcement is by plan status: structural actions during execution (or
execution actions during planning) return a `ToolError` with a hint
(e.g. "plan is executing; use /plan:replan to re-enter planning").

`start_item` validation (the sequential-dispatch invariant):

1. Plan status is `executing`.
2. Item exists and is `pending` (on `blocked`, hint at `replan`).
3. No other item is `in_progress` — one in flight, strictly.
4. Every ID in `DependsOn` is `done` or `skipped`.

`skip_item` validation: plan status is `executing`; the item exists and is
`pending` or `blocked` (never `in_progress` or `done`). Because a `skipped`
dependency satisfies rule 4, skipping a blocked item is the execution-phase
escape hatch that keeps the "all items `done`/`skipped`" terminal condition
reachable without re-entering planning (§9.2).

On success the returned brief is self-contained for a fresh small-context
worker: item title, full description, ordered list of dependency results
(`<id>: <result summary>`), and the instruction that the worker must finish
via `task_outcome`.

### 5.1 The `task_outcome` Worker Tool

New tool `task_outcome` in `tools/plan/task_outcome.go`, injected **only**
into plan-run worker agents (never into the planner/orchestrator, never into
non-plan runs). Implements `ResultTool` with `StopTurn: true`.

```json
{"status": "done|needs_clarification|blocked", "summary": "...", "question": "..."}
```

- `done` — work complete; `summary` is the item result.
- `needs_clarification` — `question` required; the worker stops and the
  orchestrator receives the question in the delegate result.
- `blocked` — `summary` carries the reason.

The delegate result delivered to the orchestrator is typed
(`status + summary + question?`), so the orchestrator can route without
parsing prose: `done` → review then `complete_item` (or `rework`);
`needs_clarification` → answer from its own context or escalate with
`ask_user`, then `rework` the same worker with the answer; `blocked` →
`block_item`, then skip dependents, replan, or abort.

## 6. Configuration Schema

### 6.1 Role Context Limits

Extend `config.OrchestratorRole`:

```go
type OrchestratorRole struct {
    Model         string   `yaml:"model"`
    Provider      string   `yaml:"provider,omitempty"`
    AllowedTools  []string `yaml:"allowed_tools,omitempty"`
    ContextWindow int      `yaml:"context_window,omitempty"` // tokens; 0 = model default
    MaxTokens     int      `yaml:"max_tokens,omitempty"`     // compression threshold override; 0 = default
}
```

Wiring (`internal/app/orchestrator_adapter.go`, agent factory): after agent
creation, if `ContextWindow > 0` call `SetContextWindow(ContextWindow)`; if
`MaxTokens > 0` override the agent's `ContextCompression.MaxTokens`.
`effectiveMaxTokens` already takes `min(model window, compression max)`, so a
worker with `context_window: 16384` compresses aggressively while the planner
keeps the full model window.

Validation (`config/config_validate.go`): both fields `>= 0`; warn (flash,
not error) when `context_window` exceeds the known model window — harmless
but likely a typo. Merge support in `config/config_merge.go`; completion
entries in `core/commands/config_completion.go`
(`orchestrator.roles.*.context_window`, `.max_tokens`).

### 6.2 Plan Retention

```go
type PlanConfig struct {
    Retention PlanRetentionConfig `yaml:"retention,omitempty"`
}
type PlanRetentionConfig struct {
    Enabled bool `yaml:"enabled"` // default true
    Days    int  `yaml:"days"`    // default 7; 0 = keep forever
}
```

Top-level `plan:` config section; identical semantics and housekeeping cadence
to `orchestrator.retention` (startup + hourly + opportunistic; only
terminal-status plans older than N days are deleted).

### 6.3 Planner Role

The planner is the `orchestrator` role from the existing orchestrator config
(no new role concept). If no orchestrator roles are configured — the default
config ships `orchestrator.roles: {}` — `/plan:new` reuses the
`effectiveOrchestratorConfig` path from `core/commands/orchestrate.go`:
default `orchestrator`/`coder`/`reviewer` roles are synthesized from the
active model and the same "using default roles" flash is shown.

Planner tool surface: `plan` + the role's `allowed_tools`, defaulting to the
exploration set (`readfile`, `search`, `smartsearch`, plus `bash` under the
standard jail) when the role has no allowlist — the planner must be able to
explore the repo to write grounded plans. Workers get
`task_outcome` + their role's `allowed_tools`; never `plan`.

## 7. Command Surface

New `core/commands/plan.go`, self-registered via `init()`, colon syntax,
interactive when arguments are missing (same conventions as orchestrate-v2).

| Command | Behavior |
|---------|----------|
| `/plan` | Action list: new / review / approve / status / replan / list / delete. |
| `/plan:new:objective=...` | Create plan + start planner session (§8). Prompts for objective when missing. |
| `/plan:review:id=` | Open the plan pager at the current revision. |
| `/plan:approve:id=` | Approve and start execution (§9). Confirms when unresolved comments exist. |
| `/plan:status:id=` | Open the plan-status overlay (§10). |
| `/plan:replan:id=` | Pause execution at the next item boundary; status→`in_review`; planner resumes with current state to revise the remaining items. |
| `/plan:list` | Filterable plan list (name, status, revision, items done/total, updated). |
| `/plan:delete:id=,confirm=true` | Stop the bound run if active, then delete. `id=*` bulk with confirmation. |
| `/plan?`, `/plan??` | Short / long help from embedded help files. |

`id` resolution mirrors orchestrate-v2 §5.4: friendly name first, then
internal ID, else `plan %q not found`. Names are generated with
`internal.FriendlyNameUnique`; IDs with `internal.PrefixedHexID("plan", 4)`.

Escape cancels the current step; Ctrl-C cancels the whole flow — same as
`/orchestrate`.

## 8. Planning Phase

1. `/plan:new` creates the plan (`plan_created` event), status `draft`.
2. Build the planner agent: orchestrator role (full context window), system
   prompt from embedded `prompts/plan/planner.md` (with user prompt-override
   directory support, matching the orchestrator adapter), tools = `plan` +
   read-only exploration set. The prompt fixes the contract: explore first,
   keep items small enough for a worker with no prior context, write
   self-contained descriptions, declare dependencies, end with
   `submit_review`.
3. The planner runs in the main chat — the user sees its exploration and may
   converse freely (steering is the normal chat input; no special channel).
4. `submit_review` → `revision_submitted` event, status `in_review`,
   `StopTurn`, and the host posts `event.ChatEvent{ShowPlanPager: ...}`.
5. User reviews in the pager (§8.1). On submit, `AnnotationsSummary` goes to
   the planner via `ctx.SubmitToAgent`; the planner revises via the tool
   (including `resolve_comment`) and calls `submit_review` again. The pager
   reopens on the new revision; unresolved comments persist.
6. Repeat until the user approves (`a` in the pager or `/plan:approve`).

There is no hard cap on review rounds; the user may approve with open
comments (pager confirms).

### 8.1 Plan Pager (`tui/plan_pager.go`)

The review-pager interaction model applied to plans. To avoid a second
hand-rolled pager, first extract the generic core out of
`tui/review_pager.go` into `tui/annotate/` — a document-agnostic pager:
rendered lines + line anchors + comment CRUD + main-input callbacks
(`OnCommentRequest`, `OnConfirm`, `OnSubmit`, `OnClose`, `RequestRender`,
`SetViewport`). `ReviewPager` becomes a thin adapter over it (diff parsing,
base-ref selection), `PlanPager` another (rendered plan, item anchors). The
extraction is behavior-preserving; the existing `review_pager_test.go` suite
is the equivalence proof. For that proof to hold, the extraction must keep
`ReviewPager`'s public surface (struct fields and callback names) intact —
the tests construct and drive it directly and stay unchanged.

PlanPager keys:

| Key | Action |
|-----|--------|
| `↑/↓`, `PgUp/PgDn` | Navigate lines. |
| `n` / `p` | Jump to next / previous item heading. |
| `c` | Add comment on the current line's item (main input; plan-level when on a non-item line). |
| `e` / `d` | Edit / delete comment (`d` confirms via main input). |
| `s` | Submit annotations to the planner (confirm). |
| `a` | Approve the plan (confirm; warns on open comments). |
| `q` / Esc | Close without submitting (comments are kept). |

Comments save immediately (event-sourced) so closing the pager never loses
work. The footer line shows revision, open-comment count, and the key hints.

## 9. Execution Phase

1. Approval (`plan_approved` event) starts an orchestrator **hub** run via the
   existing `core/orchestrator` runtime. The run's `run_started` payload
   records `plan_id`, following the existing conditional `goal_id` pattern in
   `runtime.go` (a `Runtime.SetPlanID(planID)` setter before start; payload
   field omitted when unset); the plan records `RunID`. Run objective:
   `"Execute plan <name> (<plan-id>): <objective>"`.
2. The execution orchestrator is a **fresh agent** built from the
   `orchestrator` role (standard hub construction — the planning-phase agent
   lives in the main chat and is not carried over). The *why* context comes
   from the plan itself: its first turn starts with `plan get`, which returns
   items with their descriptions, dependency results, and the comment
   history (including resolved comments and their resolution notes) — the
   same state-injection mechanism as resume (§9.4), so approval, resume, and
   crash-recovery all converge on one orchestrator bootstrap path. Tools:
   `plan` (execution actions only), `delegate`, `rework`, `ask_user`.
3. Loop (driven by the orchestrator model, constrained by the tool):
   `plan get` → `start_item` (enforces: one in flight, deps satisfied) →
   `delegate(role, brief, new_agent: true)` → worker runs → typed outcome →
   review → `complete_item` / `rework` / `block_item` → next item.
4. Workers are always **fresh agents** (`new_agent: true` on `delegate`, an
   existing flag) with the role's `context_window` applied — no cross-item
   context bleed; the item brief is all they see.
5. Clarification: worker `task_outcome{needs_clarification, question}` →
   orchestrator answers from context or calls `ask_user` (existing runtime
   pause + main-input prompt) → user answer steers the orchestrator →
   `rework` targets the same worker with the answer. Every exchange is
   recorded as a `clarification` plan event and is visible in the
   conversation tab.
6. Terminal: all items `done`/`skipped` → `plan_completed`, run finishes.
   An item the orchestrator cannot unblock → `PlanBlocked` and the run
   pauses for the user (steer / replan / delete).

### 9.1 Review-the-output contract

The orchestrator must review each worker result against the item description
before `complete_item` (the TODO's "run review of the output/check it
matches"). This is a prompt-level obligation in the execution section of the
planner prompt, plus a tool-level nudge: `complete_item` rejects an empty
`result`. The tool cannot distinguish the orchestrator's own summary from a
pasted worker result — that part is prompt-level only; the tool-enforced
contract is non-emptiness.

### 9.2 Failure paths

- Worker turn errors (provider failure, context cancel): the delegate result
  reports the error; the orchestrator may retry (re-delegate, fresh worker)
  or `block_item`. Pool caps release on all exit paths (existing guarantee).
- User interrupt (Esc/Ctrl-C on the run): normal orchestrator stop; plan
  stays `executing` and is resumable.
- `block_item` with pending dependents: they stay `pending`; the orchestrator
  is told (in the tool result) which items are now unstartable, and must
  either replan (hand back via `/plan:replan`) or finish without them:
  `skip_item` the blocked item — and, where appropriate, its dependents —
  which satisfies their dependencies and keeps the plan finishable (§5).

### 9.3 Delegation visibility

All agent streaming renders in the existing orchestrator conversation tab
(the user watches every worker live). Plan events additionally update the
status overlay (§10) and a footer progress line:
`plan happy.hare · item 3/8 in_progress: auth-schema`.

### 9.4 Resume

`goa --plan <id>` (headless) or `/plan:approve`/re-approve on an `executing`
plan resumes execution. The plan store is the source of truth: on resume the
orchestrator's first injected user message is the current plan state from
`plan get`, and hub re-driving simply continues from the first non-done item.
This fixes the hub topology's known "prior delegation results are not reused"
weakness for plan runs: item results live in the plan, not the run.

## 10. Plan-Status Overlay

New read-only full-screen component `tui/plan_status.go`, opened with
`/plan:status` (or a footer hint key during execution). It never steals focus
on its own.

Contents:

- Header: name, objective, status, revision, bound run, progress bar
  (`done+skipped / total`).
- Item list, one row per item: status glyph (`☐` pending, `◐` in-progress
  animated, `☑` done, `✖` blocked, `–` skipped), title, role, open-comment
  count.
- Detail pane for the selected item: full description, dependency states,
  result, clarification log.
- Highlighted section for open clarifications (questions awaiting the user).

Updates: plan events are posted onto the TUI `commandLoop` like orchestrator
events (single-owner invariant, R1); the overlay re-renders from the store
snapshot. Keys: `↑/↓` select, `Enter` toggle detail, `q`/Esc close.

## 11. Legacy `plan_mode` Deprecation

- `tools/plan/plan_mode.go` and `core/plan/mode.go` remain functional.
- Tool long-doc and `/help` gain a deprecation note pointing at `/plan`.
- No migration: PLAN.md files are just files; users finish in-flight work or
  start a `/plan` instead.
- Removal is a separate future decision, out of scope here.

## 12. Implementation Phases

Each phase keeps the build green and passes the static-analysis gate.

### Phase 1 — Plan model, store, renderer
`core/plan/model.go`, `store.go` (NDJSON + snapshot + replay), `render.go`
(Markdown + line anchors), `annotations.go` (summary builder). Unit tests:
event round-trip, replay idempotence, anchor stability across re-renders,
dependency validation helpers, summary contents.

### Phase 2 — Config
Role `context_window`/`max_tokens` (parse/merge/validate/defaults/
completion) and `plan.retention`. Unit tests per config conventions.

### Phase 3 — Tools
`plan` tool (full action/phase matrix, sequential-dispatch enforcement) and
`task_outcome` tool; embedded docs; TUI renderer; registration. Unit tests
for every action × phase combination, dependency errors, reorder validation,
`skip_item` validation and dependent-satisfaction semantics, `StopTurn`
semantics.

### Phase 4 — Pager extraction + PlanPager
Extract `tui/annotate/` from `review_pager.go` (behavior-preserving; existing
tests prove equivalence), then `PlanPager` with item anchors, comment CRUD,
submit/approve flows. Filmstrip tests via the `tui-test` skill.

### Phase 5 — Command + planning phase
`/plan` command surface, plan lifecycle, planner agent builder (prompt from
`prompts/plan/planner.md`), `ShowPlanPager` event wiring, annotation
round-trip back into the planner. Unit + TUI tests: new → planner drafts →
submit_review → pager → annotate → planner revises → re-review.

### Phase 6 — Execution binding
Plan↔run binding, worker factory (fresh agent, context limits, `task_outcome`
injection), typed outcome routing, clarification escalation via `ask_user`,
`replan`, resume-from-store. Tests with the scripted provider
(`multiagent/test_provider_test.go` pattern): full lifecycle including a
clarification round, a blocked item, and a crash-resume that skips done
items.

### Phase 7 — Status overlay + footer
`tui/plan_status.go`, footer progress line, event plumbing through
`commandLoop`. Filmstrip tests: item transitions, clarification highlight,
overlay open/close during a live run.

### Phase 8 — Housekeeping + docs
Retention sweep, `goa --plan <id>` headless resume, deprecation notes,
`core/commands/help/plan.*.md`, `docs/` + README updates, final gate run.

## 13. Testing Strategy

- **Unit** (table-driven, `t.TempDir()`, `-race`): model/store/render/
  annotations; config; tool action matrix incl. phase enforcement and the
  one-in-flight invariant; pager anchor mapping; outcome routing; retention
  math with fake clock.
- **TUI** (`tui-test` skill filmstrips): pager navigation/comment/submit/
  approve; status overlay transitions; footer progress.
- **Integration**: scripted-provider end-to-end — plan → two review rounds →
  approve → three sequential workers (one needing clarification, one blocking)
  → done; kill mid-item → resume → done items not re-run.
- **Gate**: `go vet ./...`, `go test -count=1 -race -cover ./...`,
  `gocognit -over 15`, `gocyclo -over 12`. Complexity budgets per AGENTS.md
  (config 20/12, TUI 18/12, other 15/12).

## 14. Files to Modify / Create

- `core/plan/` — new: `model.go`, `store.go`, `render.go`, `annotations.go`
  (+ tests). Existing `mode.go` untouched.
- `core/orchestrator/` — `Runtime.SetPlanID` and the conditional `plan_id`
  field in the `run_started` payload, mirroring `goal_id` (+ payload/replay
  test). No other runtime changes.
- `tools/plan/` — new: `plan.go`, `task_outcome.go`, `plan_renderer.go`,
  `plan.short.md`, `plan.long.md` (+ tests). `plan_mode.go` gains a
  deprecation note in its long doc.
- `core/commands/plan.go` (+ `plan_test.go`), `core/commands/help/plan.short.md`,
  `plan.long.md`; `core/commands/config_completion.go` additions.
- `config/config.go`, `defaults.go`, `config_validate.go`, `config_merge.go` —
  role fields + plan section (+ tests).
- `tui/annotate/` — extracted pager core; `tui/review_pager.go` refactored
  onto it; new `tui/plan_pager.go`, `tui/plan_status.go` (+ tests).
- `internal/event/event.go` — `ShowPlanPager`, `ShowPlanStatus` payloads.
- `internal/app/` — planner builder, plan↔run binding, worker factory with
  context limits + `task_outcome`, retention scheduling, `--plan` flag in
  bootstrap/headless.
- `prompts/plan/planner.md` — embedded planner system prompt (planning +
  execution sections).
- `README.md`, `docs/COMMANDS.md`, `docs/TOOLS.md`, `docs/ORCHESTRATOR.md`
  (role fields), new `docs/PLAN.md`.

## 15. Complexity and Design Constraints

- Small composable primitives: store ops, renderer, anchor mapper, annotation
  summary, tool actions as individual handlers behind a dispatcher (no fat
  switch over the budget).
- Depend on abstractions: the plan tool takes a `Store` interface; the pager
  takes anchors + callbacks; the execution binder takes the orchestrator
  `Builder`.
- All prompt/help text from embedded files; tool errors in
  `internal.ToolError` format.
- Events onto the TUI only via `commandLoop` posts — no cross-goroutine
  component mutation (R1).
- Planner/orchestrator system prompts byte-stable across turns; dynamic plan
  state (annotations, resume snapshots, progress) delivered as appended
  user-role messages (B3a pattern) to keep prompt cache warm.
- Pager does no I/O: persistence and agent submission via host callbacks,
  exactly like `ReviewPager` (SRP; historical bug source).

## 16. Risks and Open Items

- **Pager extraction regression**: mitigated by running the existing
  `review_pager_test.go` suite unchanged against the extracted core before
  building PlanPager.
- **Orchestrator sequencing discipline**: the model could try to parallelize;
  `start_item` hard-rejects a second in-flight item, turning misuse into a
  self-correcting tool error rather than a race.
- **Context-limit semantics**: `context_window` smaller than a single item
  brief + tool schemas will thrash compression. Mitigation: validate a sane
  floor (warn < 4096) and document brief-sizing guidance in the planner
  prompt. Worker-output size is unbounded prose today; the typed outcome's
  `summary` should be length-capped (tool truncates with a notice).
- **Replan mid-flight**: pausing "at the next item boundary" means the
  in-flight worker finishes first; the UI must say so. Abort-now is just
  deleting the run (existing) — documented, not special-cased.
- **Review-round loops**: no cap; if a user never approves, the plan simply
  stays `in_review`. Acceptable; retention cleans up stale drafts only in
  terminal states, so drafts are never auto-deleted (explicit decision:
  retention applies to `done`/`blocked`/`failed` only).
- **Plan tool on the main agent**: out of scope for v1 — the `plan` tool is
  only given to planner/orchestrator agents inside plan mode. Exposing it to
  the main agent (TodoWrite-style) is a natural follow-up and the model
  supports it unchanged.

---

*This spec is ready for implementation. Each phase should be committed
independently and must pass the static-analysis gate before moving to the
next phase.*
