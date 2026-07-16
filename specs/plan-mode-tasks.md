<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Plan Mode — Micro-Task Execution Plan

Executable decomposition of `specs/plan-mode.md` (the spec) into small,
self-contained tasks. Each task is designed for a worker with **no prior
context**: it names exact files, symbols to mirror, behavior, tests, and the
verification command. Read only your task; the spec is the tie-breaker.

## Rules for every task

1. Work only on the files listed in your task. If a needed symbol is missing,
   stop and report — do not improvise cross-task work.
2. Every new/changed behavior ships with its test in the same commit. Test
   conventions: table-driven, `t.TempDir()` for filesystem, `-race` clean,
   <100ms per unit test.
3. Verification command per task: run it, read the output, paste the result.
   Green = `go build ./...` plus the listed `go test` invocation.
4. Errors use `internal.ToolError` (see `internal/errors.go`). All user-facing
   prompt/help text comes from embedded files (`//go:embed`), never string
   literals in Go.
5. SPDX header on every new file:
   ```go
   // SPDX-License-Identifier: GPL-3.0-or-later
   //
   // Copyright (C) 2026 Pierre Poissinger
   ```
6. Do not refactor existing code unless the task says so.

## Reference patterns (already in the repo — read these first)

| Pattern | File |
|---|---|
| Tool contract | `internal/agentic/tool.go` (`Tool`, `ResultTool`, `ToolResult.StopTurn`) |
| Simple action-enum tool | `tools/plan/plan_mode.go` (schema, `ToolError` usage, `//go:embed` docs) |
| NDJSON event store | `core/orchestrator/store.go` (`Event`, `EventStore`, `FileEventStore`) |
| Conditional run payload field | `core/orchestrator/runtime.go:959-961` (`SetGoalID`) + `:256-260` (payload) |
| Colon command + key=value parsing | `core/commands/orchestrate_input.go` |
| Command registration | `core/commands/register.go` (`RegisterAll`); conditional registration in `internal/app/subsystems.go:973-986` |
| Tool renderer registration | `tui/register_renderers.go` |
| Review pager (extraction source) | `tui/review_pager.go` (482 lines), tests `tui/review_pager_test.go` + `tui/review_comment_input_test.go` |
| Annotation summary analog | `internal/review/session.go:119` (`MarkdownSummary`) |
| Default-role synthesis | `core/commands/orchestrate.go:533-547` (`effectiveOrchestratorConfig`) |
| ChatEvent extension point | `internal/event/event.go:65-87` (`ShowReviewPager` pattern) |
| Agent context limits | `internal/agentic/agent.go:609` (`SetContextWindow`), `internal/agentic/agent_context.go:17` (`effectiveMaxTokens`) |
| Config role struct | `config/config.go:260-264` (`OrchestratorRole`) |
| Scripted provider for integration tests | `multiagent/test_provider_test.go` |

---

## Phase 1 — Plan model, store, renderer (`core/plan/`)

Target package: `core/plan` (exists; contains only `mode.go` + `mode_test.go`
— legacy, do not touch). New files only.

### T1.1 — Plan types (`core/plan/model.go`)
- Define exactly the types from spec §4.1: `PlanStatus` (7 constants),
  `ItemStatus` (5 constants), `PlanItem`, `PlanComment`, `Plan` — same field
  names and JSON tags as the spec.
- Add helper methods on `*Plan`: `Item(id string) *PlanItem`,
  `Dependents(id string) []string` (IDs of items whose `DependsOn` contains
  `id`), `AllTerminal() bool` (every item `done` or `skipped`).
- **Test** (`model_test.go`): table-driven — `Item` hit/miss, `Dependents`
  with chains and diamonds, `AllTerminal` for each status mix.
- **Verify:** `go build ./... && go test ./core/plan/ -run 'TestPlan' -count=1 -race`
- **Depends:** none.

### T1.2 — Event types (`core/plan/events.go`)
- Mirror `core/orchestrator/store.go:19-47`: `EventType` string constants for
  the 18 event types in spec §4.2 (note: `item_blocked`, `item_skipped` — no
  `_evt` suffix), and `Event{Seq, Type, PlanID, Timestamp, Payload}` with the
  same JSON tag style.
