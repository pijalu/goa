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

## Browser scenario (G7 — `w1_webui_browser.sh`)

`e2e/w1_webui_browser.sh` is the browser twin of the PTY scenarios: it starts
`goa server` and drives it in a **real Chrome** through
`/opt/homebrew/bin/agent-browser` (`open` / `click` / `press` / `screenshot` /
`eval`), asserting the live DOM at every stage.

```bash
e2e/w1_webui_browser.sh                     # defaults to 127.0.0.1:8099
E2E_ROOT=/tmp/my-run GOA_WEB_PORT=8199 e2e/w1_webui_browser.sh
```

It is **not** part of `run_all.sh`: the T-series need LM Studio, this one needs
a real provider for the pinned model in your own `~/.goa/config.yaml`. The model
is pinned through the environment only —
`GOA_ACTIVE_PROVIDER=opencode-go`, `GOA_ACTIVE_MODEL=space-bunny-free`
(override with `GOA_WEB_PROVIDER` / `GOA_WEB_MODEL`) — because the config
cascade lets env win over every file layer, so the run cannot silently answer
with whatever model you last used.

Assertions (each one is a `PASS`/`FAIL` line in `results.tsv`, with a
screenshot per stage under `shots/`): page reaches `#status[data-state=live]`,
the pinned model appears in the status bar, a tool call (`$ echo G7-E2E-OK`) and
the model's reply are rendered, the grid **grows incrementally** while a turn is
in flight (sampled from inside the page, not from the shell), `/help` lists
commands, `/config` opens and its `›` selector moves on `ArrowDown`, the type
filter narrows the list, `Escape` closes it, and the model pin still holds.

Typing note: `agent-browser keyboard type` uses `Input.insertText`, which fires
no `keydown`, and `internal/webui/assets/app.js` deliberately sends *key
descriptors* to the server (the server owns the byte table). The script
therefore types character by character with `press`.

## Attach & multi-project contract (`w3_attach_multiproject.sh`)

`e2e/w3_attach_multiproject.sh` asserts the **documented contract** of the
web UI's interop features against the real binary — every expected result is
a claim made by README.md / docs/WEBUI.md / `--help` output, checked against
actual process behavior. It exists because a docs review once found a flag
(`--server-max-sessions`) that was documented but never implemented: no test
was watching the docs↔code seam. This suite is that watcher. It needs **no
LLM provider** (the attach flow types into the editor but never submits a
prompt) — only `curl`, `expect` (PTY for raw-mode attach) and `python3`.

```bash
# defaults: GOA_BIN=/tmp/goa-e2e/goa, port 18811, artifacts /tmp/goa-w3/w3-<ts>
e2e/w3_attach_multiproject.sh

# explicit binary / port / artifact root (no LM Studio needed, ~3 min)
GOA_BIN=./goa GOA_WEB_PORT=18811 E2E_ROOT=/tmp/my-run e2e/w3_attach_multiproject.sh
```

Prerequisite: a configured `~/.goa` (first run wizard done) — each project
session is a real child `goa server` whose readiness gate only opens once the
session is wired; an unconfigured home would hang the connect handshake until
the ready timeout.

Expected results (one `PASS`/`FAIL` line each in `results.tsv`; exit 0 iff
all pass):

| Assertion | Documented behavior under test |
|---|---|
| `help_flags` / `attach_help` | `goa server --help` and `goa attach --help` list every flag the docs describe |
| `help_topics` | `goa help server` + `goa help attach` answer with their documented content |
| `sup_health` | supervisor `/healthz` boots with `session:""`, `sessions:0` |
| `connect_opens` / `connect_reuses` | `POST /connect {path}` spawns a session; the same path (trailing slash too) returns the same id |
| `connect_badpath` / `connect_escape` | relative path, and a symlink escaping the root, are refused with the documented reasons |
| `connect_symlink_inside` | a symlink to a directory *inside* the root resolves to that directory's session |
| `sessions_list` | `GET /sessions` lists live sessions |
| `max_sessions` | `--server-max-sessions 1` refuses the second distinct project with "session limit reached" |
| `form_redirect` | urlencoded `/connect` (browser form) → 303 to `/s/<id>`; rejected path → HTML error |
| `proxied_text` | `/s/<id>/text` serves the child's live grid through the proxy |
| `attach_flow` / `attach_keys_reach_server` / `attach_detach_survives` | attach under a real PTY renders frames, typed keystrokes reach the server grid, `Ctrl+]` detaches, session alive with 0 clients |
| `plain_fallback` | a plain server has no `/connect` (404); `/healthz` names the session |
| `idle_reap` | `--server-session-idle 2s` reaps the untouched child at the reaper tick; transcript stays on disk |
| `shutdown_clean` | no goa processes or listeners left on this run's ports |

Suite hygiene — keep these properties when editing:

* **Hermetic ports**: every `start_supervisor` first clears its port of stale
  occupants and verifies readiness against *its own* pid — an orphaned
  supervisor from a crashed run otherwise poisons the next run silently.
* **Real pids**: children spawn with `exec` so the pidfile records the goa
  process, not a wrapper shell — `stop_supervisor` must kill what is running.
* **EXIT trap**: every started process is stopped on *any* exit path; the
  shutdown check is scoped to this run's ports and Unix-socket children (an
  unrelated goa process elsewhere on the machine is not this suite's leak).
* **pipefail discipline**: `grep` exits 1 on no-match — guard counts with
  `|| true`, or the suite dies exactly when the expected outcome (zero
  leftover processes) happens.

The attach PTY helper is a quoted heredoc (`<<'EXPECT'`) that reads
`$env(GOA_BIN)` / `$env(W3_PORT)` / `$env(W3_PROJ)` — unquoted, bash would
expand `$env(...)` itself and die under `set -u`.

## Clipboard scenario (B6 — `clipimg.sh`)

`e2e/clipimg.sh` is the terminal twin of the web page's image paste: it puts a
known 8×8 PNG on the OS clipboard, boots a real `goa` in a PTY (config pinned to
the mock LLM — the paste never reaches a model) and presses `Ctrl+V`. It asserts
the **rendered input line** shows a path inside the durable image store
(`…/goa/images/goa-image-<n>.png`) *and* that the stored file is that image (its
dimensions are re-read from the stored PNG's IHDR, so an empty or stale file
fails).

```bash
E2E_ROOT=/tmp/my-run e2e/clipimg.sh
```

It **owns the OS clipboard** (it replaces its contents) and records `SKIP` where
no clipboard tool exists (needs `osascript` on macOS, `wl-copy` or `xclip` on
Linux). It is not part of `run_all.sh`: it needs no LM Studio, but it does need a
real desktop clipboard.

`CLIP_KEEP=1 e2e/clipimg.sh` validates whatever is **already** on the clipboard
instead of writing the synthetic PNG — used to check a clipboard a real browser
copied an image into (the script then asserts the path and that the file exists,
since it does not know the source dimensions). Note for that path: agent-browser
launches Chrome `--headless=new`, where `navigator.clipboard.write` resolves but
never reaches the OS pasteboard, so the copy has to be made `--headed`.

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
   `--wait-output <regex>` waits on the TUI's **own rendered output**
   (ANSI-stripped, whole session) instead — use it when the evidence *is* the
   screen (hotkeys that insert text, e.g. `clipimg.sh`), not a file.
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
