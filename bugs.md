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
| T1 orchestration (qwen/qwythos/gemma) | PASS 10/10 | `/tmp/goa-e2e/run-t1d/` — qwen issued 2 `delegate` calls (coder, reviewer); qwythos independently verified file content; `run_finished ok=true`; artifact `answer.txt=BLUE` |
| T2 companion (qwen+qwythos) | T2a FAIL (2), T2b PASS | `/tmp/goa-e2e/run-t2b/` — T2a: file created but `request_review` tool not invoked by model — the home config (`~/.goa/config.yaml`) has `request_review: false, delegate_to: false` which overrides the (correct) embedded defaults. Tools ARE registered (probe confirms 13 tools) but `cfg.Tools.Enabled` returns false, so the model's instance-level `Enabled` flag gates execution. T2b framework-driven: artifact created, companion visible in TUI stream |
| T3 goals + companion | _pending_ | |
| T4 orchestration + goals + companion | _pending_ | |

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
  - **F5 (config override — home config)**: the user's home config
    (`~/.goa/config.yaml`) has `delegate_to: false` and `request_review: false`,
    which overrides the (correct) embedded defaults (`request_review: true,
    delegate_to: true` from Bug 6 fix). While the tools ARE registered in the
    tool registry (probe confirms 13 tools including `request_review` and
    `delegate_to`), `cfg.Tools.Enabled.RequestReview` and
    `cfg.Tools.Enabled.DelegateTo` return `false`, and `registerAgentDrivenTools`
    uses these config booleans to set the tools' instance-level `Enabled` flag
    (via `restoreSessionState`). The runtime then gates execution of disabled
    tools with a clean "tool is disabled" rejection. This is why T2a.2 (real
    `request_review` tool call) and T2a.3 (companion review evidence) fail: the
    model can see the tool but cannot execute it. The test scripts in `lib.sh`
    (`write_base_config`) do not emit a `tools.enabled` block, so the cascade
    picks up the home config values. *Fix plan*: either (a) the e2e `lib.sh`
    `write_base_config` should emit `tools.enabled.request_review: true,
    tools.enabled.delegate_to: true` in its project config, or (b) the
    `registerAgentDrivenTools` function should use the companion-active intent
    (already present in the `companionActive` parameter) as the sole gate,
    ignoring the config booleans when companion mode is active.

## Remaining Work (for next session)

1. **Fix T2a** — address F5 (home config override): update `e2e/lib.sh`
   `write_base_config` to emit `tools.enabled.request_review: true` and
   `tools.enabled.delegate_to: true`, OR change
   `registerAgentDrivenTools` to gate on companion intent alone.
2. **Run T2** — re-run `e2e/t2_companion.sh` after fix; validate T2a
   assertions pass (request_review tool call + companion review evidence).
3. **Run T3** — goals + companion: `e2e/t3_goals_companion.sh`. Validate
   goal lifecycle (`goal-events.jsonl`) + companion coexistence + artifact.
4. **Run T4** — orchestration + goals + companion: `e2e/t4_all.sh`
   (ptydrive TUI). Validate role→model mapping, run_finished, goal binding
   (goal_id in run_started), companion history, artifact.
5. **Update bugs.md** — record T3/T4 results and any new issues found.
6. **Archive** — when all 4 scenarios pass, move bug list to
   `docs/archive/bugs.<date>.md`.

---
