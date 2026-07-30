# PLAN — E2E Feature Validation Series (LM Studio, fake projects)

## Goal

Validate Goa's four key feature combinations end-to-end against local LM Studio
models, on throwaway fake projects, with machine-checked validation (not
self-reported success). Produce reusable runner scripts and document the
approach + all findings in `bugs.md`.

## Models (LM Studio, http://localhost:1234/v1)

| Role in tests | Model ID | Notes |
|---|---|---|
| Orchestrator / main agent | `qwen/qwen3.5-9b` | reasoning capable |
| Reviewer / companion | `qwythos-9b-v2` | review-focused |
| Coder | `google/gemma-4-e4b` | fast, small |

Local models are SLOW (few tok/s, JIT model load on first call). This is a
feature for Goa (async orchestration, durable goals, activity timeouts), not a
test blocker: all prompts are minimal, all timeouts generous, models are
warmed up before timing-sensitive runs.

## Scenarios

### T1 — Orchestration: qwen orchestrates, qwythos reviews, gemma codes
- Fake project config: `orchestrator.roles` = {orchestrator→qwen,
  reviewer→qwythos, coder→gemma-4}, topology=hub.
- Launch path A (seeded headless): write
  `.goa/orchestrator/<run-id>/events.jsonl` with one `run_started` event
  (objective+topology), then `goa --orchestrate <run-id> --yes`.
  `resumeObjective()` replays the log, rebuilds the runtime, drives the run.
- Launch path B (TUI via PTY driver): send
  `/orchestrate:new:topology=hub,objective=...` to the real TUI. Validates the
  command path users actually use.
- Objective: tiny coding task on the fake project (e.g. "Create answer.txt
  containing the word BLUE").
- **Validate** (from `events.jsonl`, not stdout):
  - `agent_started` for each role with the EXPECTED model id per role.
  - `agent_message` deltas from ≥2 distinct agents (real model output).
  - hub conversation: orchestrator delegation + worker reply events.
  - `run_finished` with ok=true; task artifact (answer.txt) exists.
  - `run_started.payload.goal_id` non-empty (orchestration auto-binds a goal).

### T2 — Companion: qwen main + qwythos companion
- Config: `multi_agent.enabled=true`, `companion_provider=lmstudio`,
  `companion_model=qwythos-9b-v2`, `show_inter_agent_messages=true`,
  active_model=qwen.
- Mode: framework-driven (`/companion:framework` → companion reviews EVERY
  turn) — deterministic, unlike agent-driven which depends on the LLM
  choosing `request_review`. Enabled by seeding `.goa/state.json`
  (`minor_mode=companion`) or via PTY `/companion:framework`.
- Headless prompt with a tiny deliverable.
- **Validate**: output/log shows an actual companion review message
  (qwythos-generated) AND the main agent's final answer; session/companion
  history non-empty. Two models actually talked.

### T3 — Goals + companion
- T2 config + `goa --goal --prompt "<objective with completion criterion>"`.
- Objective: create one file + trivially verifiable criterion.
- **Validate**: goal persists in `.goa/goals/`, goal driver runs turns,
  companion review fires per turn, goal reaches a terminal state
  (complete/blocked) with the artifact created; exit code reflects outcome.

### T4 — Orchestration + goals + companion
- T1 config + companion enabled (state.json) + seeded orchestration run.
- **Validate**: run finishes with per-role model evidence (as T1); the
  orchestrator-bound goal is created and driven to a terminal state
  (`.goa/goals/`); companion config coexists without breaking the run
  (document observed behavior of companion during orchestration).

## Infrastructure (reusable — lives in repo under `e2e/`)

- `e2e/lib.sh` — shared helpers: build binary, make fake project (config
  generator per scenario), warmup models, JSONL assertions.
- `e2e/ptydrive/main.go` — minimal PTY driver (creack/pty, already in
  go.mod): spawn goa TUI in a dir, send keystrokes, wait on FILE condition
  (events.jsonl content), dump raw output to a log, kill. File-condition
  waiting avoids parsing TUI redraws.
- `e2e/t1_orchestration.sh`, `t2_companion.sh`, `t3_goals_companion.sh`,
  `t4_all.sh`, `run_all.sh`.
- Fake projects: `/tmp/goa-e2e-<ts>/proj-<scenario>/` — tiny JS/Go files,
  own `.goa/config.yaml` (project-level config overrides home: provider
  lmstudio, model definitions with `thinking_level` low/off for speed,
  `active_model` per scenario), own `.goa/state.json` when seeding companion.
- Artifacts kept under `/tmp/goa-e2e-<ts>/` (configs, events.jsonl, stdout
  logs) for post-hoc inspection; paths reported in bugs.md.

## Validation principles

1. Never trust exit code alone — check artifacts (files, events.jsonl, goal
   state, session history).
2. Proof of model identity: `agent_started.model` per role must equal the
   configured model.
3. Proof of conversation: interleaved `agent_message` events from distinct
   agents + delegation events; companion review text present in T2/T3 logs.
4. Slow-model discipline: warmup each model (1-token chat) before runs;
   timeouts ≥ 5–10 min; `--thinking-level off` where compatible.
5. Every failure → entry in bugs.md with repro command + evidence, per
   bugs.md guidelines.

## Risks / open questions (resolve during execution)

- Does headless orchestrate path wire the goal binder (goal_id in
  run_started)? If not, T4 goal evidence comes from TUI path B.
- Companion behavior during foreground/headless orchestration — undocumented;
  observe and record.
- LM Studio may serialize requests across models (single GPU): multi-agent
  runs interleave model loads → expect slowness; if thrashing is fatal,
  document as environmental note, not a Goa bug.
- Seeded-run resume: confirm runtime accepts a log containing only
  `run_started` (smoke test before T1).
