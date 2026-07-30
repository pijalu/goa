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

## Configuration

Orchestration lives under the `orchestrator:` key of the [configuration
cascade](CONFIGURATION.md). Write it to exactly one of:

- **Project**: `.goa/config.yaml` (shared) or `.goa/config.local.yaml`
  (machine-local) — affects only this project.
- **Global**: `~/.goa/config.yaml` — default for every project.

Edit the file and restart Goa to apply.

### Roles are free-form names bound to a provider+model

`orchestrator.roles` maps **role name → provider+model binding**. Role names
are chosen by you: the `hub` topology requires and drives the role named
`orchestrator` (planner/delegator); every other role (`coder`, `reviewer`,
`tester`, ...) is a specialist the orchestrator can delegate to.

`model` and `provider` must reference IDs Goa already knows: providers come
from the `providers:` config or built-ins (see [PROVIDERS.md](PROVIDERS.md)),
models from that provider's configured models (`models:` entries or the
provider's registry). `/models` in the TUI lists what is actually configured;
an unknown ID fails the run at start.

Full example — planner/reviewer on one provider, coder on another:

```yaml
orchestrator:
  roles:
    orchestrator:                  # reserved name: hub planner/delegator
      model: glm-5.2
      provider: zai
    reviewer:
      model: glm-5.2
      provider: zai
      allowed_tools: [read, search, bash]
    coder:
      model: deepseek-v4-flash
      provider: opencode-go
      allowed_tools: [read, write, edit, bash]
      context_window: 128000       # optional, tokens; 0 = model default
      max_tokens: 64000            # optional compression threshold
  pool:
    max_total_agents: 4
    max_agents_per_model:
      glm-5.2: 2
      deepseek-v4-flash: 2
  defaults:
    topology: hub                  # hub | fanout | pipeline
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

While a run is active, a persistent tab bar appears above the input line with
two tabs:

- **Conversation** (default) — the orchestrator and every specialist agent
  stream their thinking, content, and tool calls into the main chat viewport as
  agent-labeled, in-place-updating blocks. This is the same chat viewport used
  by the main agent, so parallel agents each get their own distinct widget.
- **Stats** — shows the live agent table (role / model / provider / status /
turns / tokens / CH) and aggregate counters. Use `Ctrl+x` (or
`/orchestrate:tab:<n>`) to toggle between the two.

Run events (`run_started`, `agent_started`, `agent_message`, `agent_thinking`,
`agent_tool_call`, `agent_tool_result`, `agent_stats`, `agent_finished`,
`run_finished`) are appended to `.goa/orchestrator/<run-id>/events.jsonl`, so
every run is fully resumable and replayable via `ReplaySnapshot`.

## Steering

On the **Conversation** tab, steering targets the most recently started agent
(e.g. the currently delegated specialist). On the **Stats** tab, steering
broadcasts to all live agents. Use `/orchestrate:steer <agent-id|all|orchestrator> <text>`
or the input prompt shown in the footer.

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
