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

# TODO

## BUG: Team/model activation binds a model whose temperature the endpoint rejects — hard 400 on first turn

**Status:** PARTIALLY FIXED — the safety-net (capability + actionable error) is implemented & tested. The PRIMARY root cause (team provider resolution) is a separate OPEN bug below; this entry stays open until that lands and the e2e validation passes.

**Log:** `/Users/muaddib/dev/localtest/.goa/exports/goa-export-20260812-121044.zip`

**Symptom:** After activating team `Local (kimi-code)` (main model
`google/gemma-4-e4b`), sending any prompt (e.g. "Create simple html
tic-tac-toe") fails immediately:

```
Error: 400 - invalid temperature: only 1 is allowed for this model
[error] The LLM request failed. LLM request failed (not retryable):
  {"error":{"message":"invalid temperature: only 1 is allowed for this model","type":"invalid_request_error"}}
```

The turn is dead — no retry, session stuck until the model/team is changed.

**Root cause — UPDATED after deeper investigation (the real bug is the team
provider-resolution bug, below):**
- `config/user.yaml`: model `google/gemma-4-e4b` sets `temperature: 0.2`
  **and `provider: lmstudio`**. The active team `Local` binds this model as
  its main member **without a `provider:` override** (`main: {model: ...}`).
- Team activation (`teamSessionController.SwitchModel`,
  `internal/app/team_adapters.go:28`) resolves an empty member provider by
  keeping the **current** `ActiveProvider` (`kimi-code`) instead of the
  model's **own configured provider** (`lmstudio`). So the session stays on
  the kimi-code endpoint while sending `model: google/gemma-4-e4b` and that
  model's `temperature: 0.2`.
- `logs/http.jsonl`: POST to `…/coding/v1/chat/completions` (kimi-code) for
  `google/gemma-4-e4b` returns **400** `invalid temperature: only 1 is
  allowed for this model`.
- So the 400 is a **symptom of the team sending the model to the wrong
  provider**. Two independent defects contribute:
  1. **Primary: team does not select the model's provider** (see the dedicated
     bug entry "Team activation does not switch to the member model's
     provider"). Fixing that routes gemma → lmstudio, where `temperature: 0.2`
     is valid, and the 400 never happens.
  2. **Secondary / safety net: no `supports_temperature` capability + opaque
     error.** Even on the wrong endpoint, Goa should not die on a fixed-temp
     rejection without guidance. This part is FIXED (see below).

**Why it's a Goa bug (not just user config):**
1. Goa lets a model be configured with a temperature its endpoint cannot
   accept, with no validation at add/edit/activation time.
2. The failure surfaces only at the first LLM turn as an opaque 400, after the
   team/model is already active — the user can't discover it earlier.
3. The error is marked "not retryable" but Goa offers no actionable guidance
   (e.g. "model X requires temperature=1; fix in /config → Models").

**Fix directions (choose in plan):**
- **A. Validate at config time:** when a model's endpoint/provider is known to
  constrain sampling params, reject or warn on an out-of-range `temperature`
  in `/config → Models` and on `/team` activation. Needs a per-provider /
  per-model capability descriptor (which params are allowed / forced).
- **B. Clamp/omit at request build:** if the provider declares `temperature`
  fixed (=1), omit the field or coerce to the allowed value and log a notice
  instead of erroring the whole turn.
- **C. Better error surfacing (minimum):** detect the
  `invalid temperature: only 1 is allowed` shape and render an actionable
  message naming the model + the exact setting to change, plus a `/config`
  pointer — instead of a raw 400.
- Likely correct: **B + C** (don't die; tell the user what was coerced), with
  **A** as the proper long-term guard once provider capabilities exist.

**Test approach:**
- Unit: request-builder test — a model with `temperature: 0.2` against a
  provider that only allows `1` must either send `1`/omit (with a logged
  notice) or produce a clear pre-flight error, never a dead 400 turn.
- Unit: config validation flags a disallowed temperature when the provider
  capability is known.
- Error-path: the `invalid temperature` 400 body maps to an actionable user
  message (assert the rendered text names the model + setting).
- E2E (interactive shell): configure a gemma-style model on a fixed-temp
  endpoint, activate via team, send a prompt → no dead turn; notice shown.

**Validation steps:**
- Reproduce against the kimi-code (or a stub fixed-temperature) endpoint.
- Gates (each run separately): `go vet ./...` · `staticcheck ./...` ·
  `gocognit -over 15 .` · `gocyclo -over 12 .` ·
  `go test -count=1 -race -cover ./...`.
- Confirm the real terminal output no longer shows the raw 400 box.

**Safety-net fix applied (B + C), on the ACTIVE protocol path:**
- `internal/agentic/provider/schema/variant.go`: added
  `CompatFlags.SupportsTemperature *bool` — the wire quirk as data, matching
  the variant-profile design.
- `internal/agentic/provider/schema/variants/kimi-code.json`:
  `"supports_temperature": false` — kimi-code rejects any temperature but its
  fixed default, so the field is now omitted.
- `internal/agentic/provider/protocol/openai_completions.go`: new
  `openAICompletionsCompat.SupportsTemperature` (default true, set from the
  profile); `buildOpenAIParams` omits `temperature` when it is false so the
  endpoint applies its own default instead of returning HTTP 400.
- `internal/agentic/retry_classify.go` (`actionableHint`): a
  fixed-temperature rejection ("invalid temperature … allowed") now renders an
  actionable hint ("remove the model's temperature setting (/config → Models)
  or set it to the allowed value") instead of a bare 400.
- Tests: `TestBuildOpenAIParams_OmitsTemperatureWhenUnsupported` /
  `..._SendsTemperatureWhenSupported` (unit, RED→GREEN),
  `TestKimiCodeProfileDisablesTemperature` (end-to-end: the real kimi-code
  profile resolves to SupportsTemperature=false and the gemma temperature is
  omitted), `TestFormatFatalStreamMessage_TemperatureHint` (actionable error).
- Gates green: `go vet ./internal/agentic/...` ✓ · `staticcheck` ✓ ·
  `gocognit -over 15` / `gocyclo -over 12` on changed files ✓ ·
  `go test -count=1 -race -cover ./internal/agentic/provider/protocol` ✓ (67.0%).
- **Remaining for full close:** fix the PRIMARY team-provider-resolution bug
  (next entry) + run the requested live e2e validation (last entry).

**Notes / open questions:**
- The temperature constraint is per-endpoint-model on kimi-code; the
  capability descriptor now lives in the variant profile (`supports_temperature`).
- The primary defect (team not selecting the model's provider) is tracked in
  the next bug entry.

---

## BUG: Team activation does not switch to the member model's provider — requests go to the wrong endpoint

**Status:** OPEN — root cause confirmed; this is the PRIMARY cause of the
temperature-400 bug above.

**Symptom:** Activating a team whose main (or companion) member names a model
that belongs to a *different* provider than the current session keeps the
session on the **current** provider and sends the member's model ID + settings
to that wrong endpoint. Example (from the export): session was on `kimi-code`,
team `Local` main = `google/gemma-4-e4b` (whose model config is
`provider: lmstudio`), yet the request went to
`https://api.kimi.com/coding/v1` with `model: google/gemma-4-e4b` → 400.

**Root cause (traced):** `teamSessionController.SwitchModel`
(`internal/app/team_adapters.go:28`):
```go
pid := providerID
if pid == "" {
    pid = c.cfg.ActiveProvider   // ← falls back to CURRENT provider
}
```
A team member with no explicit `provider:` (`main: {model: ...}`, the common
case) passes `providerID == ""`, so the controller reuses `ActiveProvider`
instead of the model's **own configured provider**. The `/model` command does
this correctly via `providerIDForModel` (`core/commands/model.go:564`), which
returns `ModelConfig.ProviderID`; the team path is missing the equivalent
lookup. Affects the main member (session model) and any pool member whose
config is resolved the same way.

**Why it's a Goa bug:** team activation must bind the member's model **on the
provider that model is configured for**. Reusing the session's current
provider silently mis-routes the model and its parameters.

**Fix plan:**
- In `teamSessionController.SwitchModel` (or the manager's
  `applyMainMemberLocked` / pool member config), when the member's `Provider`
  is empty, resolve it from the model's config entry
  (`ModelConfig.ProviderID` for `member.Model`), falling back to
  `ActiveProvider` only when the model names no provider. Mirror
  `providerIDForModel` semantics; keep the explicit-`provider:` override
  highest priority.
- Apply the same resolution to pool members (`teamMemberApplier.MemberConfig`
  sets `ProviderID: rm.Member.Provider`, which is "" in the common case) so
  companion/worker members also land on the right provider.
- Restore path: the snapshot/restore already records prior provider+model, so
  deactivation is unaffected.

**Test approach:**
- Unit (`internal/app`): a team whose main member has no `provider:` and whose
  model is configured with `provider: lmstudio`, activated while the session
  is on `kimi-code` → after activation `ActiveProvider == "lmstudio"` and the
  resolved model's BaseURL is the lmstudio endpoint (not kimi-code).
- Unit: an explicit member `provider:` still wins over the model's configured
  provider; a member model with no configured provider falls back to
  `ActiveProvider`.
- Unit (pool): `MemberConfig` for a companion with no `provider:` resolves the
  companion model's configured provider.
- Regression: the temperature-400 scenario — gemma (provider lmstudio) on a
  kimi-code session routes to lmstudio, where `temperature: 0.2` is accepted.

**Validation steps:**
- Gates (each run separately): `go vet ./...` · `staticcheck ./...` ·
  `gocognit -over 15 .` · `gocyclo -over 12 .` · `go test -count=1 -race -cover ./...`.
- Live e2e per the validation request entry below (gemma codes on lmstudio,
  qwen reviews).

---

## BUG: Activating a team persists `teams.active` to the HOME config instead of the project (local) config

**Status:** OPEN — logged, root cause identified.

**Symptom:** Selecting/activating a team (`/team:<name>` or /config → Teams →
Active team) writes `teams.active` to the **home** config
(`~/.goa/config.yaml`), so the team selection leaks across all projects
instead of being scoped to the current project.

**Root cause (traced):** `persistActiveTeam` (`core/commands/team.go:379`)
calls `ctx.ConfigSaver.SaveHomeFieldValue([]string{"teams","active"}, name)`
— explicitly the home field. The config cascade
(embedded → home → project `.goa/` → local `.goa/config.local.yaml`) means a
home-level `teams.active` applies to every project. The expected scope for a
per-project team binding is the project (`.goa/config.yaml`) or local
(`.goa/config.local.yaml`) layer. Note `/model` persists `active_model` to
home by design (a global default), but a team is a project-scoped working
set, so the same default is surprising.

**Open design question (decide in plan):** should `teams.active` persist to
the project `.goa/config.yaml` (shared, committed) or the local
`.goa/config.local.yaml` (gitignored, per-developer)? A team is arguably
per-project + per-developer; recommend the **local** layer
(`config.local.yaml`) so it neither leaks across projects nor dirties the
committed project config. Confirm the intended scope before implementing.

**Fix plan:**
- Add a saver method for the project/local layer (the cascade loader already
  supports layered writes for other fields) and point `persistActiveTeam` at
  the chosen layer instead of home.
- Keep `teams.active` resolution order unchanged (cascade already merges; the
  most specific layer wins).

**Test approach:**
- Unit: after `teamActivate`, the value is written to the local/project layer
  file and NOT to the home config (assert which file changed under a temp
  HOME + temp project dir).
- Unit: on startup the project/local `teams.active` resolves correctly through
  the cascade.

**Validation steps:**
- Interactive: in project A activate a team → `~/.goa/config.yaml` unchanged;
  the project's `.goa/config.local.yaml` (or `.goa/config.yaml`) carries
  `teams.active`; project B is unaffected.
- Gates (each run separately): `go vet ./...` · `staticcheck ./...` ·
  `gocognit -over 15 .` · `gocyclo -over 12 .` · `go test -count=1 -race -cover ./...`.

---

## VALIDATION REQUEST (e2e, live LM Studio): local team gemma+qwen writes tic-tac-toe

**Status:** REQUESTED — environment confirmed reachable; run after the
provider-resolution bug above is fixed.

**Environment (confirmed 2026-08-12):** LM Studio at `http://localhost:1234/v1`
serves `google/gemma-4-e4b` and `qwen/qwen3.5-9b` (both local).

**Setup:**
1. A local team on the `lmstudio` provider: main = `google/gemma-4-e4b`,
   companion = `qwen/qwen3.5-9b`, review = `framework` (default every-turn).
2. A temp project with that local team selected (persisted per the
   team-save-location decision above).

**Scenario:** ask Goa to "write a tic-tac-toe in HTML".

**Must validate:**
1. **gemma does the coding** — the main turn runs on `google/gemma-4-e4b` via
   the lmstudio endpoint (not any other provider).
2. **qwen does the review** — the framework review runs on `qwen/qwen3.5-9b`,
   and gemma actions the review feedback (review → act loop visible).
3. **TUI shows the active model correctly** — footer/dialog reflect gemma as
   main and qwen as companion (and transitions as each runs).
4. **TUI shows the inter-model dialog** — the review request/verdict and the
   follow-up edits render correctly in the transcript.

**Method:** use the interactive shell / PTY against the real binary (bugs.md
guideline #5) and/or the `qa-e2e` skill against the local LM; capture actual
terminal output (not just logs).

---

## BUG: Companion minor mode sticks after team use — footer shows `mode(companion)` and it is impossible to disable

**Status:** OPEN — logged, root cause identified, fix plan below.

**Symptom:** After using a team (with a reviewer) on a project, the footer
permanently shows companion state, e.g.:

```
~/dev/localtest coding-posture(companion) │ YOLO
(kimi-code) k3-256k • high | (zai) glm-5.2 (companion) • xhigh • [25%|63%]
```

The mode is annotated `(companion)` and a companion model line is rendered —
and it is **impossible to disable**: the companion indicator survives
`/team:off`, and returns on restart even after `/companion:off`.

**Root cause (traced through code):**
1. Team activation applies the review policy via
   `teamReviewController.ApplyReview` (`internal/app/team_adapters.go`). For
   `review: agent|framework|gated` it calls `am.SetAgentDrivenEnabled(true)`
   (+ orch mode + `InjectCompanionReview`).
2. `AgentManager.SetAgentDrivenEnabled(true)` (`core/agentmanager.go:1026`)
   calls `persistState()` — it **persists** `AgentDrivenEnabled: true` to the
   session state store. Merely activating a team therefore writes
   agent-driven=true to disk.
3. **Deactivation never resets it.** `/team:off` → `Manager.Deactivate` →
   `restoreReviewLocked` → `ApplyReview(ReviewApplyOff)` sets
   `orch.SetMode(WorkflowInactive)` + `InjectCompanionReview(false)` — but it
   does **not** call `SetAgentDrivenEnabled(false)`, does **not** clear
   `modeMgr.currentMinor`, and does **not** `emitMinorMode("")`. So the
   footer's `MinorMode` stays `"companion"` and `AgentDrivenEnabled` stays
   true (still persisted).
4. **Restart re-asserts it.** `restoreSessionState`
   (`internal/app/subsystems.go:1163`):
   `if snap.MinorMode == "companion" || snap.AgentDrivenEnabled { SetMinorMode("companion", true) }`.
   Because `AgentDrivenEnabled` was persisted true by team activation, every
   startup force-enables companion mode again — even if the user never asked
   for companion and even after turning the team off.
5. The footer only learns `MinorMode` via the `ev.MinorMode` event
   (`core/agentmanager.go:1170` `emitMinorMode`, consumed at
   `internal/app/events.go:336`). The team review-restore path never emits a
   clear event, so the footer is never told companion went away.

**Why it's a Goa bug:** team deactivation must restore the pre-team session
state (TEAMS.md §4.2 snapshot/restore contract) — including the companion /
agent-driven / minor-mode flags and the footer display. Today it restores
model/mode/thinking and the orchestrator review mode, but leaves the
agent-driven flag set and the minor-mode label stale, and persists that leak
to disk so it re-appears on every restart.

**Fix directions (plan):**
- **A. Snapshot + restore the minor-mode / agent-driven flag.** Extend
  `sessionSnapshot` (core/team/manager.go) to capture `AgentDrivenEnabled` and
  the current minor-mode label (and the footer-visible state). On restore
  (`restoreReviewLocked` / a new `restoreCompanionLocked`), call
  `SetAgentDrivenEnabled(prior)` and `SetMinorMode`/emit the prior label so
  both the agent state and the footer return to the pre-team value.
- **B. Make the team review controller own the full companion teardown.**
  `ApplyReview(ReviewApplyOff)` should also `SetAgentDrivenEnabled(false)` and
  emit the minor-mode clear, so any path that lands on "off" (including
  deactivation) fully disables companion. Careful: `ReviewApplyOff` is also
  used when a team legitimately has `review: off` — restoring must use the
  *snapshotted* prior state, not a blanket off (hence A).
- Correct approach: **A** (state-driven restore) is the source of truth; B is
  a consequence of A when the prior state was "off". Do not blanket-disable in
  the adapter or a user's pre-existing `/companion:on` would be lost on
  `/team:off`.
- **C. Restart guard:** `restoreSessionState` should only force companion from
  `snap.MinorMode == "companion"`, not from a bare `AgentDrivenEnabled` left
  over by a team (agent-driven tools being on is not the same as the companion
  *minor mode* being the user's intent). Reconsider the `|| snap.AgentDrivenEnabled`
  clause.

**Test approach:**
- Unit (core/team): snapshot/restore round-trip — activate a `review: agent`
  team over a session whose companion was OFF, assert deactivate returns
  `AgentDrivenEnabled()==false` and `MinorMode()==""`; and the inverse (prior
  `/companion:on` is preserved after `/team:off`).
- Unit (internal/app): `ApplyReview(ReviewApplyOff)` after a team apply leaves
  no residual agent-driven flag when the snapshotted prior state was off.
- Unit (subsystems): `restoreSessionState` with
  `{MinorMode:"", AgentDrivenEnabled:true}` must NOT force the companion
  minor-mode label (only enable agent-driven tools).
- Regression test would have caught this: footer `MinorMode` after team
  activate→deactivate returns to "".

**Validation steps:**
- Interactive: activate a reviewer team → `/team:off` → footer no longer shows
  `(companion)`; `/companion:off` sticks; restart → footer stays clean.
- Gates (each run separately): `go vet ./...` · `staticcheck ./...` ·
  `gocognit -over 15 .` · `gocyclo -over 12 .` · `go test -count=1 -race -cover ./...`.

**Fix applied:**
- `internal/app/team_adapters.go` (`teamReviewController.ApplyReview`): the
  `ReviewApplyOff` path now also calls `SetAgentDrivenEnabled(false)` before
  `InjectCompanionReview(false)`. Team deactivation / a `review: off` restore
  therefore fully tears down the agent-driven companion state (and, because
  `SetAgentDrivenEnabled` persists, stops writing the leftover
  `AgentDrivenEnabled:true` that re-asserted companion on restart).
- `internal/app/subsystems.go` (`restoreSessionState`): the startup guard now
  only forces the companion minor mode from an explicit
  `snap.MinorMode == "companion"`. A bare `snap.AgentDrivenEnabled` restores
  agent-driven *tool availability* (`SetAgentDrivenEnabled(true)`) without
  stamping the companion minor-mode label — agent-driven tools on ≠ companion
  minor mode.
- Tests (`internal/app/team_companion_teardown_test.go`):
  `TestTeamReviewController_OffDisablesAgentDriven` (RED: off left
  agent-driven=true), `TestRestoreSessionState_AgentDrivenAloneDoesNotForceCompanion`
  (RED: bare flag forced companion), `TestRestoreSessionState_CompanionMinorModeRestores`
  (guards the legit explicit-companion restore). All GREEN after the fix.
- Gates green: `go vet` ✓ · `staticcheck ./internal/app` ✓ · `gocognit -over 15` /
  `gocyclo -over 12` on changed files ✓ · `go test -count=1 -race -cover
  ./internal/app` ✓ (55.3%) and `./core/team` ✓.

**Note (footer label within a live session):** the footer only learns the
minor-mode label via `SetMinorMode` (emitted by `/companion:on|off` and the
startup restore), never by team apply. So a session that never ran
`/companion:on` no longer shows `(companion)` from team use, and the stale
label no longer survives a restart. Syncing the footer label live on team
activate/deactivate is a possible follow-up polish, not required for the
stuck-state fix.

---

## BUG: Config → Teams navigation never builds a history stack — ESC anywhere in Teams exits the whole menu to root

**Status:** FIXED — implemented, tested, validated.

**Symptom:** In `/config` → Teams, drilling into a team (detail view) or its
Description field and pressing ESC (or completing an edit and then navigating
back) drops the user **out of the config menu entirely** (back to the root
TUI), instead of returning to the Teams list / team detail. From the user's
perspective: "selecting description and enter returns to root config" — the
navigation stack is broken so any `back()` bails to root.

**Root cause (reproduced):** the config menu drives `back()` off a history
stack (`configMenu.open()` pushes, `back()` pops). But the entire Teams flow
never pushes onto it:
- `showSubMenu("teams")` calls `openTeams` **directly** (handler map in
  `core/commands/config.go:218`), NOT via `m.open(...)` — unlike every other
  submenu (`openModels`, `openTools`, `openSandbox`, … all wrap with
  `m.open(...)`). So the root page is never pushed.
- `openTeams` → selecting a team calls `m.openTeamDetail(name)` directly (sets
  `m.current` only); `openTeamDetail` → "description" calls
  `m.promptTeamField(...)` directly (doesn't even set `m.current`).
- Net effect: `len(m.history) == 0` for the whole Teams session (verified in a
  repro test). Any `m.back()` (ESC from detail, ESC from the description
  input) hits the empty-history branch → `m.current = nil` → the menu closes
  and the user is dumped to root instead of up one level.

Repro test (removed after use) showed: after root→teams→detail, ESC yields
`current=nil` (menu closed) where `Teams:` was expected — **RED confirmed**.
The same defect likely affects `openOrchestrator` and `openGoalsRetention`,
which also bypass `m.open(...)` (worth auditing in the same fix).

**Why it's a Goa bug:** every config submenu must integrate with the menu's
history/back navigation so ESC goes up one level. Teams (and any other
unwrapped submenu) breaks that contract.

**Fix directions (plan):**
- **A. Wrap the Teams entry in `m.open`.** Register the submenu handler so
  Teams pushes the root page: change `openTeams` wiring to `m.open(m.openTeams)`
  (or make the handler map store `func`s that already push, consistent with
  `openModels` et al.). This restores root→Teams back.
- **B. Push within the Teams tree.** Make `openTeams` open the detail via
  `m.open(func(){ m.openTeamDetail(name) })`, and `openTeamDetail` open
  sub-pages (description/review/gates/members/remove) via `m.open(...)` (or at
  least ensure each `back()` from them returns to the detail/Teams list).
  `promptTeamField` should return to `openTeamDetail` on both submit and
  cancel via the history stack rather than a hardcoded call.
- **C. Audit the other unwrapped submenus** (`openOrchestrator`,
  `openGoalsRetention`, and any handler not using `m.open`) and apply the same
  fix so ESC works uniformly.
- Correct minimal fix: **A + B** for Teams (the reported path); **C** as a
  follow-up sweep in the same commit since it is the same one-line pattern.

**Test approach:**
- Unit: after root→teams→detail, ESC (`onSel("", false)`) returns to
  `Teams:` (title), not `current=nil`; ESC from the Teams list returns to the
  root `Settings:` page; submit-then-ESC from description returns to the team
  detail. Assert `len(m.history)` grows/shrinks as navigation proceeds.
- Regression: a test that drives root→teams→detail→description→ESC and
  asserts the visible title at each step would have caught this.

**Validation steps:**
- Interactive: `/config` → Teams → a team → Description → ESC returns to the
  detail; ESC again returns to the Teams list; ESC again returns to Settings
  root — never a hard exit to the TUI.
- Gates (each run separately): `go vet ./...` · `staticcheck ./...` ·
  `gocognit -over 15 .` · `gocyclo -over 12 .` · `go test -count=1 -race -cover ./...`.

**Fix applied:**
- `core/commands/config.go`: added `openTeamsMenu` / `openOrchestratorMenu` /
  `openGoalsMenu` wrappers that push the root page via `m.open(...)`; the
  submenu handler map now points at them (previously `teams`/`orchestrator`/
  `goals` bypassed `m.open`).
- `core/commands/config_teams.go`: `openTeams` opens the team detail via
  `m.open(...)`; `openTeamDetail` opens each sub-page (description / review /
  gates / members / remove) via `m.open(...)`; `promptTeamField` now sets
  `m.current` and returns via `m.back()`; the review/gates completion
  callbacks return to the pushed detail via `m.back()` instead of re-invoking
  `openTeamDetail` directly.
- Tests: `TestConfigMenu_TeamsNavigationHistory`,
  `TestConfigMenu_TeamDetailEscReturnsToList`,
  `TestConfigMenu_TeamDescriptionEscReturnsToDetail` — all RED before the fix
  (ESC exited the menu), GREEN after.
- Gates green: `go vet ./...` ✓ · `staticcheck ./core/commands` ✓ ·
  `gocognit -over 15` / `gocyclo -over 12` on changed files ✓ ·
  `go test -count=1 -race -cover ./core/commands` ✓ (58.3%).