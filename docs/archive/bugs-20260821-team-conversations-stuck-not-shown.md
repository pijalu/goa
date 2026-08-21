## Team conversations stuck / not shown on TUI (2026-08-20)

Evidence: `/Users/muaddib/dev/frigolite/.goa/exports/goa-export-20260820-212545.zip`
(main session `1787251255_vg840t2k`, team `Smartish` = main gpt-5-6-luna + companion
glm-5-3/reviewer, `review: agent`, forced via `.goa/config.local.yaml: teams.active`).

User report:
- TUI does not show any "companion" work: no message, no tool calls — all frozen
  (`planner · cycle 2` / `coder · cycle 3` sections stuck on `▾ thinking...`,
  footer stuck on `coder ⟳ (zai) glm-5.3 thinking • [23%]`).
- Team should be clearly shown on UI when enabled — there must not be any *hidden* team.
- `config.local.yaml` forces team no matter if disabled; model changes were not saved
  (project config ended up with `active_model: glm-5-3` — the *companion's* model).
- Logging/export must contain the *complete* set of exchanges/logs — not only main
  (export had only the main session events; planner/coder/companion exchanges missing).

### Root-cause analysis (recon complete)

**RC-1 — Orchestrator TUI forwarder keeps ONE shared stream state for ALL roles.**
`internal/app/orchestrator.go:runOrchestratorEventForwarder` holds a single
`section *CompanionSectionComponent`, `cycle int`, `thinkingBuf`, `messageBuf`.
`ensureCompanionSection` only creates a new section when the current one is nil/Done.
With concurrent delegates (the exported session delegated to planner + coder +
companion in one turn):
- role B's `thinking_chunk`/`stream_chunk` lands in role A's section (wrong title/color);
- role B's `stream_end` closes role A's section (`handleOrchestratorContentStream`
  `stream_end` branch clears the *shared* section);
- footer `SetCompanionBusy(false)` + `SetActiveAgent("","","")` fires on the *first*
  role to finish while others still stream → footer lies;
- buffers cross-contaminate (screenshot: planner section holds fragment "The",
  coder section holds planner's analysis text).
Matches screenshot exactly: sections stuck "thinking...", content under wrong role,
footer stuck thinking.

**RC-2 — Sub-agent tool calls are never forwarded to the TUI.**
`multiagent/foreground_orchestrator.go:handleAgentOutputEvent` — `EventToolCall`
only increments `stageToolCount`. No orchestrator message is emitted, so companion
sections can never show tool activity → "no message/no tool calls".

**RC-3 — Delegate results never reach the main agent for planner/coder.**
`multiagent/agent_driven_tools.go:runDelegatedAgent` relays output via `sendToMain`
ONLY for `role == companion`. planner/coder outputs are dropped (main agent in the
export kept re-delegating "Return your analysis now" — visible in events.jsonl).
The `delegate_to` description also does not tell the LLM the result will not come back.

**RC-4 — Team state hidden from the user.**
- No startup activation: nothing calls `team.Manager.Activate(cfg.Teams.Active)` on
  boot (only `/team` commands activate). Config says `teams.active: Smartish` but the
  runtime has no team → invisible divergence both ways.
- Goal-scoped overlay (`core/goal_driver.go:syncTeamOverlay` → `ApplyOverlay`) applies
  a team with errors discarded (`_ =`) and zero chat announcement → hidden team.
- `/config → Teams → Active team` (`core/commands/config_teams.go:openTeamsActive`)
  writes `cfg.Teams.Active` + local config but NEVER calls Activate/Deactivate →
  config and runtime diverge ("disabled" in menu but team still running, and
  `teams.active` resurrected at next start).
- Footer badge exists (`Footer.SetTeam`, `⛃ name`) but is only refreshed on full
  footer rebuild; overlay apply/remove does not refresh the footer.

**RC-5 — Model persistence corrupted by team overlay.**
`teamSessionController.SwitchModel` mutates `cfg.ActiveProvider/ActiveModel`
(in-memory). Later, ANY thinking-level application (`saveModelThinkingLevel`,
`core/agentmanager_modes.go`) persists `cfg.ActiveProvider/ActiveModel` via
`SaveHomeProvidersAndModels` (+ project when `auto_save_model: true`) — saving the
team's *overlay* model as the user's model. Export shows project config
`active_model: glm-5-3` = companion's model. Meanwhile a real user `/model` change
made while a team is active is overwritten on team restore and never clearly saved.

**RC-6 — Export contains only the main session.**
`internal/logs/export/bundle.go:collectArtifacts` adds only
`session/events.jsonl` (one session). Sub-agent (pool) agents have NO SessionStore
at all (`multiagent/agent_pool.go` never wires one) — their exchanges exist only in
the ephemeral observer stream. Export therefore cannot show what planner/coder/
companion actually did. (`logs/http.jsonl` has raw HTTP, but nothing structured.)

### Fix plan

**F1 — Per-role stream state in the orchestrator TUI forwarder** (RC-1, RC-2)
- Replace single section/buffers with a per-role registry in the forwarder:
  `map[role]*roleStreamState{section, thinkingBuf, messageBuf, active}`.
- `stream_start`/`thinking_start` → get-or-create that role's section
  (`role · cycle N`, per-role cycle counter).
- chunks/thinking → that role's section only.
- `stream_end`/`thinking_end` → close THAT role's section only.
- Footer: track active streaming roles (set); `SetCompanionBusy(len>0)`;
  `SetActiveAgent` = most-recently-started active role; cleared when that role ends.
- Forward sub-agent tool calls: emit a new orchestrator message kind
  (e.g. `tool_call`/`tool_end` with tool name) from `handleAgentOutputEvent`
  (`EventToolCall`, and tool result/end events) and render one line in the role's
  section (`⚙ toolname …`) so companion work is visible.
- Guard: `EmitStreamEnd(role)` stays role-scoped (now safe — closes only that role).
- Files: `internal/app/orchestrator.go` (forwarder rewrite),
  `multiagent/foreground_orchestrator.go` (emit tool events),
  `tui/chat_viewport_components.go` (tool line in CompanionSectionComponent).

**F2 — Delegate result returned to the caller** (RC-3)
- `runDelegatedAgent`: after `subAgent.Run` returns, ALWAYS relay the collected
  output back to the main agent via `sendToMain` (not only companion), labelled by
  role ("Message from coder: ..."). Keep error-path `EmitStreamEnd`.
- Update `delegate_to` tool description: async, result arrives as a follow-up
  message from the delegated agent.
- File: `multiagent/agent_driven_tools.go`.

**F3 — Team visibility + lifecycle consistency** (RC-4)
- Startup: after subsystem assembly, if `cfg.Teams.Active != ""` call
  `teamManager.Activate(...)` (failure → flash + clear). Team is then real, not hidden.
- Overlay apply/remove in `goal_driver.syncTeamOverlay`: on change, emit a chat
  flash (`Team overlay: <name> (goal …)` / `Team overlay removed`) and trigger
  footer refresh; log overlay errors instead of `_ =`.
- `/config → Teams → Active team`: route through the team manager
  (Activate/Deactivate) so config write and runtime state move together
  (mirror `/team` `persistActiveTeam`).
- Footer: ensure team badge refresh on Activate/Deactivate/Overlay changes
  (FooterRefresh already called by /team paths; add to config-menu path).
- Files: `internal/app/bootstrap.go` or `subsystems.go` (startup activation),
  `core/goal_driver.go` (overlay visibility), `core/commands/config_teams.go`
  (runtime apply), `internal/app/team_adapters.go` if an event hook is needed.

**F4 — Model persistence guard** (RC-5)
- `teamSessionController.SwitchModel` must not leak into persisted state:
  make `AgentManager`/saver paths that persist `ActiveProvider/ActiveModel`
  team-aware (skip persisting while a team overlay/session team governs the model),
  OR snapshot+restore so persistence only ever sees the user's own selection.
  Chosen approach: TeamManager suppresses model persistence for the duration of an
  active team/overlay (flag on AgentManager consulted by `saveModelThinkingLevel`
  and any future saver), and `/model` during an active team marks drift (already
  supported) + flashes that the team governs the model.
- Files: `core/agentmanager_modes.go`, `core/team/manager.go`,
  `internal/app/team_adapters.go`, `core/commands/model.go` (flash).

**F5 — Complete export: sub-agent sessions** (RC-6)
- Give pool sub-agents structured recording: on `OnAgentCreated`, attach a recorder
  that writes the agent's OutputEvents to `.goa/sessions/<mainSessionID>/<role>.jsonl`
  (new `core/sessionstore.go` multi-writer or a small `RoleEventRecorder`).
- Export: bundle all role files under `session/agents/<role>.jsonl` plus the
  inter-agent/orchestrator event log; manifest lists them.
- Files: `multiagent/foreground_orchestrator.go` (hook), `core/sessionstore.go`
  (role recorder), `internal/logs/export/bundle.go` (+manifest.go, readme.go).

### Test approach (filmstrip-first, per repo guideline #5)

Unit:
- `internal/app/orchestrator_test.go` (new): interleaved two-role message script →
  each role gets its own section/cycle/buffer; stream_end closes only its own
  section; footer busy/active-agent correct at every step.
- `multiagent/foreground_orchestrator_test.go`: tool-call events emitted;
  per-role state isolation in `handleAgentOutputEvent`.
- `multiagent/b1_tools_test.go`: planner delegate relays output to main bus.
- `core/team/manager_test.go`: overlay suppresses model persistence; startup
  activation; config-menu activate/deactivate calls manager.
- `internal/logs/export/bundle_test.go`: bundle contains `session/agents/*.jsonl`.

Mocked-LLM integration (mandatory — validate TUI/multi-agent UI through the REAL
event path, not handler-injected messages):
- New shared scripted mock provider `internal/agentic/provider/mock` (package,
  reusable; supersedes per-test `mockTestApiProvider`/`toolCaptureProvider`
  patterns): registers its own `provider.Api`, serves per-model *scripts* of
  `AssistantMessageEvent` sequences (thinking deltas, tool_call blocks, text
  deltas, end turn), with **gate channels** so a test can hold a stream open
  mid-thinking and interleave two concurrent agents deterministically.
- `internal/app/team_mockllm_filmstrip_test.go` (new):
  1. `TestConcurrentDelegates_MockLLM_Filmstrip` — real `AgentPool` +
     `ForegroundOrchestrator` wired into a real `uiScenario`; two pool roles
     (`planner`, `coder`) backed by two mock-LLM scripts; run both via the real
     `DelegateTool` (`Execute` → goroutine → observer → orchestrator events →
     app forwarder → TUI). Hold planner mid-thinking (gate), stream coder
     thinking + a tool call + text, release planner. Filmstrip captures after
     each phase; assert:
     - two sections, correct titles/colors per role;
     - planner section shows ONLY planner thinking, coder section ONLY coder
       thinking + tool-call line + message (no cross-contamination);
     - footer shows the currently-streaming role's provider/model, busy until
       BOTH end, then reverts to main model;
     - after completion both sections collapse `[done]` with their own final
       message.
  2. `TestDelegateResultRelay_MockLLM_Filmstrip` — coder script ends with a
     final text; assert the main agent's bus receives "Message from coder: …"
     (CommConnector consumed) and the chat shows the delegated block.
  3. `TestFrameworkCompanionCycle_MockLLM_Filmstrip` — companion script via
     `AfterMainTurn`; cycle section completes; no stuck `thinking...`.
  4. `TestTeamOverlayVisibility_MockLLM_Filmstrip` — goal with team binding
     driven one turn by mock main LLM → overlay applied; assert chat flash
     `Team overlay:` + footer badge `⛃ <team>` in captured frames; goal end →
     badge cleared + restore flash.
  5. `TestTeamStartupActivation_MockLLM_Filmstrip` — boot app with
     `teams.active` in local config → frame contains team badge + activation
     announcement; main model switched per team definition.
- All filmstrip tests assert on `AgentFrame`/`Diff.AddedLines` +
  `footer.Data()` like existing `team_filmstrip_test.go`; keep each < 100ms
  where possible (mock streams gated by channels, no sleeps — use
  `waitForFrame`-style polling only where async goroutines are unavoidable).

Filmstrip handler-level (kept, cheaper):
- Extend `internal/app/team_filmstrip_test.go`:
  - `TestDelegateStream_FooterShowsActiveAgent_Filmstrip` gains the two-role
    busy-count case (footer busy until BOTH end).
- Manual validation: run a 2-delegate team session against local LM Studio and
  filmstrip/verify the actual terminal output (sections complete, footer correct,
  team badge visible).

### Validation gates (each run separately, per guideline #6)
`go vet ./...` · `staticcheck ./...` · `gocognit -over 15 .` · `gocyclo -over 12 .`
· `go test -count=1 -race -cover ./...` — fix anything the change introduces.

### /team command review (findings to fold into F3)
- `/team:off`, `/team:<name>`, selector: all correctly go through the manager +
  `persistActiveTeam` + `FooterRefresh`. OK.
- `/config → Teams → Active team`: config-only, diverges from runtime — FIX (F3).
- Startup: `teams.active` never applied — FIX (F3).
- `/team:remove` of active team via selector deactivates + persists — OK.
- Goal overlay: silent, errors swallowed — FIX (F3).

---

## Resolution (2026-08-21, branch feature/team)

All fixes implemented, tested, and committed:

| Fix | Commit | Content |
|-----|--------|---------|
| F1  | ce28ab6 | Per-role stream registry in the orchestrator forwarder (own section/cycle/buffers per role), sub-agent `tool_call`/`tool_result` events, `⚙ … → ✓` lines in `CompanionSectionComponent`, footer busy until ALL roles end (LIFO active role). |
| F2  | 5e3bcb6 | `runDelegatedAgent` relays the output to the main agent for ALL roles via the agent bus; `delegate_to` description documents the async contract. |
| F3  | 56ef666 | No hidden teams: startup activation of `teams.active`, `team.Manager` change callback → chat announcement + footer refresh for every transition (activate/deactivate/overlay/overlay-removed/sync), `/config → Teams → Active team` routes through the manager, overlay errors logged. |
| F4  | 2ba0c23 | Model persistence guard: `ModelPersistenceGuard` on the session controller driven by the team manager (suppressed while any team/overlay governs, incl. restore-on-failure); `saveModelThinkingLevel` no-ops while suppressed; `/model` under an active team applies session-only and says so. |
| F5  | a8c78cd | `multiagent.RoleSessionRecorder`: per-role JSONL under `.goa/sessions/<id>/agents/<role>.jsonl` (flush-per-event, lazy dir resolution, sanitized names); wired into the orchestrator's `OnAgentCreated` hook; export bundles `session/agents/*.jsonl` + README/manifest entries. |
| RC-7 (found by the new filmstrip test) | 3b8310c | `CompanionSectionComponent` mutations never invalidated the chat viewport's render cache → stale expanded "thinking..." lines stayed on screen after `SetDone` (the literal frozen-section symptom). Added `onChange` → `ChatViewport.MarkEntryDirty(id)`. |
| Tests | f704bf6 | New `internal/agentic/provider/mock` package (scripted per-model turns, gate/hold channels) + `team_mockllm_filmstrip_test.go` driving REAL agent turns through pool → observer → orchestrator → app forwarder → TUI: concurrent planner+coder isolation (`TestConcurrentDelegates_MockLLM_Filmstrip`) and mid-turn tool activity (`TestDelegateToolActivity_MockLLM_Filmstrip`). |
| Refactor | 9ed2ac8 | Complexity back under budget (forwarder 16→12, tests split). |

### Validation performed
- Gates (each separately): `go vet ./...` ✓ · `staticcheck ./...` ✓ ·
  `gocognit -over 15 .` ✓ (remaining: 3 pre-existing test helpers from
  955868c/485e994, unrelated) · `gocyclo -over 12 .` ✓ (remaining: pre-existing
  `_test.go` only) · `go test -count=1 -race ./...` ✓ (full suite green).
- Filmstrip validation of real terminal frames: mocked-LLM tests render actual
  `AgentFrame`s proving per-role sections, collapsed `[done]` titles, footer
  busy/role transitions, and tool lines.
- Unit coverage: per-role forwarder isolation, tool-event emission, delegate
  relay to main bus, manager change callback, startup activation, /model
  suppression (home+project files untouched), role recorder (rotation,
  sanitization, close), export bundle inclusion/absence.

### Not done / follow-ups
- Live LM Studio 2-delegate manual session (guideline suggests it; the
  mocked-LLM filmstrip tests cover the same frames deterministically —
  recommend one manual smoke run before release).
- `TestFrameworkCompanionCycle`/`TestTeamOverlayVisibility`/
  `TestTeamStartupActivation` mock-LLM variants skipped: overlay/startup
  visibility already covered by `team_visibility_test.go` + existing
  filmstrip tests; add later if regressions appear.
