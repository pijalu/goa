# Bug and feature Tracking

## Guideline
1. Create a detailed fix plan for each bug - the plan must contain test approach and validation steps - execute the plan and validate the fix when all elements are in place.
2. Any issues found must be fixed and the fix plan must be updated accordingly.
3. Issues found during testing must be fixed and the fix plan must be updated accordingly.
4. Each bug should be moved to archive when tested and closed as the associated plan.
5. Use interactive shell to validate the output of the tool - you must verify the actual terminal output.
6. Check code quality with each tool run separately (do not chain them with `;` or `&&`):
   - `go vet ./...`
   - `staticcheck ./...`
   - `gocognit -over 15 .`
   - `gocyclo -over 12 .`
   - `go test -count=1 -race -cover ./...`
   Fix any new issues introduced by the change. Pre-existing warnings are acceptable only if they are unrelated to the change and explicitly noted.

At the end of the session - the bug list should be empty and this file should only contain the guidelines for bug reporting.
If new items are added, restart the process.

## Workflow for bugs
1. Reproduce the failure before editing — ideally a command or script that triggers it on demand.
2. State the observed failure exactly (command + output).
3. Localize to the smallest region — ideally the specific lines — before editing. Precise localization is the strongest predictor of a correct fix.
4. Change one hypothesis at a time.
5. Prefer the minimal fix over a broad refactor.
6. Verify against the original failing command before declaring done.
7. Run the code-quality checks from guideline #6 separately and confirm the fix does not introduce new violations.
8. Move the bug list to `docs/archive/bugs.<fixdate>.md` when all items are closed.

---

# E2E Feature Validation Series (LM Studio, 2026-07-30)

## Test Approach — how to run (reusable)

Full method: `e2e/README.md`. Plan: `.agents/PLAN-e2e-feature-validation.md`.

1. Prereqs: LM Studio at `http://localhost:1234/v1` with models
   `qwen/qwen3.5-9b` (orchestrator/main), `qwythos-9b-v2` (reviewer/companion),
   `google/gemma-4-e4b` (coder). `jq`, `python3`, Go.
2. Run everything (build, model warmup, T1–T4, summary):
   `e2e/run_all.sh` — artifacts land in `/tmp/goa-e2e/run-<ts>/`
   (`/tmp/goa-e2e/last` symlink). Expect 1h+ on local models.
3. Single scenario: `E2E_ROOT=/tmp/goa-e2e/tX bash e2e/tN_*.sh`.

Key techniques (all in `e2e/lib.sh`):
- **Fake projects**: per-scenario `/tmp/goa-e2e/*/proj-*` with project-level
  `.goa/config.yaml` pinning provider lmstudio + 3 models (thinking off),
  `.goa/state.json` seeds, and isolated `.goa/orchestrator|goals` state.
- **Seeded headless orchestration**: `goa --orchestrate <run-id>` only
  *resumes*; seeding `.goa/orchestrator/<run-id>/events.jsonl` with one
  `run_started` event makes the resume path drive a full run headless.
- **Seeded companion**: `.goa/state.json` with
  `minor_mode=companion, agent_driven_enabled=true` restores agent-driven
  companion at startup. Framework mode (`/companion:framework`) is
  in-memory-only → driven via `e2e/ptydrive` (Go PTY driver; waits on file
  conditions, not ANSI scraping).
- **Machine-checked validation**: `jq` over `events.jsonl` (role→model
  mapping, ≥2 distinct agents messaging, orchestrator `delegate` tool calls,
  `run_finished`), goal lifecycle over `.goa/goals/goal-events.jsonl`,
  companion over output + `.goa/state.json` `companion_history`, plus real
  task artifacts (file contents). Exit codes are advisory only.
- **Slow-model discipline**: warmup each model first (JIT load), timeouts
  10–25m, tiny prompts, thinking off. Slowness is expected, not failure.

## Test Results

