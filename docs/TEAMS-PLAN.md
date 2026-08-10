<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Teams of Agents — MICROSTEP execution plan

Branch: **`feature/team`** (created; spec committed as `78cc5af`). Spec:
`docs/TEAMS.md` (§ references below). Follow steps **in order**; do not
redesign — open questions (TEAMS.md §14) are resolved before or during their
phase and recorded here.

## Confirmed decisions

- **/config full CRUD is in scope for v1** (TEAMS.md §8.3): add/edit/remove
  teams and members via the config menu, mirroring
  `core/commands/config_orchestrator.go` (list → detail → field edit /
  `— add —` / remove-confirm / `saveHomeSection`).
- **Members map is canonical**; `main:`/`companion:` are shorthand normalized
  at load (TEAMS.md §3).
- **Per-member `thinking_level`** decouples reasoning effort from model
  (TEAMS.md §3.6): same model, different roles/budgets.
- **Backward compatibility**: no team active / no binding → byte-identical
  behavior; all existing companion/goal/orchestrator suites must pass
  unchanged.

## Gates (run each **separately**, no chaining — bugs.md guideline #6)

- `go vet ./...`
- `staticcheck ./...`
- `gocognit -over 15 .`
- `gocyclo -over 12 .`
- `go test -count=1 -race -cover ./...` (package timeouts per AGENTS.md)

Run all five after **every phase**; fix any new violation before proceeding.
Pre-existing warnings are acceptable only if unrelated and explicitly noted.

## References (read fully before coding)

- `config/config.go` (`OrchestratorConfig`, `MultiAgentConfig`,
  `ThinkingLevelConfig`, `GetThinkingLevel`), `config/defaults.go`,
  `config/config_merge.go`, `config/config_validate.go`.
- `core/commands/config_orchestrator.go` (the CRUD menu pattern to mirror),
  `core/commands/config.go` (menu registry), `core/commands/companion.go`.
- `internal/app/subsystems.go` (`configureRoleModels`, pool wiring,
  `ModelFactory`/`ProviderModelFactory`), `core/companion.go`,
  `core/sessionstate.go`, `core/goal_driver.go`, `core/goal/model.go`,
  `core/goal/store.go`, `core/goal_queue.go`.
- `multiagent/foreground_orchestrator.go` (`WorkflowCompanionMinor`,
  `AfterMainTurn`), `multiagent/agent_pool.go` (`SetConfig`, `ReasoningEffort`),
  `multiagent/agent_tool.go`, `multiagent/agent_driven_tools.go`.
- `core/orchestrator/runtime.go` (synthesis point, role fill-in),
  `core/commands/orchestrator*.go`.
- TUI: `tui/footer_data.go`, `internal/app/fork_filmstrip_test.go` and
  `internal/app/goal_tool_filmstrip_test.go` (`newUIScenario`,
  `sc.engine.AgentFrame()`, `film.Capture`, `waitForFrame`).

---

# Phase 1 — Config schema (TEAMS.md §3)

1. `config/config.go`: add `Teams TeamsConfig \`yaml:"teams,omitempty"\`` with
   `TeamsConfig{Active string, Definitions map[string]TeamDefinition}`,
   `TeamDefinition{Description, Main *TeamMember, Companion *TeamMember,
   Members map[string]TeamMember, Review string, ReviewGates
   TeamReviewGates{Triggers []string, Quorum string}, Delegation string,
   Defaults TeamDefaults{Autonomy string, TurnBudget int}}`,
   `TeamMember{Model, Provider, Mode, ThinkingLevel, Role string}`.
2. `Normalize() ([]ResolvedMember, error)` on `TeamDefinition`: expand
   shorthand → members list with role tags; mixing forms → error (§3 rule 9).
3. `config/defaults.go`: embedded defaults — `teams` absent (opt-in);
   `review` default `agent`, `quorum` default `all`, `delegation` default
   `agent`.
4. `config/config_merge.go`: deep-merge `definitions` maps per cascade rules;
   scalars last-write-wins; trigger slices replaced.
5. `config/config_validate.go`: enforce §3.5 rules 1–10 (name charset,
   exactly-one-main, model/provider/mode resolution against configured
   models/providers + `ModeRegistry`, review/quorum/delegation enums,
   gated-needs-triggers, thinking-level enum, both-forms rejection).
6. `core/commands/config_completion.go`: completion entries for
   `teams.active`, `teams.definitions.*` (structure-level; model/mode/trigger
   enums where enumerable).
7. Tests `config/teams_test.go`: table-driven — parse/normalize/merge/
   validate; every §3.5 rule has a failing case; shorthand ↔ members
   equivalence; `thinking_level` validation; legacy configs without `teams`
   load unchanged.

# Phase 2 — TeamManager (TEAMS.md §4)