- **Test:** JSON round-trip of one event per type (marshal→unmarshal→equal).
- **Verify:** `go test ./core/plan/ -run 'TestEvent' -count=1 -race`
- **Depends:** none (parallel with T1.1).

### T1.3 — Store: append + snapshot (`core/plan/store.go`)
- `Store` struct: root dir `.goa/plans`, `mu sync.Mutex`, in-memory `*Plan`.
- Constructor `Create(root, objective string) (*Store, error)`: generates ID
  `internal.PrefixedHexID("plan", 4)` and name
  `internal.FriendlyNameUnique(<names of sibling plan dirs>)`, makes
  `<root>/<id>/`, appends `plan_created`, writes `plan.json` snapshot.
- `append(evt)` (private): assign `Seq`, JSON-marshal one line to
  `events.jsonl` (O_APPEND), rewrite `plan.json` — all under `mu`.
- Every exported mutation in T1.5 follows: `mu.Lock` → mutate in-memory →
  `append` → `mu.Unlock`. Single-writer invariant (spec §4.2).
- **Test:** create → events.jsonl has 1 line; plan.json matches in-memory;
  name is unique among 3 created plans; concurrent `append` from 8
  goroutines produces no interleaved lines (`-race`).
- **Verify:** `go test ./core/plan/ -run 'TestStore' -count=1 -race`
- **Depends:** T1.1, T1.2.

### T1.4 — Store: load + replay (`core/plan/store.go` continued)
- `Open(root, id string) (*Store, error)`: replay `events.jsonl` to rebuild
  state (ignore `plan.json`; it is a cache). `applyEvent` is a switch over
  event type mutating `*Plan`; unknown types are skipped (forward compat).
- `Resolve(root, ref string) (id string, err error)`: friendly name first,
  then internal ID, else `plan %q not found` (spec §7).
- **Test:** round-trip — perform ≥6 mutations, `Open`, deep-equal state;
  replay idempotence (Open twice, equal); corrupt last line → error
  identifies line number; `Resolve` by name, by ID, not-found.
- **Verify:** `go test ./core/plan/ -run 'TestOpen|TestResolve' -count=1 -race`
- **Depends:** T1.5 (replay must recognize every mutation's event type;
  skeleton may be scaffolded earlier, but do not finalize before T1.5).

### T1.5 — Store mutations (`core/plan/mutations.go`)
One method per event type, each validating then appending (spec §5 semantics):
- Planning: `AddItem(item, after string) (id string, err error)` (ID =
  `item-N`, next free N), `UpdateItem(id, patch)`, `RemoveItem(id)` (reject
  if `Dependents(id)` non-empty), `Reorder(ids)` (must be a permutation),
  `SubmitRevision()` (`Revision++`, status→`in_review`),
  `ResolveComment(id, note)`, `AddComment`, `UpdateComment`, `RemoveComment`,
  `Approve()` (status `in_review`→`approved`).
- Execution: `StartExecution(runID)`, `StartItem(id, role, agentID)` —
  enforce spec §5 rules 1-4 (executing; pending; none `in_progress`; deps all
  `done`/`skipped`), `CompleteItem(id, result)`, `BlockItem(id, reason)`,
  `SkipItem(id, reason)` (item must be `pending` or `blocked`),
  `RecordClarification(itemID, question, answer)`, `Finish()` (→`done`,
  requires `AllTerminal()`), `Fail(reason)`, `BlockPlan(reason)`.
- **Test** (table-driven, one function per method): happy path + every
  rejection branch; `StartItem` sequential invariant (second in-flight →
  error); `SkipItem` on blocked item satisfies a dependent's later
  `StartItem`.
- **Verify:** `go test ./core/plan/ -run 'TestMut' -count=1 -race`
- **Depends:** T1.3.

