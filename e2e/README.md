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

## Mock LLM (no LM Studio required)

`mockllm/server.py` is a deterministic, dependency-free OpenAI-compatible
server for scenarios that must not depend on a real model (e.g. compression
validation):

- **Normal turns**: streams ~30 KB of filler (`MOCK_FILLER_KB`), so history
  grows past a small `context_compression.max_tokens` ceiling within a turn or
  two.
- **Summarize requests**: when the system prompt starts with `Summarize`, it
  streams a short fixed reply so Compact produces a real summary.
- Serves `GET /v1/models` (advertises `context_length: 32768`) and
  `POST /v1/chat/completions` (SSE and non-streaming).

```bash
# via lib.sh helpers (readiness-wait + teardown):
source e2e/lib.sh
start_mock_llm /tmp/goa-e2e/mock-llm.log   # sets MOCK_LLM_URL, MOCK_LLM_PID
# ... run goa against $MOCK_LLM_URL ...
stop_mock_llm

# or standalone:
MOCK_LLM_PORT=8017 python3 e2e/mockllm/server.py &
```

Env: `MOCK_LLM_HOST`, `MOCK_LLM_PORT`, `MOCK_MODEL_ID`,
`MOCK_CONTEXT_LENGTH`, `MOCK_FILLER_KB`, `MOCK_LLM_LOG` (request log path;
unset = silent). Point a throwaway project at it with
`providers: [{id: mock, endpoint: http://127.0.0.1:8017/v1}]`.

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
- **Companion**: real `request_review` tool calls appear in the headless
  output (`-- tool call request_review`); the review itself is delivered
  in-session as a `[Message from companion]` user message — assert it in
  `.goa/sessions/*.jsonl` (agent-driven headless neither renders
  `-- companion start` nor persists `companion_history`; those two are
  framework/TUI-only evidence).
- **Goals**: `.goa/goals/goal-events.jsonl` must contain `goal.create` and a
  terminal state — note `core/goal` has only THREE event types
  (`goal.create`/`goal.update`/`goal.clear`); completion is a `goal.update`
  carrying `"status":"complete"` (or blocked/paused), not a `goal.complete`
  event. For T4 the orchestration's `run_started.payload.goal_id` must be
  non-empty and tracked in the goal log.
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
   The config also pins `tools.enabled.request_review/delegate_to: true`
   explicitly — never inherit these from the developer's home config, which
   may carry stale `false` values serialized from old defaults (bugs.md F5).
5. **Slow-model discipline** — warm up each model (JIT load) before runs;
   timeouts are generous (10–25m); prompts are tiny; thinking off.

## Findings

See `bugs.md` (repo root) — test approach summary + every issue found with
repro commands and evidence.
