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

## Context-compression cascade: per-layer `strategies` never merged, `micro_compaction` replaced wholesale, `enabled: false` ignored, home `Save` full-dumps merged config — OPEN

- **Found during diagnosis** of the archived 2026-08-03 "startup /config
  does not match config" bug (docs/archive/bugs.20260803.md); all four are
  pre-existing latent config-merge/persist defects in the same code area:
  1. `mergeContextCompression` (`config/config_merge.go`) never merges the
     `Strategies` block (`CompressionLayerStrategiesConfig`): a
     `context_compression.strategies.{soft,trigger,hard}` key in a
     home/project/local config file is silently DROPPED on cascade load —
     only the embedded defaults survive. Verified: no `Strategies`
     assignment exists in `config_merge.go`.
  2. `MicroCompaction` is merged wholesale
     (`if cc.MicroCompaction != (MicroCompactionSettings{}) { c... = cc... }`):
     a higher layer that sets ONE micro key (e.g. `min_context_ratio`)
     resets every other micro key to zero/SDK-default, unlike the
     field-wise threshold merge right above it.
  3. The `if !cc.Enabled { return }` gate makes `enabled: false` in a
     home/project file a NO-OP: the embedded default (`enabled: true`)
     survives, so compression cannot be disabled from any config file
     (the /config `Enabled` toggle persists `enabled: false` to the home
     file, which is then ignored on the next load). Verified empirically:
     home file with `context_compression.enabled: false` loads as
     `Enabled=true`.
  4. `CascadeLoader.Save` (`config/loader.go`) still full-dumps the merged
     in-memory config to `~/.goa/config.yaml` (used by the model-manager
     and orchestrator settings flows via `m.saveConfig()`), baking
     project-layer and embedded values into the home file — the same
     contamination class as the archived bug, in the opposite direction.
- **Relevant code**: `config/config_merge.go` `mergeContextCompression`,
  `mergeCompressionThresholds` (contrast: field-wise), `mergeGoals` /
  tri-state patterns for booleans; `config/loader.go` `Save`,
  `SaveHomeFieldValue`; `core/commands/config_persist.go` `saveConfig`
  callers (`config_models.go`, `config_orchestrator.go`).
- **Plan**:
  1. `mergeContextCompression`: merge `Strategies` field-wise (non-empty
     wins per layer), merge `MicroCompaction` field-wise (non-zero scalars
     per field, matching the threshold merge), and handle `Enabled` as a
     tri-state (or gate only the *enabling* semantics) so an explicit
     `enabled: false` in a higher layer disables compression. Keep the
     gate's protection against a zero-value layer wiping the block.
  2. Replace the `Save` full-dump callers in the model-manager and
     orchestrator flows with field-scoped saves (`SaveHomeProvidersAndModels`
     for models, `SaveHomeFieldValue` for the orchestrator section); keep
     `Save` for flows that genuinely rewrite the home config.
  3. Tests (table-driven, RED first): strategies block survives a
     home+project cascade; micro keys merge field-wise across layers;
     `enabled: false` in home disables compression; `Save`-replacement
     4. Validate with the guideline-6 checks run separately; verify the
        /config compression display interactively per guideline 5 after a
        round-trip through each flow.

---

## No CLI way to relocate `~/.goa` (blocks isolated first-run testing) — OPEN

There is no way to point goa at a non-default home directory to test or
isolate a first run without disturbing the real `~/.goa`. (Bug A — the
wizard black screen — was fixed and archived to
`docs/archive/bugs.2026-08-04.md`.)

- **Symptom**: There is no `--home` flag (nor a `GOA_HOME` env override)
  to point goa at a home directory other than the OS user home. The only
  file-path override is `--config`, which sets an explicit *config file*
  (`configPath`), not the home root, so it does not relocate the cache,
  crash log, usage stats, or first-run detection. `GOA_HOME` appears in
  the codebase **only** as a string captured into log-export bundles
  (`internal/logs/export/bundle.go:289`) — it is never *read* as a home
  override. This makes reproducing/verifying any first-run change require
  juggling `HOME=`, which is fragile and also moves every other dotdir.
- **Root cause**: `NewCascadeLoader` (`config/loader.go:91-99`) hardcodes
  `homeDir, _ := os.UserHomeDir()` with no parameter or override; the
  home dir is then used to build `~/.goa/config.yaml` (first-run
  detection at `config/loader.go:165-168`, `loadHomeConfig` at
  `config/loader.go:172-180`) and `ConfigDir`.
