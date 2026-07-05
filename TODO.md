<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<!-- Copyright (C) 2026 Pierre Poissinger -->

# TODO — execution tracker

Two-track plan, decided 2026-07-04: concrete bugs + reviews ship first
(single session), Orchestration is a separate dedicated session.

## Track 1 — Bugs + Reviews (execute now)
**Plan:** [`docs/FIX-PLAN-2026-07-04.md`](docs/FIX-PLAN-2026-07-04.md)

97 numbered microsteps, in execution order:

- [x] **B1** — Thinking-loop discoverability (completion + config menu)
- [x] **B2** — Up-arrow on empty line / cannot navigate to empty line (follow-up fixes: recall history when cursor is at the start of the first visual line; fix `findVisualLine` boundary so Up moves from a wrapped second visual line to the first visual line; add `Editor.VisualCursor` for debugging; **additional fix:** `buildVisualLineMap` now includes a trailing empty logical line so Up from an empty second line works; agentic DOM integration test covers both decoded keys and raw CSI-u `Ctrl+Enter`)
- [x] **B3** — `goal` tool: `.goa/goals` location + disable flag (default off) + cache collapse (kimi-code append-on-top model)
- [x] **B4** — Spinner disappears after 1st tool call
- [x] **B5** — Steering messages enqueued (`prompt.steered`-style injection)
- [x] **B6** — Config selection list cursor at `search>` marker
- [x] **B7** — smartsearch review (fix everything found)
- [x] **R1** — Stability review of all TUI code (drainInput goroutine leak fixed; panic recovery added to commandLoop/renderLoop; callback entry points already covered by dispatchInput recover; edge paths handled by existing shutdown/resize paths)
- [x] **R2** — Perf review of all TUI code (profiled via `-perf-load`; top user hotspots are compositor differential rendering and terminal write syscalls; no user-code regressions found; drainInput goroutine removal also reduces allocation churn)
- [x] **R3** — Functional review: workflow/swarm/multi-agent/goal (full test suite passes under `-race`; no new regressions found; steering queue already aligned with multi-agent orchestrator)
- [x] **Close** — gates re-run separately, bugs archived to `docs/archive/bugs.2026-07-04.md`, `bugs.md` reduced to guidelines only

## Track 1 addendum — Agentic TUI test model
A flat DOM model was added to the TUI so agents and tests can query exact component bounds and cursor positions without parsing ANSI:
- `AgentNode` (name, type, rect, text, focus, cursor, children)
- `AgentFrame.Nodes`, `FindNode`, `FindNodeByType`, `FocusedNode`, `CursorNode`, `Dump`
- `TUI.SendKey(key)` for synchronous input injection in tests
- New tests in `tui/agentic_dom_test.go` including the B2 regression with raw CSI-u bytes
- Backed by the same `Scene`/`AgentFrame` pipeline that renders the real terminal

## Track 2 — Orchestration (in progress)
**Plan:** [`docs/ORCHESTRATION-DESIGN.md`](docs/ORCHESTRATION-DESIGN.md)

43 numbered microsteps across 8 phases. Confirmed decisions baked in:
per-run topology selector (hub/fanout/pipeline), config-only role→model map
with bounded pool (`max_agents_per_model` + `max_total_agents`), tabs per
agent + orchestrator + Summary, tab-driven steering (orchestrator may post
to agents), fully event-sourced & resumable under `.goa/orchestrator/<run-id>/`,
layered above swarm, optional goal binding.

### Progress (this session)
Foundation layers shipped, fully tested under `-race`, all 5 gates green:

- [x] **Phase 0 — Config schema** (`OrchestratorConfig`): types in
  `config/config.go` (`OrchestratorConfig`/`OrchestratorRole`/
  `OrchestratorPoolConfig`/`OrchestratorDefaultsConfig`), defaults in
  `configs/default.yaml`, merge in `config_merge.go::mergeOrchestrator`,
  validation in `config_validate.go::validateOrchestrator` (role model must
  exist in configured models; caps ≥1; topology enum hub/fanout/pipeline),
  completions in `config_completion.go`, tests in `config/orchestrator_test.go`.
- [x] **Phase 1 — Bounded agent pool with caps** (`core/orchestrator/pool.go`):
  `BoundedAgentPool` with FIFO waiting, context-cancellable `Acquire`,
  idempotent `Release`, factory-error rollback, `Live()`/`Counts()` observers.
  Depends on an `AgentFactory` abstraction (SOLID) so the cap logic is
  unit-tested without a live provider. `-race` concurrent stress test passes.
