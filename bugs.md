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
| T2 companion (qwen+qwythos) | PASS 5/5 | `/tmp/goa-e2e/run-t2c/` (fresh binary, 2026-07-31) — T2a: real `request_review` tool call by qwen; qwythos review generated and delivered in-session as `[Message from companion]: Review complete: color.txt created correctly…` (sessions/*.jsonl); artifact `color.txt=GREEN`. T2b: framework companion reviewed the turn, visible in TUI stream; artifact `sky.txt=AZURE`. The 2026-07-30 T2a FAIL was a stale test binary (built 21:59, registration fix committed 22:29) — see F5 correction and Bug 7 |
| T3 goals + companion | PASS 5/5 | `/tmp/goa-e2e/run-t3/` — artifact `done.txt=DONE`; goal "sparky.orca" lifecycle in goal-events.jsonl: `goal.create` → 3×`goal.update` → `status=complete` → `goal.clear`; 3 real `request_review` calls; qwythos review delivered in-session (`done.txt has been successfully created with the correct content`); exit 0 |
| T4 orchestration + goals + companion | PASS 9/9 | `/tmp/goa-e2e/run-t4/` (ptydrive TUI `/orchestrate:new`) — role→model all correct (orchestrator=qwen, reviewer=qwythos, coder=gemma); 4 distinct agents spoke; orchestrator issued `delegate` calls; `run_finished ok=true`; run bound to goal "fair.puma" (`run_started.payload.goal_id`, `managedBy=orchestrator`); goal lifecycle `create → 8×update → clear` (terminal); artifact `orbit.txt=ORBIT`. T4.10 observation: companion idle during orchestration (hooks main-agent turns, not orchestration agents) |

## Bugs Found & Fixed (this series)

### Bug 1 — headless `--orchestrate` exits instantly, killing the run (race) — FIXED
- **Repro**: seed a run (see approach), `goa --orchestrate <run-id> --yes --timeout 20m`.
  Process exited after **3ms** with `turns=0`; the run's `events.jsonl` stopped at
  seq 2; no error, exit 0.
- **Localization**: `internal/app/headless.go` `startWork` launched
  `go h.waitForOrch(ctx, dc)` *before* `startOrchestrate` installed the runtime
  (`orchActive.Set(rt)`). `waitForOrch` read `orchActive.Get()` → nil → closed
  `dc.done` → main returned → process exit killed the in-flight run.
- **Fix**: `startOrchestrate` now returns the runtime after installing it;
  `waitForOrch` takes the runtime explicitly and starts only after. Run errors
  are captured (`setOrchErr`) and mapped to new exit code
  `headlessExitOrchFailed` (6) instead of being swallowed by
  `go func() { _ = rt.Run(...) }()`.
- **Tests**: `TestHeadless_WaitForOrchWaitsForRuntime`,
  `TestHeadless_TerminalOrchestrateCode` (`internal/app/orchestrate_headless_test.go`).
- **Validation**: `go test ./internal/app -race` green; e2e T1 rerun drives a
  full 3-agent run (see Bug 2/4).

### Bug 2 — headless orchestrate resume forks the run instead of continuing it — FIXED
- **Repro**: same seeded run; after the (racy) start, a SECOND run dir
  `run-<new-id>/` appeared while the seeded run dir stayed unfinished forever.
- **Localization**: `headless.go startOrchestrate` built a runtime via
  `OrchestratorAdapter.NewRuntime`, which generates a **new** run-id + store,
  and never called `Runtime.Resume(store, snap)` (unlike the TUI
  `/orchestrate:resume` path `core/commands/orchestrate.go doResume`, which
  does). Consequences: forked run, original log orphaned, finished-role skip
  logic dead.
- **Fix**: `startOrchestrate` now mirrors `doResume`: resolves the run id,
  replays the run's own store (advancing its seq), and calls
  `rt.Resume(store, snap)` so the resumed run continues the same event log
  (same run-id, finished roles skipped).
- **Tests**: `TestHeadless_OrchestrateContinuesSameRun` — same dir (no fork),
  same run-id on all events, strictly increasing seq, `run_finished` appended.
- **Validation**: tests green; e2e T1.10 asserts a single run dir on disk.

### Bug 3 — orchestration run failure silently exits 0 — FIXED
- **Repro/observation**: `go func() { _ = rt.Run(ctx, objective) }()` discarded
  the terminal error; failures rendered nothing and exited 0.
- **Fix**: error captured in `HeadlessApp.orchErr`, reported at summary
  (`orchestration failed: ...`), exit code 6. (Same change set as Bug 1.)
- **Tests**: `TestHeadless_TerminalOrchestrateCode`.

### Bug 4 — hub orchestrator loses the objective between turns (continuation amnesia) — FIXED
- **Repro**: T1 hub run: qwen correctly delegated to coder on turn 1, then on
  the post-specialist turn replied *"I see there's no active goal currently.
  Could you please clarify..."* — the loop treated that as the final answer;
  the reviewer delegation never happened (2 independent runs).
- **Localization**: hub orchestrator agents are created **fresh every turn**
  (`runtimeAgentFactory.acquire` never pools role `orchestrator`), and the
  continuation prompt `buildSpecialistResultsPrompt()` contained only
  `"Specialist outputs:\n..."` — no objective, no tool guidance
  (turn 1 gets the full `hub_orchestrator.md`; later turns got neither).
- **Fix**: new `prompts/orchestrate/hub_continuation.md` template renders a
  self-contained continuation (tools recap + `{{.Objective}}` + specialist
  outputs + next-action rules) with an objective-carrying inline fallback.
  The literal `"Specialist outputs:"` marker is preserved (loop drivers and
  three existing tests branch on it — changing it caused an infinite
  delegate loop in `TestRuntime_HubConversationStyleRunsSynthesisEvenIfOrchestratorSpoke`).
  Note: `prompts/orchestrate/hub_synthesis.md` exists but was never wired
  (dead template; superseded by `hub_continuation.md`).
- **Tests**: `TestBuildSpecialistResultsPrompt_SelfContained`,
  `TestHubLoop_ContinuationTurnCarriesObjective`
  (`core/orchestrator/hub_continuation_test.go`).
- **Validation**: suite green under `-race`; e2e T1d — qwen issued both
  `delegate` calls (coder THEN reviewer), qwythos reviewer independently
  verified `answer.txt` content, run finished ok.

### Bug 6 — agent-driven companion tools never registered (request_review missing) — FIXED
- **Repro**: T2a — seeded agent-driven companion
  (`state.json: minor_mode=companion, agent_driven_enabled=true`) + headless
  prompt instructing a review. qwen created the file, then reported:
  *"I don't see a request_review tool in my available tools list"* and asked
  for clarification. No review ever ran. (The naive grep assertion initially
  false-passed on the model's prose — assertions now match only real
  `-- tool call request_review` / `-- companion start` log lines.)
- **Localization**: `internal/app/subsystems.go registerAgentDrivenTools`
  registers the tools only when `cfg.Tools.Enabled.RequestReview/DelegateTo`
  is true — and the embedded `config/configs/default.yaml` shipped
  `request_review: false, delegate_to: false`. Result: the tools were never in
  the registry, so neither `/companion:on` nor restored state could arm them;
  the `SetAgentDrivenChangeCallback` toggled `Enabled` on a nil/absent tool.
  The runtime `Enabled` flag already gates EXECUTION with a clean rejection,
  so config-gating REGISTRATION off by default breaks the whole feature.
- **Fix**: embedded defaults now `request_review: true, delegate_to: true`
  (always registered; companion mode gates execution). Also fixed stale
  rejection messages referencing removed commands (`/agent-driven:on`,
  `/workflows:run:*` → `/companion:on|framework`).
- **Tests**: `TestDefaultConfig_AgentDrivenToolsEnabled`
  (`config/defaults_test.go`) — embedded default must enable both;
  zero-Config expectations clarified in `config_test.go`; existing
  `multiagent` disabled-execution tests still pass.
- **Validation**: config+multiagent suites green; e2e T2a rerun (strict
  assertions) — see results table.
- **Note**: staticcheck reports pre-existing unused helpers in
  `multiagent/agent_tool_visibility_test.go` (unrelated to this change).

### Bug 5 — pipeline/fanout role order nondeterministic (map iteration) — FIXED
- **Localization**: `managedRoles()` ranged over the roles map → random order;
  `runPipeline` executes roles in that order → pipeline stages could run in
  any sequence (e.g. reviewer before coder) across runs.
- **Fix**: `sort.Strings` in `managedRoles()` → deterministic order.
- **Tests**: `TestManagedRoles_Sorted`.

### Bug 7 — e2e assertion bugs: nonexistent goal event types, wrong companion evidence location — FIXED (test infra)
- **T3.4/T4.8 asserted `grep '"goal\.(complete|clear|block)"'` over
  goal-events.jsonl — but `core/goal/model.go` defines only THREE event
  types: `goal.create`, `goal.update`, `goal.clear`. A healthy completion
  writes `{"type":"goal.update","status":"complete"}` → the grep fails on
  success. Fix: jq assertions for `goal.clear` OR `goal.update` carrying a
  terminal `status` (complete/blocked/paused) in `t3_goals_companion.sh`,
  `t4_all.sh`.
- **T2a.3 asserted companion evidence via `-- companion start` (a
  framework-driven TUI render marker) or `companion_history` in state.json —
  but the agent-driven path (`request_review` tool) delivers the review
  in-session as a `[Message from companion]` user message and persists no
  companion_history in headless. The review DID run and complete
  (`{"status":"review_complete"}` with non-empty output) while the assertion
  reported failure. Fix: primary evidence = `Message from companion` in
  `.goa/sessions/*.jsonl`; fallbacks kept (`review_complete` w/o
  "no review output", render marker, history). Same session-evidence check
  added to T3.5.
- **Validation**: updated assertions re-checked against the run-t2c
  artifacts (T2a.3 passes with real session evidence); scripts pass
  `bash -n`.

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
2. ~~**Run T2**~~ — DONE 2026-07-31: run-t2c PASS 5/5 (fresh binary +
   corrected T2a.3 evidence assertion, see Bug 7).
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
7. **Archive** — all 4 scenarios PASS; move the e2e-series bug list to
   `docs/archive/bugs.<date>.md` per guideline #4/#8.
8. **Open bug (separate)** — goal tool result line should render the goal
   short name, not the full objective (see "Other issues" →
   "Goal tool result line floods the timeline…").

---
# Other issues

## Dead loop
the model went into a dead loop but not loop guard did trigger:
```
Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the

╭──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╮
│ Error: context canceled                                                                                                                      │
╰──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
╭──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╮
│ Generation stopped by user.                                                                                                                  │
╰──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
*(none — all items fixed and archived to `docs/archive/bugs.2026-07-26.md`)*
```

## Goal tool result line floods the timeline with the full objective — should show the goal short name — OPEN

- **Observed**: every `goal` tool result carrying a goal snapshot
  (create / get / update_todo) renders the full objective, which floods the
  timeline and truncates mid-word:
  ```
  ✓ ◆ Updated todo t2 → done
  Goal active: Wire the go-lemon generated TCL parser into tcl2go as a drop-in replacement for the hand-written tcl.ParseCommands parser, complet
  ```
  Expected — the goal's short friendly name is used instead:
  ```
  ✓ ◆ Updated todo t2 → done
  goal active: honest.zebra
  ```
- **Localization**: `tui/goal/tool_renderers.go`:
  - `renderGoalSnapshotLine` (line ~222) builds
    `"Goal %s: %s · %d turns · %s tokens · %s"` from
    `goalSummaryJSON.Objective` — the raw, unbounded objective string.
  - `goalSummaryJSON` (line ~166) does NOT decode the snapshot's `"name"`
    field, even though the goal tool result carries it
    (e.g. `"name":"honest.zebra"`).
  - The prefer-name pattern already exists for queued goals:
    `upcomingGoalJSON.Name` + `goalLabel()` (line ~300) — reuse it.
- **Fix plan**: add `Name string \`json:"name"\`` to `goalSummaryJSON`; in
  `renderGoalSnapshotLine` render the short name when non-empty, falling back
  to a truncated objective (same rule as `goalLabel`). Keep the
  turns/tokens/elapsed/todos stats suffix (useful signal) — only the
  objective → short-name swap changes.
- **Tests**: table-driven cases in `tui/goal/tool_renderers_test.go`:
  snapshot with name → line shows `<status>: <name>` and NOT the objective;
  snapshot without name → truncated-objective fallback; stats suffix intact.
- **Validation**: `go test ./tui/goal -race`; then run a TUI session with a
  long-objective goal, update a todo, and verify the timeline shows the
  one-line short-name summary (guideline #5 — verify real terminal output).