### T1.6 — Renderer (`core/plan/render.go`)
- `Render(p *Plan) (markdown string, anchors []LineAnchor)`; `LineAnchor
  {Line int; ItemID string}` — exact output shape per spec §4.3 (`# Plan:
  <Name> (revision N)`, `## N. <title>` per item in order, `<!-- anchor:
  item-N -->` trailing comment on heading lines, `_Depends on: … | Role: …_`
  line, status line).
- Deterministic: same plan → same bytes (map-free iteration; items in slice
  order).
- **Test:** golden-file or literal-string comparison for a 3-item plan with
  deps; every item heading appears in `anchors` with its ID; anchor stability
  — render, add a comment, render again → same anchors for unchanged items.
- **Verify:** `go test ./core/plan/ -run 'TestRender' -count=1 -race`
- **Depends:** T1.1.

### T1.7 — Annotation summary (`core/plan/annotations.go`)
- `AnnotationsSummary(p *Plan) string` per spec §4.4: objective, revision,
  open comments grouped by item ID (with ≤5-word excerpt of the item title),
  resolved comments from the current revision. Mirror tone/format of
  `internal/review/session.go:119 MarkdownSummary`.
- **Test:** plan with plan-level + item comments (open and resolved, mixed
  revisions) → summary contains groups, excludes old-revision resolved,
  deterministic ordering (by item order, then CreatedAt).
- **Verify:** `go test ./core/plan/ -run 'TestAnnotations' -count=1 -race`
- **Depends:** T1.1.

**Phase 1 gate:** `go vet ./core/plan/ && go test ./core/plan/ -count=1 -race -cover` (target ≥80%) and `gocognit -over 15 ./core/plan/`.

---

## Phase 2 — Config (`config/`)

### T2.1 — Role context fields
- Add to `config.OrchestratorRole` (config.go:260): `ContextWindow int
  \`yaml:"context_window,omitempty"\`` and `MaxTokens int
  \`yaml:"max_tokens,omitempty"\`` — doc comments from spec §6.1.
- Merge: in `config/config_merge.go`, find where `AllowedTools` merges for
  roles; add the two ints (non-zero overrides).
- **Test:** YAML parse with both fields; merge override + zero-keeps-base.
- **Verify:** `go test ./config/ -run 'TestOrchestratorRole|TestMerge' -count=1`
- **Depends:** none.

### T2.2 — Role validation
- In `config/config_validate.go`, in the existing role-validation loop:
  error if either field < 0; collect a **warning** (use the existing warning
  mechanism in that file; if none exists, add a `Warnings []string` out-param
  alongside errors) when `context_window` is 1–4095 (thrash floor, spec §16).
  Model-window-exceeds check: only if the file already resolves model windows
  — otherwise skip with a code comment referencing spec §6.1.
- **Test:** negative → error; 2048 → warning; 0 → neither; 16384 → neither.
- **Verify:** `go test ./config/ -run 'TestValidate' -count=1`
- **Depends:** T2.1.

### T2.3 — `plan.retention` section
- `PlanConfig{Retention PlanRetentionConfig}` + `Plan PlanConfig
  \`yaml:"plan,omitempty"\`` on the root `Config`, mirroring `GoalsConfig`
  (config.go:280-289). Defaults in `config/defaults.go`: `enabled: true,
  days: 7`; semantics `Enabled=false or Days=0 → keep forever` (doc comment).
  Add `plan:` block to `config/configs/default.yaml` right after `goals:`.
- Validation: `Days >= 0`. Merge: mirror goals retention merge.
- **Test:** default load → enabled/7; `days: 0` parses and means forever;
  merge override; negative days → error.
- **Verify:** `go test ./config/ -count=1` (whole package — defaults tests are load-bearing)
- **Depends:** none.

### T2.4 — Completion entries
- In `core/commands/config_completion.go`, extend the orchestrator-roles key
  completion with `context_window` and `max_tokens` (follow the existing
  per-key pattern in that file).
- **Test:** completion for `orchestrator.roles.coder.` suggests both new keys.
- **Verify:** `go test ./core/commands/ -run 'TestConfigCompletion' -count=1`
- **Depends:** T2.1.

**Phase 2 gate:** `go vet ./config/ ./core/commands/ && go test ./config/ ./core/commands/ -count=1 -race`

---