8. `core/team/manager.go` + `core/team.go`: `TeamManager` per TEAMS.md §4.1
   (registry, `Active`, `Activate`, `Deactivate`, `Resolve`, drift state).
   Dependencies injected via narrow interfaces (model switcher, pool
   configurer, companion-mode setter, autonomy setter) — SOLID; the manager
   holds no agents.
9. Activation per §4.2 steps 1–6: normalize → resolve models
   (`ResolveModelByID`/`ResolveModelForProvider`) → snapshot (model, mode,
   thinking level, companion workflow mode, autonomy, **all pool role configs
   the team touches**) → apply main (`/model`-path, `/mode`-path,
   `/thinking`-path) → apply members (`pool.SetConfig("companion", …)` for
   first reviewer + `pool.SetConfig(memberName, …)` for all, with
   `ReasoningEffort` from §3.6) → apply review policy (existing
   `/companion:agent|framework|off` code paths; `gated` wired in Phase 5) →
   record + emit `team.activated`.
10. `Deactivate`: restore snapshot; emit `team.deactivated`. Re-activation =
    deactivate-then-activate against the pre-team baseline (no leaks).
11. Drift: post-activation manual `/model`, `/mode`, `/thinking`,
    `/companion:*` mark drift (footer `team*`); `/team:sync` re-applies;
    `/team:off` restores baseline.
12. Session persistence: active team name in `core/sessionstate.go`; resume
    re-applies (missing model/team → loud fallback to no-team + warning).
13. Wire into `internal/app/subsystems.go` (construct after pool +
    foreground orchestrator; expose on `core.Context`).
14. Tests `core/team/*_test.go` (-race): activation applies expected pool
    configs/model/thinking for pair + N-member teams; mid-activation failure
    restores snapshot; re-activation clean; drift on manual `/model`;
    resume round-trip; no-team session untouched.

# Phase 3 — Commands, footer, /config CRUD (TEAMS.md §8)

15. `core/commands/team.go` (self-registered `init()`): `/team`,
    `/team:list`, `/team:use:<name>`, `/team:off`, `/team:sync`,
    `/team:show:<name>` + completions from the merged registry.
16. Footer: `tui/footer_data.go` team segment `⛃ <name> (review:<policy>)`
    + drift marker `*`; goal panel shows bound team when ≠ session team.
17. `core/commands/config_teams.go` — **full CRUD** per §8.3, mirroring
    `config_orchestrator.go`:
    - Root item in `subMenuHandlers()` + `openTeams()` (Active team selector
      + definitions list + `— add team —`).
    - `openTeamDetail(name)` — description, review policy, gated triggers +
      quorum (when gated), delegation (when workers exist), members list,
      defaults, remove-team (confirm; refused while `teams.active` or bound
      to a queued goal).
    - `openMemberDetail(team, member)` — model/provider/mode/thinking_level
      selectors, role selector (main promotion demotes previous main after
      confirmation), remove member (cannot remove the last main).
    - `addTeamWizard()` — name → main (model/mode/thinking) → review policy →
      reviewer(s) when policy ≠ off → optional workers → validate + save.
    - All mutations via `saveHomeSection([]string{"teams"}, cfg.Teams)` +
      §3.5 validation on save (invalid → error flash, no persist).
    - Editing the active team's definition marks drift (§8.3 live-effect).
18. `/config` tests (`config_menu_test.go` style): menu tree walk — add team,
    edit member thinking level, role promotion, remove refusal cases,
    persistence round-trip to a temp home config.
19. Filmstrip `internal/app/team_config_filmstrip_test.go`: drive `/config →
    Teams` in a `newUIScenario`; capture frames for list, add-wizard steps,
    member detail, save; assert added/removed lines per `FrameDiff`; footer
    segment appears after `/team:use`.

# Phase 4 — Goal binding (TEAMS.md §5)

20. Goal record + queue items gain `team` (event payload + `upcoming-goals.json`,
    versioned/backward compatible — old files load with empty team).
21. Goal tool `create` arg `team`; `/goal:new|next` `--team <name>` flags with
    completion; unknown team → model-actionable error listing defined teams.
22. Goal driver overlay (§5.2): on activation of a bound goal, apply team
    (per-goal snapshot); restore on complete/pause/block/cancel/postpone;
    missing team at promotion → created paused with reason; re-apply on
    resume; precedence goal > run > session.
23. `defaults.turn_budget` applied to team-bound goals (§5.4); explicit
    `set_budget` wins.
24. Tests: event-sourced round-trip (create → queue → promote → restart →
    binding intact); overlay apply/restore across every exit path; missing
    team → paused; run-managed goal with explicit `team` → rejected.

