<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<!-- Copyright (C) 2026 Pierre Poissinger -->

# Orchestration — MICROSTEP execution plan (separate session)

Excluded from the 2026-07-04 fix session. Execute in a dedicated later session. Follow steps in order; do not redesign. Same 5 gates, run separately.

## Confirmed decisions (from Q&A)
- **Topology:** per-run selector (D). Existing `ForegroundOrchestrator`/`TaskOrchestrator`/swarm `runAll`/workflows are the backends the user chooses among at run start.
- **Models:** config-only role→model map (A). Roles define `model`/`provider`/`allowed_tools`.
- **Pool caps:** bounded agent pool with `max_agents_per_model` and `max_total_agents` — acquire blocks/queues when a cap is hit.
- **Tabs:** one tab per agent instance + one orchestrator tab + one **Summary tab** (aggregate stats).
- **Steering:** target driven by active tab — Summary → broadcast to all; Orchestrator/Agent → that target only. Orchestrator may also post messages into managed agents on its own.
- **Persistence:** fully event-sourced & resumable (A) under `.goa/orchestrator/<run-id>/`.
- **Execution layer:** layered ABOVE swarm (A) — reuse `runAll`/`runOne`/`prepareConfig` as the primitive; swarm tool stays.
- **Goal binding:** optional (B) — a run may be goal-less or bound to a `core/goal` goal (per-agent + aggregate budgets).

## References (read fully before coding)
- `multiagent/agent_pool.go`, `agent_tool.go`, `agent_driven_tools.go`, `orchestrator.go`, `foreground_orchestrator.go` (`InjectSteering`/`checkSteering`), `task_orchestrator.go`, `pipeline.go`, `pipelines.go`, `runner.go`, `workflow_registry.go`, `workflow_tool.go`, `mode_resolver.go`.
- `core/swarm/` (mode.go, injection.go, enter/exit_reminder.md), `tools/swarm/agent_swarm.go` (`prepareConfig`/`runAll`/`runOne`).
- `core/goal/` (model.go, mode.go, store.go, budget.go, injection.go, outcome.go).
- `core/steering.go` (SteeringQueue — generalized by fix B5), `core/event`, `core/agentmanager_events.go`.
- `tui/goal/panel.go`, `tui/swarm/renderer.go`, `tui/status.go`, `tui/footer_data.go`, `tui/compositor.go`, `tui/model.go`.
- kimi-code patterns: append dynamic content as user-role message on top of history; `mergeConsecutiveUserMessages`.

---

# Phase 0 — Foundations & config schema

1. Create package `core/orchestrator/` (new). Add `doc.go` describing the package and the per-run topology selector.
2. Config schema (`config/config.go`): add an `Orchestrator` section:
   ```
   orchestrator:
     roles:
       <name>: { model: <id>, provider: <id>, allowed_tools: [<names>] }
     pool:
       max_total_agents: 8
       max_agents_per_model: { <model_id>: <int> }
     defaults:
       topology: hub | fanout | pipeline
   ```
   Implement struct `OrchestratorConfig` with `Roles map[string]OrchestratorRole`, `Pool.OrchestratorPoolConfig`, `Defaults.Topology`. Add defaults in `config/defaults.go`. Add validation in `config/config_validate.go` (role model must exist in configured models; caps ≥1). Wire into merge (`config/config_merge.go`).
3. Add config completion entries in `core/commands/config_completion.go`: `orchestrator.roles.*`, `orchestrator.pool.max_total_agents`, `orchestrator.pool.max_agents_per_model.*`, `orchestrator.defaults.topology` (enum: hub/fanout/pipeline).
4. Tests: `config/orchestrator_test.go` — parse/merge/validate/default; unknown model → error.

# Phase 1 — Bounded agent pool with caps

5. Add `core/orchestrator/pool.go`: `type BoundedAgentPool struct` wrapping `*multiagent.AgentPool`. Fields: `totalCap int`, `perModelCap map[string]int`, current counts `total int`, `perModel map[string]int`, `mu sync.Mutex`, `waiters []chan struct{}`.
6. `Acquire(roleName string) (*AgentHandle, error)`: resolve role→model from `OrchestratorConfig.Roles`; block (register waiter) while `total>=totalCap` OR `perModel[model]>=perModelCap[model]`; on success increment counts and `pool.GetOrCreate(role)` (reuse multiagent). Return handle.
7. `Release(h *AgentHandle)`: decrement counts, signal the head waiter (FIFO). Use `select{ default }` on signal so a non-blocking notify never blocks.
8. Tests `core/orchestrator/pool_test.go` (-race): concurrent Acquire up to caps; assert block then release unblocks; per-model cap respected; total cap respected.

