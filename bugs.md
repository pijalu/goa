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