# Phase 5 — Gated review + quorum (TEAMS.md §3.2–3.3, §5.3, §6)

25. `multiagent.WorkflowCompanionGated` + `/companion` status rendering;
    `GateTriggers` bitmask + per-turn match set with the §6.2 hooks
    (`turn_end`/`goal_turn`, `file_commit` via tool events, `goal_complete`
    via driver interception, `run_complete` via runtime synthesis).
26. Generalize `AfterMainTurn` → `RunGatedReview(ctx, output, matched)`;
    today's `WorkflowCompanionMinor` ≡ gated{turn_end}.
27. Review round fan-out: all `role: reviewer` members in parallel (pool
    caps honored); quorum `all|any`; per-reviewer fail-open; all-error →
    pass + visible incident (§3.3).
28. `core/team/review.go`: `CompletionReviewer` (§6.3) wired into the goal
    done-gate before the challenge; FAIL rejects with all FAILing rationales;
    judge config unchanged and complementary.
29. Tests: scripted fake reviewer models — trigger matrix (one round per
    turn max); quorum all/any; error exclusion; all-error fail-open;
    `goal_complete` FAIL rejection; framework parity for single reviewer.

# Phase 6 — Worker delegation (TEAMS.md §3.4)

30. Surface worker members to `agent`/`delegate_to` tool descriptions at
    activation (`delegation: agent`); `off` hides them; member mode toolset
    and guards enforced like any sub-agent.
31. Tests: delegation to a worker member uses its model + mode toolset;
    `delegation: off` hides; pool caps respected.

# Phase 7 — Orchestrator binding (TEAMS.md §7)

32. `/orchestrate new --team <name>`; `RunStarted` payload gains `team`;
    member → role fill-in (main → `orchestrator` role; reviewers →
    `companion` + same-named; workers → same-named); explicit
    `orchestrator.roles.*` wins; run-managed goals inherit the run's team.
33. Synthesis review round before `RunFinished` for
    `framework|gated(run_complete)` (advisory, annotated).
34. Tests: run with team → correct models per role; explicit roles not
    overridden; synthesis review recorded; `--goal` + `--team` binding.

# Phase 8 — E2E validation (LM Studio, filmstrip, interactive shell)

## 8.1 Fixture

Local LM Studio endpoint with three loaded models (exact IDs as served by
LM Studio — record them in the test run log):

| Role in fixture | Model | Thinking |
|-----------------|-------|----------|
| Main | **qwen** (the Qwen instruct model served locally) | member override `medium`, later `high` |
| Reviewer | **gemma** (the Gemma instruct model served locally) | member override `low` |
| Worker / 2nd reviewer | **qwythos** | member default (inherit) |

Test project (temp dir, never the goa repo itself — local models are slow;
follow qa-e2e guidance: simple prompts, side-effect validation, generous
timeouts):

```yaml
# .goa/config.yaml of the fixture project
active_provider: lmstudio
providers:
  - id: lmstudio
    endpoint: http://localhost:1234/v1/chat/completions
    provider: lm-studio
    api: openai-completions
models:
  - id: qwen-local
    provider: lmstudio
    model: <served-qwen-id>
  - id: gemma-local
    provider: lmstudio
    model: <served-gemma-id>
  - id: qwythos-local
    provider: lmstudio
    model: <served-qwythos-id>
teams:
  definitions:
    qa-pair:                        # the 2-model shorthand case
      main:      { model: qwen-local,  thinking_level: medium }
      companion: { model: gemma-local, thinking_level: low }
      review: gated
      review_gates: { triggers: [goal_complete, file_commit] }
    full-crew:                      # the N-member case
      members:
        lead:     { model: qwen-local,    mode: coder,    role: main,     thinking_level: high }
        helper:   { model: qwythos-local, mode: planner }                # worker
        reviewer: { model: gemma-local,   mode: reviewer, role: reviewer, thinking_level: low }
      review: gated
      review_gates: { triggers: [goal_complete], quorum: all }
```

## 8.2 Headless scenarios (binary built from `feature/team`)

35. **T1 — activation + model/thinking switch**: in fixture dir, headless
    `/team:use:qa-pair` then status: output shows main=qwen, companion=gemma,
    `review:gated`; session state persists team name.
36. **T2 — goal binding + gated review**: `/goal:new:write hello.txt containing OK
    --team qa-pair` with `--yes`; validate side effects: `hello.txt` exists;
    goal-events show team binding; team-events show a `goal_complete` review
    round with a verdict; the companion traffic used the **gemma** model
    (assert via LM Studio server logs / provider session stats), qwen used
    for main turns.
37. **T3 — thinking-level assertion**: run T2 with LM Studio verbose logging;
    assert the request payload for main turns carries the `medium`→`high`
    reasoning mapping for the qwen model and the reviewer round carries the
    `low` mapping for gemma (assert request-level thinking params, not prose).