# Phase 2 — AgentHandle & live state

9. Add `core/orchestrator/handle.go`: `type AgentHandle struct { ID, Role, Model string; agent *agentic.Agent; stats *AgentStats; events chan Event; steering *core.SteeringQueue; done chan struct{} }`.
10. `AgentStats`: `{Turns, TokensIn, TokensOut, CacheRead, CacheCreation, ToolCalls int; Status AgentStatus; StartedAt, UpdatedAt time.Time}`. Updated from provider usage events (subscribe to the existing usage/cache hook used by `internal/app/stats.go`).
11. Per-agent steering queue: each handle owns a `*core.SteeringQueue` (generalized from B5). Methods `Steer(text)` (Append) and `drainSteering()` (Flush).
12. Tests: stats update on synthetic usage event; steering append/drain.

# Phase 3 — Orchestrator runtime (topology selector)

13. Add `core/orchestrator/orchestrator.go`: `type Orchestrator struct` with `cfg OrchestratorConfig`, `pool *BoundedAgentPool`, `topology Topology`, `goal *goal.Goal` (optional), `store *EventStore`, `agents map[string]*AgentHandle`, `orchestratorAgent *agentic.Agent`, `eventBus chan Event`.
14. `Topology` enum: `Hub`, `Fanout`, `Pipeline` with a factory `NewTopology(name, deps) Topology` mapping to existing primitives:
    - `Hub` → delegation via a `DelegateTool` that calls `pool.Acquire(role)` + `runOne`.
    - `Fanout` → reuse `tools/swarm/agent_swarm.go::runAll` with a constructed `multiagent.AgentConfig`.
    - `Pipeline` → reuse `multiagent/pipeline.go`/`ForegroundOrchestrator` stages.
15. `DelegateTool` (`core/orchestrator/delegate_tool.go`, implements `agentic.Tool`): input `{role, task, [model_override?]}`; calls `pool.Acquire(role)`, emits `AgentStarted`, runs the sub-agent (stream forwarded as `AgentMessage` events), on finish emits `AgentFinished{outcome}` then `pool.Release`. The orchestrator agent has this tool available.
16. `Run(ctx, objective)`:
    - If goal-bound: create/load goal via `core/goal` (reuse `GoalManager`), inject static reminder (B3a model) into orchestrator system prompt.
    - Acquire orchestrator agent (role `orchestrator`, its own model from `cfg.Roles["orchestrator"]`).
    - Drive the chosen topology until the goal reaches a terminal state (goal-bound) or the orchestrator emits a terminal message (goal-less).
    - Between cycles: drain orchestrator steering queue + each agent's steering queue; inject as appended user-role messages (kimi-code style, same helper as B3a/B5).
17. Orchestrator→agent posting: the orchestrator may call `DelegateTool` with a steering payload that targets an existing agent handle (`AgentSteered` event) — allowed per Q3b.
18. Tests: `orchestrator_test.go` with a scripted/fake provider (`multiagent/test_provider_test.go` is the model): 
    - Hub: 1 delegation round-trip emits Started/Message/Finished.
    - Fanout: N agents run, all events emitted, counts released.
    - Pipeline: stages run in order, each feeds the next.
    - Caps: exceeding `max_total_agents` blocks then proceeds on Release.

# Phase 4 — Event sourcing & resumability

19. Add `core/orchestrator/store.go`: `EventStore` (NDJSON under `.goa/orchestrator/<run-id>/events.jsonl`), mirroring `core/goal/store.go`. Event types: `RunStarted`, `AgentStarted`, `AgentMessage`, `AgentSteered`, `AgentStats`, `AgentFinished`, `RunFinished`. Append on every state transition.
20. On `NewOrchestrator`, `os.MkdirAll(".goa/orchestrator/<run-id>", 0o755)`; append-only event log; snapshot agent stats periodically (reuse `core/goal/serialize.go` patterns).
21. `Resume(runID)`: replay events to rebuild `agents` map, stats, steering queues, and goal state; resume the run from the last non-terminal event. Crashed mid-flight agents are marked `Crashed` and re-acquired.
22. Tests: append/replay round-trip; resume after a synthetic crash restores agent set + stats + goal; idempotent.
23. List API: `ListRuns()` scans `.goa/orchestrator/*/events.jsonl` for the TUI run picker.

# Phase 5 — TUI: orchestrator view + tabs + summary

