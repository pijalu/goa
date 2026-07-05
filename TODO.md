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

### Remaining — micro-tasked plan (in execution order)

Goal: a full agent-driven workflow invocable via `/orchestrate`, observable
in the TUI, goal-bindable, and resumable — validated against local LM /
DeepSeek. Commit + update this checklist at each milestone.

#### M1 — Subsystem wiring & Context exposure (Phase 7 core)  ✅
- [x] M1.1 `OrchestratorBuilder` interface + `ActiveRuntime` holder in
      `core/orchestrator/holder.go` (avoids core↔orchestrator import cycle)
- [x] M1.2 `OrchestratorAdapter` built once in `assembleSubsystems`, stored on
      `subsystems` (`orchAdapter` + `orchActive`)
- [x] M1.3 Per-run store root `.goa/orchestrator` wired into the command
- [x] gates + commit

#### M2 — Slash command surface (Phase 7)  ✅
- [x] M2.1 Real `OrchestrateCommand`: `new [topology] [goal <obj>] <obj>`,
      `list`, `resume <run-id>`, `steer <target> <text>`
- [x] M2.2 `new` builds a Runtime via the adapter, launches `Run` in a
      goroutine, forwards lifecycle events into the chat viewport
      (Flash + InterAgent); clears active holder on completion
- [x] M2.3 `list` prints `ListRuns()`; `resume` replays + relaunches;
      `steer` routes to handle / broadcast / orchestrator via Runtime methods
- [x] M2.4 Completions for subcommands + topology shortcuts
- [x] M2.5 Live integration test (LMStudio): `/orchestrate new fanout`
      drives real agents end-to-end, run persisted + replayed
- [x] gates + commit

#### M3 — DelegateTool & true hub topology (Phase 3 remainder)  ✅
- [x] M3.1 `Runtime.Delegate(ctx, role, task)`: acquires a bounded-pool slot,
      runs one turn, releases, returns the streamed answer (per-role message
      accumulation in `Runtime`, no store dependency)
- [x] M3.2 `OrchestratorDelegateTool` (adapter, implements `agentic.Tool` +
      `ContextTool`) wired into the orchestrator-role agent via `SetTools`;
      hub topology now drives ONLY the orchestrator (true delegation, not
      fanout fallback). Added `Agent.Tools()` getter symmetric with SetTools.
- [x] M3.3 Unit test `TestRuntime_DelegateRoundTrip`; **live test against
      LMStudio (`TestOrchestratorAdapter_LiveHub`)** — proves the orchestrator
      agent actually calls the delegate tool to dispatch the coder
- [x] gates + commit

#### M4 — Goal binding (Phase 6)
- [ ] M4.1 `Runtime.BindGoal(*goal.GoalManager, objective|id)`: create/load a
      goal, inject static reminder into orchestrator system prompt
- [ ] M4.2 Aggregate budget enforcement: after each agent turn, accrue token
      deltas to the goal; on exhaustion emit a budget-band reminder
- [ ] M4.3 Completion: orchestrator synthesizes sub-results → `UpdateGoal(done)`
      only when the completion criterion is met
- [ ] M4.4 Tests: goal-bound run reaches `complete`; budget exhaustion → blocked
- [ ] gates + commit

#### M5 — TUI orchestrator view (Phase 5)
- [ ] M5.1 `tui/orchestrator` View component (Summary / Orchestrator /
      per-agent tabs), subscribes to `Runtime.Events()` via `commandLoop`
- [ ] M5.2 Summary tab table (role/model/turns/tokens/status), goal header
- [ ] M5.3 Steering Editor bound to active tab (Summary→broadcast,
      Orchestrator→orch, Agent→that agent)
- [ ] M5.4 Toggle keybinding + open run picker (`ListRuns` + New + topology)
- [ ] M5.5 Filmstrip regression test (tui-test skill): events update view on
      a single goroutine; summary re-renders changed rows only
- [ ] gates + commit

#### M6 — Headless flag & telemetry (Phase 7 remainder)
- [ ] M6.1 `--orchestrate <run-id>` flag in `bootstrap.go` resumes a run headless
- [ ] M6.2 Telemetry: emit orchestrator lifecycle events (goal/telemetry pattern)
- [ ] gates + commit

#### M7 — End-to-end validation (Phase 8)
- [ ] M7.1 Interactive scenarios: hub+goal+steer→complete; fanout kill/resume;
      pipeline stage-carry; caps block-then-proceed
- [ ] M7.2 `-race` across all; no goroutine leak (R1 methodology)
- [ ] M7.3 README/docs update with orchestrator usage
- [ ] final gates + commit; close Track 2


## Notes
- The previous TODO content (agentic optimization pass) is fully resolved
  and lives in git history; closed bugs are under `docs/archive/`.
- All changes must pass the 5 gates run **separately**: `go vet ./...`,
  `staticcheck ./...`, `gocognit -over 15 .`, `gocyclo -over 12 .`,
  `go test -count=1 -race -cover ./...`.