38. **T4 — N-member + quorum**: `/team:use:full-crew`, goal
    `refactor hello.txt to contain GOODBYE --team full-crew`; validate: one
    review round on completion, quorum `all` recorded, helper role
    registered (visible in `/team:show:full-crew`).
39. **T5 — missing team**: bind goal to a nonexistent team → created paused
    with `team "…" not defined` reason; `/goal:resume` works after fixing
    config.
40. **T6 — /config CRUD round-trip** (headless CLI surface where available,
    else menu-driven in 8.3): add a team via `/config → Teams → — add team —`
    equivalent, verify `~/.goa/config.yaml` (or fixture home) contains it;
    edit member thinking level; remove team; definitions file reflects each
    step.
41. **T7 — regression**: qa-e2e skill scenarios 1–6 pass unchanged with no
    `teams:` config present.

## 8.3 Filmstrip scenarios (`internal/app/team_*_filmstrip_test.go`, newUIScenario)

42. **F1 — activation chrome**: `/team:use:qa-pair` → footer segment appears
    (`⛃ qa-pair (review:gated)`); capture before/after frames; assert
    `FrameDiff.AddedLines` contains the segment; status announcements render.
43. **F2 — goal panel binding**: goal bound to `qa-pair` active → goal panel
    shows bound team; completion marker shows `team review: PASS/FAIL`.
44. **F3 — /config CRUD**: walk `/config → Teams` (list → add wizard frames →
    member detail → save); assert each menu renders (`frameContains`), the
    added team appears in the list frame, the footer marks drift after
    editing the active team.
45. **F4 — drift + sync**: manual `/model` after activation → footer shows
    `qa-pair*`; `/team:sync` → marker cleared.
46. **F5 — review transcript**: with `show_inter_agent_messages: true`, a
    framework/gated review round renders reviewer output labeled with the
    member name (gemma reviewer), visible in `AddedLines`.
47. **F6 — golden snapshot**: commit one representative filmstrip (activation
    → goal → review marker) as a golden file for review diffs, matching the
    orchestrator-tabs precedent (`specs/orchestrator-tabs-plan.md` §4.2).

## 8.4 Interactive-shell validation (bugs.md guideline #5)

48. Drive the real binary in the fixture project via the interactive shell:
    complete T2 flow (team use → goal → watch footer + review marker →
    completion card) and T4 flow; verify **actual terminal output**, not just
    logs. Record the session as a filmstrip artifact where the harness
    allows; otherwise transcript excerpts in the phase report.

## 8.5 Reporting

49. Produce `TEAMS-E2E-REPORT.md` in the phase-8 commit: per-scenario
    PASS/FAIL (T1–T7, F1–F6) with evidence (commands, outputs, LM Studio
    model/thinking assertions, filmstrip step references); any failure is
    fixed and the plan updated (bugs.md guidelines 1–3) before phase close.

# Phase 9 — Docs + gates + close

50. Update `docs/USER-GUIDE.md`, `docs/CONFIGURATION.md` (teams reference
    table), `docs/PROFILES.md` cross-link (modes as member modes),
    `docs/GOALS.md` (team binding), `docs/ORCHESTRATOR.md` (--team).
51. Update the qa-e2e skill with a teams scenario block (T2 condensed) so the
    regression detector covers team activation + review.
52. Run all five gates separately; run `-race` across new packages; confirm
    no goroutine leaks in review fan-out (R1 methodology).
53. Final commit(s) on `feature/team`; open review with TEAMS.md §14
    resolutions recorded in this plan.

## Risk notes (carry into execution)

- **Slow local models**: gemma/qwen/qwythos on LM Studio are slow beyond
  single-word generation — design every e2e prompt minimal; validate by side
  effects (files, events, request payloads), never by exit code alone; use
  generous timeouts and `--yes`.
- **Thinking mapping differs per provider**: assert thinking at the
  request-payload level via LM Studio logs; if lm-studio ignores reasoning
  params for a served model, T3 degrades to asserting goa *emits* the
  configured level (request body), not that the model honors it — record
  which in the report.
- **Snapshot discipline**: every team application path (session, goal
  overlay, run) must restore on all exits including panics/cancel — `defer`
  restore + `recover()` in wrappers, mirroring pool `Release` discipline.
- **Single-owner invariant**: review-round events and footer updates flow
  through the TUI commandLoop; no component mutation from goroutines.
- **Prompt cache stability**: team activation changes system prompts once at
  the boundary; never mutate member prompts mid-turn.
- **Pool role hygiene**: member names are pool role names — activation must
  snapshot and restore every touched role config, and the reserved `main`/
  `orchestrator` roles follow §7 mapping only inside runs.