- [x] **Phase 2 — AgentHandle & live stats** (`core/orchestrator/handle.go`):
  `AgentHandle` (ID/Role/Model/Stats/Steering/done), `AgentStats` with
  mutex-protected counters + `Snapshot`, per-handle `core.SteeringQueue`
  (generalized from B5), `Steer`/`DrainSteering`, idempotent `markReleased`.
- [x] **Phase 4 — Event sourcing & resumability** (`core/orchestrator/store.go`,
  `run_snapshot.go`): NDJSON `FileEventStore` under
  `.goa/orchestrator/<run-id>/events.jsonl`, monotonic seq stamped by store,
  corrupt-line tolerant replay; `ReplaySnapshot` rebuilds the full run state
  (agents, stats, steering, goal, topology) for `Resume` (Phase 4 step 21,
  side-effect-free core); `ListRuns` for the TUI run picker (step 23).
  Topology selector `ParseTopology`/`Topology` enum also landed (Phase 3 step 14).
- [x] **Phase 3 (runtime core) — `Orchestrator` runtime**
  (`core/orchestrator/runtime.go`): `Runtime.Run(ctx, objective)` drives the
  topology (fanout parallel / pipeline sequential / hub = orchestrator-then-
  fanout), composes the bounded pool + handles + store, emits the full event
  lifecycle (RunStarted→AgentStarted→AgentStats→AgentFinished→RunFinished),
  drains steering into turns, exposes `Events()` for TUI subscription, and is
  fully unit-tested with fake turn funcs (lifecycle, crash isolation, pipeline
  ordering, cap-block-then-proceed, steering drain).
- [x] **Phase 3/7 (adapter) — `internal/app/orchestrator_adapter.go`**:
  `OrchestratorAdapter.NewRuntime` bridges the pure runtime to a real
  `multiagent.AgentPool` — translates `agentic.OutputEvent` into AgentStats
  updates + AgentMessage events. **Validated end-to-end against live LMStudio**
  via a repeatable integration test (`orchestrator_adapter_integration_test.go`)
  that auto-skips when no local model is reachable: real streaming, token
  stats captured, both agents finished, run persisted + replayed (`finished=true`).

### Remaining (genuinely integration-heavy; needs a focused session)
These phases drive live `agentic.Agent` streams, the TUI component tree, the
app wiring layer, and goal-budget enforcement. Per the design doc they belong
in a dedicated session, and shipping them without end-to-end live tests would
violate Hard Rules #1/#3/#4. The foundation above is the prerequisite they
build on.

- [ ] **Phase 3 (rest)** — `DelegateTool` for true hub topology (orchestrator
  agent delegates to sub-agents via a tool, currently hub falls back to
  orchestrator-then-fanout); observer dedupe across multiple runs sharing a
  cached agent (use `CreateTaskAgent` per run for long-lived processes).
- [ ] **Phase 5** — `tui/orchestrator` View: Summary/Orchestrator/per-agent
  tabs, steering Editor, event subscription via `commandLoop`.
- [ ] **Phase 6** — Goal binding: `BindGoal`, per-agent + aggregate budget
  enforcement, completion synthesis.
- [ ] **Phase 7** — Wiring (`subsystems.go`), `/orchestrate` slash commands,
  `--orchestrate <run-id>` headless flag, telemetry.
- [ ] **Phase 8** — Validation & gates across the full interactive scenarios.

Also fixed this session: pre-existing `docs` gate failure — `fix-plan-2026-07-04.md`
and `orchestration-design.md` were committed lowercase, breaking
`TestList_UppercaseNames`; renamed to `FIX-PLAN-2026-07-04.md` /
`ORCHESTRATION-DESIGN.md` (the names TODO.md and the design doc already
referenced). `cmd/webbuild` is unaffected (it lowercases stems and excludes
`fix-plan-`).

## Notes
- The previous TODO content (agentic optimization pass) is fully resolved
  and lives in git history; closed bugs are under `docs/archive/`.
- All changes must pass the 5 gates run **separately**: `go vet ./...`,
  `staticcheck ./...`, `gocognit -over 15 .`, `gocyclo -over 12 .`,
  `go test -count=1 -race -cover ./...`.
