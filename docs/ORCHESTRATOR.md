<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<!-- Copyright (C) 2026 Pierre Poissinger -->

# Orchestrator

The orchestrator runs a **multi-agent workflow** for a single objective: it
composes a bounded agent pool, a per-run topology selector, an event-sourced
run log, and (optionally) a goal binding. It sits above the swarm/multiagent
layer and reuses the real agent pool for live model turns.

See [`ORCHESTRATION-DESIGN.md`](ORCHESTRATION-DESIGN.md) for the full design.

## Quick start

1. Configure roles + caps under `orchestrator:` in your config:

```yaml
orchestrator:
  roles:
    orchestrator:
      model: <model-id>           # the planner/delegator
    coder:
      model: <model-id>
      provider: <provider-id>     # optional per-role provider
      allowed_tools: [bash, edit] # optional tool allowlist
  pool:
    max_total_agents: 4
    max_agents_per_model:
      <model-id>: 2
  defaults:
    topology: hub                  # hub | fanout | pipeline
```

2. Run from the TUI or headless:

```
/orchestrate new hub Research X and summarize
/orchestrate new fanout goal <objective> <objective>   # goal-bound run
/orchestrate list
/orchestrate resume <run-id>
/orchestrate steer <agent-id|all|orchestrator> <text>
```

Headless resume of a persisted run:

```
goa --orchestrate <run-id>
```

## Topologies

- **hub** — the `orchestrator` role is driven and given a `delegate` tool; it
  dispatches sub-tasks to specialist roles and synthesizes their answers. Use
  when you want a model to decide who does what.
- **fanout** — every configured role runs one turn in parallel against the
  objective. Fastest for independent specialists.
- **pipeline** — roles run sequentially; each agent's output is carried forward
  as context to the next stage.

## Observability

While a run is active, a **Summary overlay** renders the live agent table
(role / model / status / turns / tokens) and updates on the command loop. Run
events (`run_started`, `agent_started`, `agent_message`, `agent_stats`,
`agent_finished`, `run_finished`) are appended to
`.goa/orchestrator/<run-id>/events.jsonl`, so every run is fully resumable and
replayable via `ReplaySnapshot`.

## Goal binding

Add `goal <objective>` to bind a run to a goal. The run accrues aggregate
token usage across all agents to the goal; on budget exhaustion the run
aborts and the goal is marked **blocked**; on success the goal is marked
**complete**.

## Caps & backpressure

`max_total_agents` bounds concurrent live agents across all models;
`max_agents_per_model` bounds per-model concurrency. Acquire blocks (FIFO,
context-cancellable) when a cap is saturated and proceeds as agents release.
Caps release on all exit paths (success, crash, context cancel).
