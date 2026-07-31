<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Bug/Feature Tracking — Archive 2026-07-31

E2E Feature Validation Series (LM Studio) — all bugs found and fixed during
the T1–T4 validation runs on fake projects under /tmp. Approach + results:
`bugs.md` (repo root), `e2e/README.md`, `.agents/PLAN-e2e-feature-validation.md`.
Every entry below was fixed, covered by tests, and validated by the e2e
scenario reruns (evidence paths in each entry and in bugs.md Test Results).

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
