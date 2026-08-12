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

**Status:** OPEN — logged, root cause identified, fix plan pending.

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

**Root cause (from the export):**
- `config/user.yaml`: model `google/gemma-4-e4b` sets `temperature: 0.2`
  (provider `lmstudio`); the active team binds this model. Session is routed
  through provider `kimi-code` (`active_provider: kimi-code`, endpoint
  `https://api.kimi.com/coding/v1`).
- `logs/http.jsonl`: POST to `…/coding/v1/chat/completions` for
  `google/gemma-4-e4b` returns **400** `invalid temperature: only 1 is
  allowed for this model`. The kimi-code endpoint only accepts `temperature == 1`.
- So Goa sends the model's configured `temperature: 0.2` to an endpoint that
  hard-rejects anything but `1`. The request is built from the model's stored
  temperature without validating/clamping against what the target endpoint
  accepts.

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

**Notes / open questions:**
- Is the temperature constraint per-provider (kimi-code) or per-model
  (gemma-4-e4b)? The error text ("only 1 is allowed for this model") suggests
  model-level on that endpoint. Decide where the capability descriptor lives.
- Related to team model binding: activation should validate the bound model's
  params against the provider before applying (pre-flight), not at first turn.

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