- **Scope**: `os.UserHomeDir()` is called directly (non-test) in 14
  places, each then hardcoding the `.goa` (or `.agents`) subdirectory. A
  `--home` flag must override **all** of them for consistency, otherwise
  config/cache/logs/usage split across two roots:

  | File | Use |
  |------|-----|
  | `config/loader.go:92` | config load + first-run detection (`CascadeLoader.homeDir`) |
  | `config/config.go:1283` | config path resolution |
  | `config/defaults.go:41` | default skill dirs (`~/.agents/skills`) |
  | `internal/app/app.go:409` | models.dev cache (`~/.goa/cache`) |
  | `internal/app/crash_log.go:73` | crash log path |
  | `internal/usage/usage.go:90` | usage stats |
  | `internal/agentic/provider/schema/loader.go:73` | provider schema cache |
  | `internal/spinner/spinner.go:95` | spinner state |
  | `internal/logs/export/bundle.go:344` | log export |
  | `internal/sandbox/manager.go:48` | sandbox home |
  | `tools/renderer_common.go:130` | tool rendering (path display) |
  | `tools/memento.go:125` | memento store |
  | `tools/common/search_priority.go:57` | search priority |
  | `tools/readfile.go:226` | readfile (path display) |
- **Plan**:
  1. Introduce a single resolution point for the goa home dir: a
     `--home` flag (priority front of the cascade: flag → `GOA_HOME` env
     → `os.UserHomeDir()`), threaded into `NewCascadeLoader` and exposed
     to the subsystems/tools that currently call `os.UserHomeDir()`
     directly. Prefer a shared helper (e.g. `config.GoaHome()`) over
     touching all 14 sites ad hoc, so future callers use one source.
  2. Test approach: table-driven tests asserting that `--home`/`GOA_HOME`
     relocates config load, first-run detection, and the cache path;
     a first-run under a temp `--home` launches the wizard from that
     root.
  3. Validate per guideline 5 (`--home /tmp/x goa` on a clean root shows
    the wizard and writes nothing to the real `~/.goa`) and guideline 6.

---

## All providers + models from models.dev should be present — FIXED

Only providers that already have a hand-curated `ProviderDef` with a
`ModelsDevKey` in the catalog (`schema.ProviderCatalog()`) are processed
from the models.dev runtime catalog (`modelsDevProviderMappings` in
`internal/agentic/provider/models/modelsdev.go`). Any provider that exists
on models.dev but lacks a catalog entry was **silently dropped** — the user
never sees it in the model picker or `/config`.

- **Symptom**: The models.dev catalog (`https://models.dev/api.json`)
 lists many providers (e.g. Together AI, Groq, Fireworks, Perplexity,
 AI21, Cohere, NVIDIA NIM, TensorX, etc.). Goa only surfaced a subset
 (those with existing `ProviderDef` entries). Models from unmapped
 providers were invisible to the user even though the data was fetched
 and cached. Concrete example: TensorX (25 models) absent.
- **Root cause**: `buildModelsDevMappings()` iterates `ProviderCatalog()`
 and only emits a mapping for entries where `ModelsDevKey != ""`. The
 runtime parser (`parseModelsDev`) then skipped any models.dev provider
 key not present in `modelsDevProviderMappings`. There was no fallback
 path that creates provider entries on the fly from models.dev data.
- **Fix**: Added a two-pass `parseModelsDev`: Pass 1 processes mapped
 providers (hand-curated, detailed compat) as before. Pass 2 iterates
 every remaining models.dev key and synthesizes a mapping from the
 provider-level metadata (`api` URL for BaseURL, `npm` package for wire
 protocol, provider key for identity). The synthesized identity matches
 `DeriveProviderID` for the endpoint, so `ListRegistryModels` finds the
 models when a user configures that provider. Extracted
 `addProviderModels` and `synthesizeMapping` helpers to keep complexity
 in budget.
- **Tests**: `TestParseModelsDev_UnmappedProviderFallback` (RED→GREEN):
 fixture with an unmapped provider (tensorx) produces runtime catalog
 entries with correct provider identity, BaseURL, API type, and model
 metadata. Verified with the real models.dev cache: all 25 TensorX
 models now visible under `provider.Provider("tensorx")`.
- **Filmstrip validation**: `internal/app/modelsdev_providers_filmstrip_test.go`
 proves "all providers from models.dev are visible in the TUI":
 - `TestModelsDev_AllProvidersReachableInPickerData` iterates every
   models.dev provider in the embedded api.json (via the new
   `models.ModelsDevProviders()` enumeration) and asserts
   `ProviderManager.ListRegistryModels` surfaces each provider's
   tool-calling models — the data source the `/model:add` picker reads.
 - `TestModelsDev_ProviderPickerShowsModelsDevProviders_Filmstrip` drives
   the real `/model:add` command and filmstrip-captures the provider
   picker, proving the models.dev-configured providers are selectable rows.
 - `TestModelsDev_ModelsVisibleInTUIPicker_Filmstrip` renders the exact
   item set `ListRegistryModels` feeds the model picker through the real
   TUI Selector for a mapped provider (zai), an unmapped-fallback
   provider (tensorx), and deepseek, then asserts from the scrolled
   filmstrip that every models.dev model ID for that provider is visible.
- **Validation**: `go vet`, `staticcheck`, `gocognit`, `gocyclo`, and
 `go test -count=1 -race -cover` all pass for affected packages; no
 existing tests broken.