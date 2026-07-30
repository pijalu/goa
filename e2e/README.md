# e2e — Local e2e feature validation (LM Studio)

Reusable end-to-end validation of Goa's headline features against **local LM
Studio models** on throwaway fake projects. Built to answer: "do
orchestration / companion / goals actually work, with proof?"

## Models under test

| Role | Model |
|---|---|
| Orchestrator / main agent | `qwen/qwen3.5-9b` |
| Reviewer / companion | `qwythos-9b-v2` |
| Coder | `google/gemma-4-e4b` |

Requires LM Studio serving `http://localhost:1234/v1` with those models
available. Override with `LMS_URL`.

## How to run

```bash
# everything (build, warmup, T1..T4, summary). Slow: 1h+ on local models.
e2e/run_all.sh

# or a single scenario against a fresh fake project
E2E_ROOT=/tmp/goa-e2e/t1 bash e2e/t1_orchestration.sh
```

Artifacts (fake projects, configs, `events.jsonl`, headless logs, raw TUI
captures, `results.tsv`) land under `$E2E_ROOT` (default
`/tmp/goa-e2e/run-<ts>`; `run_all.sh` symlinks `/tmp/goa-e2e/last`).

## Scenarios

| Script | Feature combo | Path |
|---|---|---|
| `t1_orchestration.sh` | Orchestrate: qwen hub, qwythos reviewer, gemma coder | seeded run + `goa --orchestrate <run-id>` headless |
| `t2_companion.sh` | Companion qwen+qwythos, agent-driven **and** framework-driven | seeded `state.json` headless + `ptydrive` TUI `/companion:framework` |
| `t3_goals_companion.sh` | Goals + companion | `goa --goal` headless with seeded companion |
| `t4_all.sh` | Orchestration + goals + companion | `ptydrive` TUI `/orchestrate:new` (binds a goal) with seeded companion |

## How validation works (no self-reporting)

- **Orchestration**: `.goa/orchestrator/<run>/events.jsonl` is asserted with
  `jq` — every `agent_started` role→model mapping must match config
  (orchestrator=qwen, reviewer=qwythos, coder=gemma); ≥2 distinct agents must
  emit `agent_message` (real conversation); orchestrator must emit
  `agent_tool_call` (delegation); `run_finished` must exist.
- **Companion**: companion exchange must appear in the headless output
  (`request_review`) and/or `.goa/state.json` `companion_history`.
- **Goals**: `.goa/goals/goal-events.jsonl` must contain `goal.create` and a
  terminal event; for T4 the orchestration's `run_started.payload.goal_id`
  must be non-empty and tracked in the goal log.
- **Artifacts**: the actual file the task asked for must exist with the
  expected content. Exit codes are advisory; artifacts decide.

## Key techniques (reuse these)

1. **Seeded headless orchestration** — `goa --orchestrate` only *resumes* a
   run. `seed_orch_run` (lib.sh) writes `.goa/orchestrator/<run-id>/events.jsonl`
   with a single `run_started` event (objective+topology); the headless resume
   path (`resumeObjective` → `ReplaySnapshot`) then drives the whole run.
   NB: the headless path does **not** wire the goal binder (goal binding only
   happens in the TUI `/orchestrate:new` path) — see bugs.md findings.
2. **Seeded companion** — writing `.goa/state.json` with
   `minor_mode=companion, agent_driven_enabled=true` restores agent-driven
   companion at startup (re-arms `request_review`/`delegate_to`).
   Framework-driven mode (`/companion:framework`) is in-memory only and needs
   the TUI — hence `ptydrive`.
3. **ptydrive** (`e2e/ptydrive`) — runs goa TUI in a PTY, sends keystrokes,
   and waits on a **file condition** (glob+regex, e.g. `run_finished` in
   events.jsonl) rather than scraping ANSI. Raw stream saved for inspection.
4. **Fake projects** — `mk_fake_project` + `write_base_config` give each
   scenario an isolated `/tmp` project whose `.goa/config.yaml` pins
   provider/models/thinking to LM Studio (project config overrides home).
5. **Slow-model discipline** — warm up each model (JIT load) before runs;
   timeouts are generous (10–25m); prompts are tiny; thinking off.

## Findings

See `bugs.md` (repo root) — test approach summary + every issue found with
repro commands and evidence.