24. New package `tui/orchestrator/`. `type View struct` implementing `tui.Component`: holds tabs `[]Tab` (`Summary`, `Orchestrator`, then one per agent), `active int`, a local chat viewport per tab (reuse `tui/chat_viewport.go`), and a steering `Editor`.
25. Tab content:
    - **Summary tab**: a table (reuse `tui/status.go` styling per row) — one row per agent: Role, Model, Turns, Tokens, CacheRead%, Status. Plus the goal header (reuse `tui/goal/panel.go`) when goal-bound.
    - **Orchestrator tab**: orchestrator chat transcript (streamed `OrchestratorMessage` events).
    - **Agent tab**: that agent's streamed conversation + a compact stats header.
26. Steering input: a dedicated `Editor` bound to the active tab. On submit:
    - Summary tab → broadcast: `orch.SteerAll(text)` (Append to every agent queue + orchestrator).
    - Orchestrator tab → `orch.OrchestratorSteer(text)`.
    - Agent tab → `agentHandle.Steer(text)`.
    Show pending steering pinned above the input (reuse `footer_data.SteeringPending` from B5, generalized to per-target).
27. Event subscription: the View subscribes to the orchestrator eventBus. ALL events are posted onto the TUI `commandLoop` as messages (do NOT touch component state from goroutines — preserve the single-owner invariant; see R1 findings). Each event updates the relevant tab's viewport + the summary table + stats.
28. Open/close: a keybinding (e.g. `Ctrl+O`) or `/orchestrate` slash command toggles orchestrator mode. On open, show run picker (existing runs from `ListRuns()`) + "New run" + topology selector + optional goal bind. Register the command in `core/commands/` (`init()` self-register per AGENTS.md).
29. Cursor/focus: the orchestrator view's steering Editor owns the cursor (apply the B6 compositor fix — `CURSOR_MARKER` honored for the topmost component). Tab switching via `←/→`.
30. Tests `tui/orchestrator/*_test.go`: tab switching; summary table renders N agents; steering submit routes to the correct target based on active tab; events from a fake orchestrator update the view; all state mutations happen on a single goroutine (assert via a commandLoop stub).

# Phase 6 — Goal binding integration

31. `BindGoal(runID, goalID or objective)`: if objective given, create goal via `core/goal.GoalManager`; inject static reminder into the orchestrator system prompt (B3a) and per-agent sub-objectives.
32. Budget enforcement: per-agent budget + aggregate. Reuse `core/goal/budget.go`. When aggregate budget exhausted → orchestrator receives a budget-band reminder (dynamic, appended user message) and must call `UpdateGoal(blocked)`.
33. Completion: agents report sub-results; orchestrator synthesizes and calls `UpdateGoal(complete)` only when all sub-results satisfy the completion criterion (reuse `core/goal/outcome.go`).
34. Tests: goal-bound run reaches `complete` only after all agents finish; budget exhaustion forces `blocked`; aggregate vs per-agent budget accounting correct.

# Phase 7 — Wiring & command surface

35. Wire `core/orchestrator` into `internal/app/subsystems.go`: construct the pool from `cfg.Orchestrator`, expose the orchestrator manager on `core.Context`.
36. Slash commands (`core/commands/orchestrator*.go`, self-register): `/orchestrate new [topology] [goal <id|objective>]`, `/orchestrate list`, `/orchestrate resume <run-id>`, `/orchestrate steer <agent-id|all|orchestrator> <text>`. Add completions.
37. Headless mode: `--orchestrate <run-id>` flag in `bootstrap.go` to resume a run headless (mirrors `--goal`).
38. Telemetry: emit orchestrator lifecycle events via the existing telemetry client (`core/goal/telemetry.go` pattern).

# Phase 8 — Validation & gates

39. End-to-end interactive_shell scenarios:
    - New hub run with 2 distinct-model agents + goal; switch tabs; watch live stats; steer one agent; broadcast from Summary; confirm orchestrator posts back to an agent; reach `complete`.
    - Fanout run of 3 agents; Summary tab shows all; kill terminal mid-run → restart → `Resume` restores the run.
    - Pipeline run; stage outputs feed next stage.
    - Caps: configure `max_total_agents: 2`, launch 3 → third blocks until one finishes.
40. Stability: `-race` across all the above; no goroutine leak (R1 methodology).
41. Perf: confirm per-event TUI update cost is bounded (R2 methodology); summary table re-renders only changed rows.
42. Run all 5 gates separately; fix any new violation.
43. Update README/docs with orchestrator mode usage; commit.

## Risk notes (carry into execution)
- Funnel ALL orchestrator events through the TUI `commandLoop` (post as messages) — never mutate component state from event goroutines (preserves R1 single-owner invariants).
- Keep orchestrator/agent system prompts byte-stable across turns; deliver dynamic progress (stats, budget band) as appended user-role messages (B3a model) so prompt cache stays warm.
- Per-model + total caps must release on all exit paths (panic, ctx cancel, crash) — use `defer pool.Release(h)` and a `recover()` in the run wrapper.