| Scenario | Result | Evidence |
|---|---|---|
| T1 orchestration (qwen/qwythos/gemma) | PASS 9/9 (+F1 note) | `/tmp/goa-e2e/run-t1e/` (2026-07-31 rerun, same script revision + binary as T2–T4; confirms the lib.sh hermetic-config change doesn't regress orchestration) — orchestrator=qwen, reviewer=qwythos, coder=gemma all correct; 5 distinct agents; 4 `delegate` calls; `run_finished`; resume continued the same run (no fork); artifact `answer.txt=BLUE`. Original evidence: `/tmp/goa-e2e/run-t1d/` (2026-07-30, 10/10) |
| T2 companion (qwen+qwythos) | PASS 5/5 | `/tmp/goa-e2e/run-t2d/` (2026-07-31, fixed script + fresh binary; results.tsv records `T2 PASS agent-driven + framework companion verified`) — T2a: real `request_review` tool call by qwen; qwythos review generated and delivered in-session as `[Message from companion]` (sessions/*.jsonl); artifact `color.txt=GREEN`. T2b: framework companion reviewed the turn, visible in TUI stream; artifact `sky.txt=AZURE`. The 2026-07-30 T2a FAIL was a stale test binary (built 21:59, registration fix committed 22:29) — see F5 correction and archived Bug 7. First fresh-binary rerun: `/tmp/goa-e2e/run-t2c/` (same evidence; ran with pre-Bug-7 assertions) |
| T3 goals + companion | PASS 5/5 | `/tmp/goa-e2e/run-t3/` — artifact `done.txt=DONE`; goal "sparky.orca" lifecycle in goal-events.jsonl: `goal.create` → 3×`goal.update` → `status=complete` → `goal.clear`; 3 real `request_review` calls; qwythos review delivered in-session (`done.txt has been successfully created with the correct content`); exit 0 |
| T4 orchestration + goals + companion | PASS 9/9 | `/tmp/goa-e2e/run-t4/` (ptydrive TUI `/orchestrate:new`) — role→model all correct (orchestrator=qwen, reviewer=qwythos, coder=gemma); 4 distinct agents spoke; orchestrator issued `delegate` calls; `run_finished ok=true`; run bound to goal "fair.puma" (`run_started.payload.goal_id`, `managedBy=orchestrator`); goal lifecycle `create → 8×update → clear` (terminal); artifact `orbit.txt=ORBIT`. T4.10 observation: companion idle during orchestration (hooks main-agent turns, not orchestration agents) |

## Bugs Found & Fixed (this series)

All 7 bugs fixed, test-covered, validated by scenario reruns, and **archived
to `docs/archive/bugs.2026-07-31.md`** (2026-07-31): headless orchestrate
race/exit-0-swallow (1, 3), resume-fork (2), hub continuation amnesia (4),
pipeline role-order nondeterminism (5), agent-driven tool registration (6),
e2e assertion bugs (7).

## Findings / Observations (not fixed this session)

- **F1 (gap)**: headless `--orchestrate` runs bind no goal — the goal binder
  is only wired in the TUI `/orchestrate:new` path
  (`core/commands/orchestrate.go:558 rt.SetGoalBinder`). Headless
  orchestration is therefore goal-less; T4 exercises goal binding via the
  TUI path. *Fix plan*: in `startOrchestrate`, when a goal manager is
  available, create + bind a goal (`NewGoalBinder`) with
  `ManagedBy: orchestrator`, and assert `run_started.payload.goal_id` in e2e.
- **F3 (model behavior)**: qwen3.5-9b (thinking off) as hub orchestrator has
  single-delegation bias: given a soft objective it delegates once (coder) and
  closes out. With the Bug 4 fix + explicit multi-step instructions it
  completes the full coder→reviewer chain. Consider a stronger nudge in
  `hub_orchestrator.md` ("do not finalize while required sub-tasks remain").
- **F4 (environment note)**: local model throughput on this machine:
    qwen ~1.4 tok/s, qwythos ~1.7, gemma ~3.3; ~5.4K-token Goa system prompt;
    a 3-agent hub run (2 delegations) ≈ 3–4 min end to end. First call per
    model pays JIT load (warm up first). LM Studio serializes GPU work, so
    interleaved multi-model runs queue rather than parallelize.
  - **F5 (config override — home config) — RESOLVED 2026-07-31 (was: stale binary + wrong mechanism analysis)**: the user's home config
    (`~/.goa/config.yaml` lines 271/275) has `delegate_to: false` and
    `request_review: false` (serialized from pre-Bug-6 defaults), which wins
    the cascade over the embedded defaults (`cfg.Tools.Enabled.*` = false in
    e2e projects — probe confirms). **Correction of the earlier analysis**:
    the claimed mechanism ("config booleans set the tools' instance-level
    `Enabled` flag via `restoreSessionState`") does NOT exist in the code —
    `Enabled` is driven solely by the `AgentDrivenGate` change callback, and
    companion intent (`companionActive` from the seeded state) forces BOTH
    registration (`registerAgentDrivenTools`, subsystems.go:938/943) and
    execution-enablement (`SetMinorMode` → gate fires → `Enabled=true`).
    Probe evidence (e2e/probe extended with
    `ProbeAgentDrivenToolState`): with home config forcing
    `Tools.Enabled.*=false`, a seeded-companion project still yields
    `request_review: registered=true enabled=true` (same for delegate_to).
    The 2026-07-30 T2a FAIL was a **stale test binary**: `/tmp/goa-e2e/goa`
    was built 21:59, the registration fix committed 22:29; the rerun log
    shows the pre-fix symptom (model: "I don't see a request_review tool").
    Rerun with a fresh binary (run-t2c): model calls `request_review`,
    qwythos review delivered → T2a PASS. **Residual hermeticity fix**:
    e2e `lib.sh write_base_config` now emits
    `tools.enabled.request_review: true, delegate_to: true` so fake projects
    never silently inherit a developer's home config (portability).
  - **F6 (observation)**: agent-driven `request_review` reviews are
    delivered in-session (`[Message from companion]` user message,
    multiagent/agent_driven_tools.go:185) but are NOT rendered in headless
    `--plain` output and do NOT persist `companion_history` in state.json
    (headless). Evidence lives in `.goa/sessions/*.jsonl` — e2e asserts it
    there. Also: per-agent model identity in the companion path is not
    separately logged (unlike orchestrator `agent_started.model`); the
    companion model is pinned by `multi_agent.companion_model` config only.

## Remaining Work (for next session)

1. ~~**Fix T2a**~~ — DONE 2026-07-31: root cause was a stale test binary
   (built before the Bug 6 registration fix was committed); companion intent
   already overrides the home-config `tools.enabled` false values for both
   registration and execution (probe-verified). lib.sh additionally made
   hermetic (emits `tools.enabled.*: true`).
2. ~~**Run T2**~~ — DONE 2026-07-31: run-t2d PASS 5/5 (fresh binary +
   corrected T2a.3 evidence assertion, archived Bug 7; results.tsv records
   PASS). run-t2c was the first fresh-binary rerun (pre-Bug-7 assertions).
3. ~~**Run T3**~~ — DONE 2026-07-31: run-t3 PASS 5/5 (goal lifecycle
   create→update→complete→clear, companion review delivered in-session,
   artifact DONE).
4. ~~**Run T4**~~ — DONE 2026-07-31: run-t4 PASS 9/9 (ptydrive TUI path;
   role→model correct, run bound to goal "fair.puma", goal terminal,
   companion idle-but-coexisting, artifact ORBIT).
5. **Update bugs.md** — DONE 2026-07-31 (all four scenarios recorded with
   evidence paths).
6. ~~**T1 confirmation rerun**~~ — DONE 2026-07-31: run-t1e PASS 9/9 (+F1
   note); all four scenarios now validated against the same script revision
   and binary. (A full `run_all.sh` clean series remains optional.)
7. ~~**Archive**~~ — DONE 2026-07-31: closed e2e-series bugs moved to
   `docs/archive/bugs.2026-07-31.md`; approach/results/findings remain here.
8. **Open bug (separate)** — goal tool result line should render the goal
   short name, not the full objective (see "Other issues" →
   "Goal tool result line floods the timeline…").

---
# Other issues



## Option: move the busy spinner from in-chat line to the status bar — OPEN

- **Request**: add an option to switch the in-chat spinner line
  ("⬣ Sending request...") to a simple animated spinner in the status bar
  (footer), next to the model, e.g.:
  ```
  ⬣ (kimi-code) k3-256k • high • [7%|10%]
  ```
  The animation must use the user's selected spinner style
  (`internal/spinner/spinners.json` + spinner selection), just rendered in
  the footer instead of the chat timeline. Benefits: chat timeline stays
  clean (no transient spinner lines in scrollback/export), busy state is
  visible at a fixed location.
- **Localization**:
  - In-chat spinner: `internal/app/stats.go` (`a.subs.statusMsg.Show("Sending
    request...")`, label at ~line 680), `internal/app/submithandler.go:456`,
    `internal/app/toolcall_footer.go:81` — all drive `subs.statusMsg`.
  - Footer/status bar: `tui/footer.go` (`Footer`, `FooterData` with Model,
    Provider, ThinkingLevel, context %, `SetModelBusy`) — a spinner frame
    field would be added to `FooterData` and rendered left of the provider/
    model block.
  - Spinner styles/selection: `internal/spinner/spinner.go` +
    `spinners.json` (frame sets already user-selectable).
  - Config surface: new setting e.g. `tui.spinner_location: chat|statusbar`
    (default `chat` = current behavior), exposed via `/config` like the other
    TUI options.
- **Fix plan**:
  1. Config: add `tui.spinner_location` (enum chat|statusbar, default chat),
     merged/cascaded like other settings + `/config` entry.
  2. Footer: add `BusySpinner string` (current frame) to `FooterData`; render
     it as an animated prefix next to the provider/model when busy; frame
     advances on the existing spinner tick (reuse the statusMsg tick source
     so both paths share timing).
  3. App: when `statusbar` mode, suppress the in-chat `statusMsg.Show` busy
     line and instead push spinner frames to the footer; keep in-chat for
     non-busy status messages (errors, infos) unchanged.
  4. Keep behavior identical for `chat` mode (default) — no visual change.
- **Tests**:
  - `tui/footer_test.go`: busy + spinner frame set → footer line contains the
    frame next to the model; not busy → no frame.
  - Filmstrip test (tui-test skill pattern): drive request events in both
    modes — `statusbar` mode shows no "Sending request..." chat line, footer
    carries the spinner; `chat` mode unchanged.
  - Config: default = chat; `/config` set/get round-trip for
    `tui.spinner_location`.
- **Validation**: `go test ./tui/... ./internal/app/... -race`; then live TUI
  session in both modes, watching the real terminal (guideline #5): in
  statusbar mode the footer animates "⬣ (provider) model • …" while the chat
  shows no spinner line; switching mode via /config takes effect on the next
  request.

## TUI shows unexpected repetition on normal (non-thinking) messages — OPEN

- **Observed**: assistant message text renders with near-identical sentences
  repeated back to back, with small casing/punctuation variations:
  ```
  ... Let me search for all callers of checkConstraints.isIgnoreableConflict is never called — the error comes from a different path.
  Let me find all callers of checkConstraints:isIgnoreableConflict is NEVER called! The error must come from a different path. Let me find all
  callers of checkConstraints:isIgnoreableConflict is NEVER CALLED. The error must come from a different path. Let me find all callers of
  checkConstraints:isIgnoreableConflict is NEVER called — ... (repeats ~7× with casing variations)
  ```
  Two candidate root causes, must be distinguished before fixing:
  (a) TUI/stream-accumulation bug: stream deltas are appended twice or a
      re-render overlaps prior text (model output is actually clean).
  (b) Genuine model loop with per-copy variations that the stream-loop
      detector misses (see the "Stream-loop detector false positive" entry —
      the detector rework must ALSO still catch this true-positive shape).
- **Localization pointers**:
  - Stream delta → chat path: `internal/app/stats.go` (delta handling),
    `tui/` chat viewport append logic; note `tui/user_message_double_draw_test.go`
    exists — double-draw bugs have happened in this area before.
  - Detector side: `internal/agentic/agent_streaming.go` `streamLoopScan`
    (the repeated unit above is ~120 bytes with per-copy casing edits).
- **Investigation plan**:
  1. Review the streaming TUI code end to end (delta accumulation, buffer
     flush on tool-call boundaries, viewport append vs. re-render) for a
     duplication path.
  2. If the root cause cannot be found by review: add a **stream capture**
     option (command line, e.g. `goa --capture-stream <file>`) that records
     the exact inbound provider stream (raw deltas with event sequence) to a
     log file, plus a replay path (e.g. `--replay-stream <file>` feeding the
     recorded deltas through the TUI headlessly) so the exact flow can be
     replayed and bisected deterministically.
- **Tests**: once root cause is known — regression test at the failing layer
  (filmstrip test for TUI duplication; streamloop test if detector-side).
- **Validation**: reproduce with the same prompt class on LM Studio; captured
  stream replay shows identical rendering; fix removes duplication (guideline
  #5 — verify real terminal output).