## Phase 3 — Tools (`tools/plan/`)

### T3.1 — `plan` tool skeleton (`tools/plan/plan.go`)
- `PlanTool` struct: `agentic.BaseTool`, `Store *plan.Store` (the core/plan
  store), `Opener func() (*plan.Store, error)` or injected handle — see how
  the host wires it in Phase 5; keep the tool dumb.
- Input: `{action, ...}` — schema enumerates the 11 actions from spec §5.
  Dispatch: `map[string]func(json.RawMessage) (string, error)` built in the
  constructor (spec §15: no fat switch).
- Implement `ResultTool`: `ExecuteWithResult` wraps dispatch; only
  `submit_review` sets `StopTurn: true`.
- Docs: `plan.short.md`, `plan.long.md` via `//go:embed` (copy the embed +
  `ShortDoc`/`LongDoc`/`Examples` pattern verbatim from
  `tools/plan/plan_mode.go:76-90`). Long doc = action table from spec §5.
- **Test:** unknown action → `ToolError{Type: "invalid_action"}`; bad JSON →
  `invalid_input`; schema lists all 11 actions.
- **Verify:** `go test ./tools/plan/ -run 'TestPlanTool' -count=1 -race`
- **Depends:** T1.5.

### T3.2 — Structural actions
- Handlers `add_item`, `update_item`, `remove_item`, `reorder`, `get` per
  spec §5 (input fields, return values). `get` returns `Render()` output
  (T1.6) — markdown only, no anchors.
- Phase guard helper `requirePhase(store, "planning")` (T3.5 wires it;
  implement signature now, allow all).
- **Test:** table-driven per handler incl. `remove_item` with dependents →
  error naming them; `reorder` non-permutation → error; `add_item` with
  `after` inserts at position; `get` output contains headings.
- **Verify:** `go test ./tools/plan/ -run 'TestStructural' -count=1 -race`
- **Depends:** T3.1, T1.6.

### T3.3 — Review actions
- `submit_review`: `SubmitRevision()`; output tells the user the pager is
  opening; `ToolResult.StopTurn = true`.
- `resolve_comment`: `ResolveComment(id, note)`.
- **Test:** `submit_review` → revision incremented, status `in_review`,
  StopTurn true; second submit → revision 2; `resolve_comment` unknown ID →
  error.
- **Verify:** `go test ./tools/plan/ -run 'TestReview' -count=1 -race`
- **Depends:** T3.1.

### T3.4 — Execution actions
- `start_item`: `StartItem`; on success return the worker brief per spec §5:
  title, full description, ordered `<id>: <result>` lines for each
  dependency, and the sentence "Finish by calling task_outcome." (exact text
  lives in the embedded long doc; the brief template may be a Go const — it
  is tool output, not prompt text).
- `complete_item` (reject empty `result`), `block_item` (result lists
  now-unstartable dependents), `skip_item` (result lists dependents whose
  dependency is now satisfied; spec §5).
- **Test:** full sequence — start→complete→dependent start; second
  `start_item` while one in flight → error; `start_item` with unsatisfied
  dep → error; `complete_item` empty result → error; `block_item` then
  `skip_item` unblocks dependent (`start_item` succeeds).
- **Verify:** `go test ./tools/plan/ -run 'TestExecution' -count=1 -race`
- **Depends:** T3.1.

