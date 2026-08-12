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

**Status:** PARTIALLY FIXED — the safety-net (capability + actionable error) is implemented & tested, AND the PRIMARY root cause (team provider resolution, next entry) is now FIXED with unit tests. This entry stays open until the live e2e validation passes (VALIDATION REQUEST below).

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
- **Remaining for full close:** ~~fix the PRIMARY team-provider-resolution bug~~
  (DONE — next entry) + run the requested live e2e validation (last entry).

**Notes / open questions:**
- The temperature constraint is per-endpoint-model on kimi-code; the
  capability descriptor now lives in the variant profile (`supports_temperature`).
- The primary defect (team not selecting the model's provider) is tracked in
  the next bug entry.

---

## BUG: Team activation does not switch to the member model's provider — requests go to the wrong endpoint

**Status:** FIXED — implemented, tested, unit-validated. Live e2e validation
pending (see VALIDATION REQUEST below); this is the PRIMARY cause of the
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

**Fix applied:**
- `internal/app/team_adapters.go` (`teamSessionController.SwitchModel`):
  when the member's provider is empty, resolve the model's own configured
  provider via the new `providerIDForModelConfig` helper (mirrors
  `providerIDForModel` semantics in `core/commands/model.go`); fall back to
  `ActiveProvider` only when the model names no provider. Explicit member
  `provider:` remains highest priority.
- Same resolution in `teamMemberApplier.MemberConfig`: pool members with no
  `provider:` now resolve their model's configured provider so the pool's
  `ProviderModelFactory` lands companion/worker members on the right
  endpoint; models with no configured provider keep `ProviderID` empty
  (legacy pool default wiring preserved).
- Restore path unaffected: snapshot/restore already records prior
  provider+model.
- Tests (`internal/app/team_provider_resolution_test.go`, RED→GREEN through
  the full non-headless path with a recording ProviderManager):
  `TestTeamSessionController_SwitchModelResolvesModelProvider` (gemma on
  lmstudio activated from a kimi-code session → ActiveProvider==lmstudio,
  SetActive(lmstudio, gemma-local)), `..._SwitchModelExplicitProviderWins`,
  `..._SwitchModelFallsBackToActiveProvider` (model with no configured
  provider), `TestTeamMemberApplier_MemberConfigResolvesModelProvider`
  (companion), `..._MemberConfigExplicitProviderWins`,
  `..._MemberConfigNoConfiguredProviderStaysEmpty`.
- Gates green (each run separately): `go vet ./...` ✓ · `staticcheck ./...`
  pre-existing only, none from changed files ✓ · `gocognit -over 15 .` /
  `gocyclo -over 12 .` none on changed files ✓ · `go test -count=1 -race
  -cover ./...` ✓ (81 packages ok, 0 FAIL; internal/app 55.7%, core/team
  76.1%, core/commands 58.3%).
- **Remaining for full close:** live e2e validation per the request below.

---

## BUG: Activating a team persists `teams.active` to the HOME config instead of the project (local) config

**Status:** FIXED.

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

**Design decision (recorded):** `teams.active` persists to the project
**LOCAL** layer (`.goa/config.local.yaml` — gitignored, per-developer), NOT
the committed project `.goa/config.yaml`. A team is a project-scoped +
per-developer working set: the home layer leaks the selection across all
projects, and the committed project layer would dirty shared config with a
personal selection. Team *definitions* stay in the home config (user level).

**Fix applied:**
- `config/loader.go`: new `SaveLocalFieldValue(path, value)` on
  `ConfigSaver`/`CascadeLoader`, backed by a new `editLocalConfig`; the
  shared `editConfigFile` helper gained a filename parameter so home
  (`config.yaml`), project (`config.yaml`), and local (`config.local.yaml`)
  edits share one read-modify-write path (same `writeMu` serialization,
  minimal-document creation, field-scoped merge).
- `core/commands/team.go`: `persistActiveTeam` now calls
  `SaveLocalFieldValue(["teams","active"], name)` instead of
  `SaveHomeFieldValue` (covers `/team:<name>` and `/team:off`).
- `core/commands/config_teams.go`: `/config → Teams → Active team`
  (`openTeamsActive`) had the same leak via `saveTeamsSection` (whole
  "teams" section to home); it now persists only `teams.active` via the new
  `saveTeamsActive` → local layer. Definition CRUD still saves the section
  to home via `saveTeamsSection`.
- Cascade resolution order unchanged: embedded → home → project → local →
  env → flags; the local layer already wins for `teams.active` (most
  specific). No migration of stale home-layer values (harmless shadowed
  leftovers; the local layer overrides them on next activation).
- Test fakes (`core/commands/config_test.go`, `core/agentmanager_test.go`)
  gained `SaveLocalFieldValue` to satisfy the extended interface.

**Tests (RED→GREEN):**
- `config/loader_local_save_test.go` (new): `TestSaveLocalFieldValueWritesLocalLayerOnly`
  (local file carries the value; home + project configs untouched),
  `TestSaveLocalFieldValueCreatesFile`,
  `TestSaveLocalFieldValuePreservesOtherLocalSettings`,
  `TestCascadeLocalTeamsActiveResolvesOnStartup` (startup cascade resolves
  the local-layer value on reload).
- `core/commands/team_test.go`: `TestTeamCommand_ActivatePersistsToLocalLayer`
  (temp HOME + temp project dir: local file carries `active: alpha`; home
  carries only definitions; committed project config untouched; reload
  through the cascade resolves `alpha`),
  `TestTeamCommand_OffPersistsToLocalLayer` (clears to `active: ""` locally,
  home untouched),
  `TestConfigMenu_TeamsActivePersistsToLocalLayer` (the /config Active-team
  path writes only the local layer).

**Validation:**
- Gates (each run separately): `go vet ./...` exit 0 · `staticcheck ./...`
  pre-existing findings only (none in changed files) · `gocognit -over 15 .`
  0 findings in changed files (43 pre-existing elsewhere) · `gocyclo -over 12 .`
  0 findings in changed files (64 pre-existing elsewhere) ·
  `go test -count=1 -race -cover ./...` exit 0 (81 packages ok, 0 FAIL;
  config 79.0%, core/commands 58.5%).
- Interactive smoke (project A activate → home unchanged, project B
  unaffected) not run; covered by the unit tests above (temp HOME +
  per-project dirs).

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