### T3.5 — Phase enforcement
- `requirePhase`: structural/review actions require status
  `draft`/`in_review`; execution actions require `executing`; `get` always
  allowed. Violation → `ToolError` with hint from spec §5 ("plan is
  executing; use /plan:replan to re-enter planning").
- **Test:** action × phase matrix (11 actions × 4 statuses) — one
  table-driven test, each cell asserts allow/deny.
- **Verify:** `go test ./tools/plan/ -run 'TestPhase' -count=1 -race`
- **Depends:** T3.2, T3.3, T3.4.

### T3.6 — `task_outcome` tool (`tools/plan/task_outcome.go`)
- `TaskOutcomeTool`: schema `{status: done|needs_clarification|blocked,
  summary, question}` per spec §5.1; validation: `done`→summary required,
  `needs_clarification`→question required, `blocked`→summary required.
- `ResultTool` with `StopTurn: true` always. Output: canonical JSON string
  `{status, summary, question?}` (this becomes the typed delegate result).
- `summary` length cap: 4000 chars, truncate + `" [truncated]"` (spec §16).
- Docs: `task_outcome.short.md` / `task_outcome.long.md` embedded.
- **Test:** each status happy path; each missing-field rejection; StopTurn;
  5000-char summary → truncated.
- **Verify:** `go test ./tools/plan/ -run 'TestTaskOutcome' -count=1 -race`
- **Depends:** none (standalone; parallel with T3.1-3.5).

### T3.7 — Tool renderer + registration
- `tools/plan/plan_renderer.go`: renderer for `plan` tool calls (action name
  + item title/id; completed state shows result excerpt). Mirror the smallest
  existing renderer (look at `tools/bash_renderer.go` for the interface).
- Register in `tui/register_renderers.go`: `RegisterToolRenderer("plan", …)`
  and `RegisterToolRenderer("task_outcome", …)`.
- **Test:** renderer output for each action (snapshot strings).
- **Verify:** `go test ./tools/plan/ ./tui/ -count=1`
- **Depends:** T3.4.

**Phase 3 gate:** `go vet ./tools/plan/ && go test ./tools/plan/ -count=1 -race -cover` + `gocognit -over 15 ./tools/plan/`

---

## Phase 4 — Pager extraction + PlanPager (`tui/`)

### T4.1 — Extract `tui/annotate/` core
- Create package `tui/annotate`: the document-agnostic pager from spec §8.1 —
  rendered lines, `[]LineAnchor` (own type, `ItemID` → generic `Anchor
  string`), comment CRUD state, scrolling, main-input callbacks
  (`OnCommentRequest`, `OnConfirm`, `OnSubmit`, `OnClose`, `RequestRender`,
  `SetViewport`).
- Move the generic logic out of `tui/review_pager.go`; `ReviewPager` keeps
  its **exact public struct fields and callback names** (hard constraint,
  spec §8.1) and delegates.
- **Test:** run the *unmodified* `tui/review_pager_test.go`,
  `review_comment_input_test.go`, `review_selector_render_test.go`, and the
  ReviewPager case in `tab_wrap_test.go` — all must pass untouched.
- **Verify:** `go test ./tui/ -run 'TestReviewPager' -count=1 -race`
- **Depends:** none. Highest-risk task (spec §16): change nothing else.

### T4.2 — `PlanPager` (`tui/plan_pager.go`)
- Thin adapter over `annotate`: content = `plan.Render()` output, anchors =
  render anchors; key map per spec §8.1 (`n`/`p` item jumps, `c` comment,
  `e`/`d` edit/delete, `s` submit, `a` approve, `q`/Esc close).
- Footer: revision, open-comment count, key hints.
- Host callbacks only — zero I/O in the component (spec §15).
- **Test:** unit — key routing table; comment anchored to correct item ID
  when cursor on item vs non-item line; approve-with-open-comments requires
  confirm. Filmstrip via `tui-test` skill: open → navigate → comment →
  submit.
- **Verify:** `go test ./tui/ -run 'TestPlanPager' -count=1 -race`
- **Depends:** T4.1, T1.6.

**Phase 4 gate:** `go vet ./tui/... && go test ./tui/ -count=1 -race` + `gocognit -over 18 ./tui/annotate/ ./tui/plan_pager.go`

---

## Phase 5 — Command + planning phase (`core/commands/`, `internal/app/`)

### T5.1 — `/plan` command skeleton (`core/commands/plan.go`)
- `PlanCommand` implementing `core.Command`; input parsing mirrors
  `core/commands/orchestrate_input.go` (colon-separated, `key=value` pairs,
  comma-separated fallback). Subcommands: `new`, `review`, `approve`,
  `status`, `replan`, `list`, `delete` (spec §7 table).
- ID resolution via `plan.Resolve` (T1.4); `/plan` bare → action list
  (interactive select, same UX helper `/orchestrate` uses).
- Help: `core/commands/help/plan.short.md`, `plan.long.md`; wire `?`/`??`
  suffix handling the same way an existing documented command does (find the
  pattern via `grep -rn '"??"' core/commands/ internal/app/`; if no literal
  match, search `strings.TrimSuffix(cmd, "?")` and mirror that call site).
- Register in `core/commands/register.go` `RegisterAll` (needs no
  orchestrator deps for `new`/`list`/`delete`; execution wiring injected via
  fields like `OrchestrateCommand`).
- **Test:** parse matrix (each subcommand, missing/extra args); `Resolve`
  errors surface as `plan %q not found`.
- **Verify:** `go test ./core/commands/ -run 'TestPlan' -count=1`
- **Depends:** T1.4.

### T5.2 — Planner prompt (`prompts/plan/planner.md`)
- Embedded system prompt per spec §8 step 2: explore-first contract, small
  self-contained items, dependency declaration, end with `submit_review`;
  plus the execution section (spec §9 loop, review-the-output obligation §9.1,
  brief-sizing guidance §16).
- Register in the prompt registry (pattern: `prompts/orchestrate/` +
  `prompts/registry.go`); support user override dir like the orchestrator
  prompts.
- **Test:** registry loads `plan/planner`; override dir wins (existing
  registry tests show how).
- **Verify:** `go test ./prompts/ -count=1`
- **Depends:** none.

### T5.3 — Planner agent builder + `/plan:new` flow (`internal/app/plan_builder.go`)
- `/plan:new:objective=…`: `plan.Store.Create` → build planner agent —
  `orchestrator` role via `effectiveOrchestratorConfig` (reuse the helper in
  `core/commands/orchestrate.go:533`; export it if needed) — tools = `plan`
  tool (bound to the new store) + role allowlist / exploration default; run
  in the main chat session.
- On `submit_review` StopTurn: host posts `event.ChatEvent{ShowPlanPager:
  …}` — add `ShowPlanPager` + `ShowPlanStatus` payload structs to
  `internal/event/event.go` next to `ShowReviewPager` (same `Pager any`
  pattern to avoid import cycles).
- **Test:** integration-lite — scripted provider drives tool calls
  add_item→submit_review; assert store state + exactly one ShowPlanPager
  event.
- **Verify:** `go test ./internal/app/ -run 'TestPlanNew' -count=1 -race`
- **Depends:** T5.1, T5.2, T3.3.

### T5.4 — Annotation round-trip
- Pager submit → `AnnotationsSummary` → `ctx.SubmitToAgent` (user-role
  message, B3a) → planner revises → `submit_review` → pager reopens at new
  revision with unresolved comments intact.
- **Test:** filmstrip or app-level test with scripted provider: comment on
  item-2 → submit → planner calls `update_item` + `resolve_comment` +
  `submit_review` → store shows revision 2, comment resolved.
- **Verify:** `go test ./internal/app/ -run 'TestPlanAnnotat' -count=1 -race`
- **Depends:** T5.3, T4.2.

### T5.5 — Remaining subcommands
- `review` (open pager at current revision), `approve` (confirm on open
  comments; sets approved — execution start is T6.2), `replan`
  (boundary-pause semantics §7/§16), `list` (filterable list: name, status,
  revision, done/total, updated), `delete` (`confirm=true`, stop bound run
  first; `id=*` bulk with confirmation).
- **Test:** table-driven per subcommand on fixture plans in `t.TempDir()`;
  approve-confirm path; delete stops a fake active run.
- **Verify:** `go test ./core/commands/ -run 'TestPlan' -count=1`
- **Depends:** T5.1 (approve wires to T6.2 — leave a `StartExecution func`
  field seam, default nil → error "execution not wired").

**Phase 5 gate:** `go vet ./core/commands/ ./internal/app/ ./internal/event/ && go test ./core/commands/ ./internal/app/ -count=1 -race`

---

## Phase 6 — Execution binding

### T6.1 — `Runtime.SetPlanID` (`core/orchestrator/runtime.go`)
- Mirror `SetGoalID` exactly: field `planID string`, setter
  `SetPlanID(id string)` (must be called before `Run`), and in the
  `run_started` payload block (runtime.go:252-260) add `if r.planID != "" {
  payload["plan_id"] = r.planID }`.
- **Test:** run with and without SetPlanID → payload contains/omits
  `plan_id`; replay preserves it.
- **Verify:** `go test ./core/orchestrator/ -run 'TestRuntime' -count=1 -race`
- **Depends:** none. Small, isolated.

### T6.2 — Plan↔run binding on approve (`internal/app/plan_execution.go`)
- Wire `PlanCommand.StartExecution` (T5.5 seam): `Approve()` → build hub
  runtime via `OrchestratorAdapter.NewRuntime` → `SetPlanID(plan.ID)` →
  objective `"Execute plan <name> (<plan-id>): <objective>"` →
  `StartExecution(runID)` on the plan store → start run.
- Execution orchestrator is fresh (spec §9 step 2 — no history transplant);
  its first user message is the `plan get` output.
- **Test:** approve fixture plan → run exists with `plan_id` payload, plan
  status `executing`, RunID recorded.
- **Verify:** `go test ./internal/app/ -run 'TestPlanExec' -count=1 -race`
- **Depends:** T6.1, T5.5.

### T6.3 — Worker factory: context limits + `task_outcome`
- In `internal/app/orchestrator_adapter.go` `agentConfig`/`build`
  (lines 93-119): after agent creation, `if rcfg.ContextWindow > 0 {
  agent.SetContextWindow(rcfg.ContextWindow) }`; `if rcfg.MaxTokens > 0` →
  override `ContextCompression.MaxTokens` (find the agent config field —
  `internal/agentic/agent.go` `ContextCompressionConfig`).
- For plan-run workers only (key on the run's `planID != ""` +
  `AcquireOptions.Fresh`): inject `TaskOutcomeTool` into the worker's tool
  set; never into orchestrator/planner or non-plan runs.
- **Test:** role with `context_window: 16384` → agent's effectiveMaxTokens ≤
  16384; plan-run worker has `task_outcome`, orchestrator does not,
  non-plan-run agent does not.
- **Verify:** `go test ./internal/app/ -run 'TestWorkerFactory|TestAgentConfig' -count=1 -race`
- **Depends:** T2.1, T3.6, T6.2.

### T6.4 — Typed outcome routing + clarification
- After `delegate` returns for a plan-run worker: parse the `task_outcome`
  JSON from the delegate result; record `RecordClarification` on question
  exchanges; surface `needs_clarification` to the orchestrator with the
  instruction to answer or `ask_user` then `rework` (spec §5.1, §9 step 5).
  Keep it minimal: the routing hint text lives in the planner/execution
  prompt (T5.2); code only guarantees the JSON is delivered unmodified and
  clarification events land in the plan store.
- **Test:** scripted provider — worker returns needs_clarification →
  orchestrator `ask_user` → answer → `rework` → `done`; store has two
  `clarification` events.
- **Verify:** `go test ./internal/app/ -run 'TestPlanClarif' -count=1 -race`
- **Depends:** T6.3.

### T6.5 — Resume from store
- `/plan:approve` (re-approve) on an `executing` plan resumes: new runtime,
  `SetPlanID`, first message = current `plan get`; done items are not
  re-run (their results are in the plan — spec §9.4).
- **Test:** scripted provider — run item-1 done, kill; resume; assert
  `start_item` targets item-2 and item-1 has no new events.
- **Verify:** `go test ./internal/app/ -run 'TestPlanResume' -count=1 -race`
- **Depends:** T6.4.

**Phase 6 gate:** `go vet ./... && go test ./core/orchestrator/ ./internal/app/ -count=1 -race`

---

## Phase 7 — Status overlay + footer (`tui/`)

### T7.1 — `PlanStatus` overlay (`tui/plan_status.go`)
- Read-only full-screen component per spec §10: header (name, objective,
  status, revision, run, progress bar), item rows with glyphs
  `☐ ◐ ☑ ✖ –`, detail pane, open-clarifications section.
- Keys: `↑/↓` select, `Enter` toggle detail, `q`/Esc close. Never steals
  focus on its own — opened only via `/plan:status` or the footer hint key.
- **Test:** render snapshots per status mix; filmstrip (`tui-test` skill):
  open during scripted run → item transitions visible → close.
- **Verify:** `go test ./tui/ -run 'TestPlanStatus' -count=1 -race`
- **Depends:** T1.5.

### T7.2 — Event plumbing + footer progress
- Plan-store mutations post onto the TUI `commandLoop` (same mechanism
  orchestrator events use — R1 single-owner, spec §15); overlay re-renders
  from a fresh store snapshot; footer line
  `plan <name> · item N/M in_progress: <title>` during execution.
- **Test:** filmstrip — mutation → footer text updates; overlay open →
  reflects mutation without restart.
- **Verify:** `go test ./tui/ ./internal/app/ -run 'TestPlan' -count=1 -race`
- **Depends:** T7.1, T6.2.

**Phase 7 gate:** `go test ./tui/ -count=1 -race` + `gocognit -over 18 ./tui/plan_status.go`

---

## Phase 8 — Housekeeping + docs

### T8.1 — Retention sweep
- Sweep function (wherever orchestrator retention lives — find via
  `grep -rn "retention" internal/app/`) deleting `.goa/plans/<id>` when plan
  status ∈ {`done`, `blocked`, `failed`} and `UpdatedAt` older than
  configured days; honors `enabled`/`days: 0`; same cadence hooks as
  orchestrator retention (startup + hourly + opportunistic).
- **Test:** fake clock — terminal+old deleted; draft+old kept; `days: 0`
  keeps everything; disabled keeps everything.
- **Verify:** `go test ./internal/app/ -run 'TestPlanRetention' -count=1 -race`
- **Depends:** T2.3.

### T8.2 — Headless `--plan` flag
- Add `--plan <id>` next to `--orchestrate` in
  `internal/app/bootstrap.go:300`; resolves plan, runs T6.5 resume path
  headless to completion, prints plan status.
- **Test:** headless integration (pattern from existing `--orchestrate`
  tests if any) or manual verification note.
- **Verify:** `go build ./... && go test ./internal/app/ -run 'TestHeadless' -count=1`
- **Depends:** T6.5.

### T8.3 — Deprecation notes
- `tools/plan/plan_mode.long.md`: add deprecation note pointing at `/plan`.
  `/help` entry for `plan_mode`: same note. No code changes.
- **Verify:** `go build ./...` (embed check) + manual `/help plan_mode` read.

### T8.4 — Docs
- New `docs/PLAN.md` (user guide: commands, pager keys, execution, retention);
  `README.md` feature blurb; `docs/COMMANDS.md` command table rows;
  `docs/TOOLS.md` `plan` + `task_outcome`; `docs/ORCHESTRATOR.md` role
  `context_window`/`max_tokens`.
- **Verify:** docs build/lint if configured; links resolve.

### T8.5 — Final gate
- `go vet ./... && go test -count=1 -race -cover ./... && gocognit -over 15 && gocyclo -over 12`
- Fix only gate failures; no feature work.

---

## Dependency summary (for the orchestrator)

```
T1.1,T1.2 (parallel) → T1.3 → T1.5 → T1.4
T1.1 → T1.6, T1.7
T2.1 → T2.2, T2.4 ; T2.3 independent
T1.5 → T3.1 → T3.2/T3.3/T3.4 → T3.5 ; T3.6 independent ; T3.4 → T3.7
T4.1 → T4.2
T1.4 → T5.1 ; T5.2 independent ; T5.1+T5.2+T3.3 → T5.3 → T5.4 ; T5.1 → T5.5
T6.1 → T6.2 → T6.3 → T6.4 → T6.5
T1.5 → T7.1 ; T7.1+T6.2 → T7.2
T2.3 → T8.1 ; T6.5 → T8.2 ; T8.3, T8.4 independent ; all → T8.5
```

Strictly sequential dispatch (per spec: one item in flight) still works —
each task's `Depends` list defines readiness; parallel markers are only for
future DAG execution.
