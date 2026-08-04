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

## All providers + models from models.dev should be present — OPEN

Only providers that already have a hand-curated `ProviderDef` with a
`ModelsDevKey` in the catalog (`schema.ProviderCatalog()`) are processed
from the models.dev runtime catalog (`modelsDevProviderMappings` in
`internal/agentic/provider/models/modelsdev.go`). Any provider that exists
on models.dev but lacks a catalog entry is **silently dropped** — the user
never sees it in the model picker or `/config`.

- **Symptom**: The models.dev catalog (`https://models.dev/api.json`)
 lists many providers (e.g. Together AI, Groq, Fireworks, Perplexity,
 AI21, Cohere, NVIDIA NIM, etc.). Goa only surfaces a subset (those with
 existing `ProviderDef` entries). Models from unmapped providers are
 invisible to the user even though the data is fetched and cached.
- **Root cause**: `buildModelsDevMappings()` iterates `ProviderCatalog()`
 and only emits a mapping for entries where `ModelsDevKey != ""`. The
 runtime parser (`parseModelsDev`) then skips any models.dev provider key
 not present in `modelsDevProviderMappings`. There is no fallback path
 that creates provider entries on the fly from models.dev data.
- **Relevant code**:
 - `internal/agentic/provider/models/modelsdev.go`:
   `modelsDevProviderMappings`, `buildModelsDevMappings`,
   `parseModelsDev`, `convertModelsDevModel`
 - `internal/agentic/provider/schema/catalog.go`:
   `providerCatalog` (the gated list of providers with `ModelsDevKey`)
 - `cmd/genmodels/main.go`: build-time generator uses the same gated
   `supportedProviders` list
- **Plan**:
 1. Add a fallback in the runtime parser: when a models.dev provider key
    has no explicit `ProviderDef` mapping, synthesize one using the
    models.dev metadata (provider name, base URL from the first model's
    endpoint, OpenAI-compatible API by default, auto-derived Goa
    provider/identity). This makes every models.dev provider visible
    without requiring a manual catalog entry for each.
 2. For providers that need special wire-level behavior (non-standard
    API, thinking format, etc.), the existing hand-curated `ProviderDef`
    with `ModelsDevKey` + `Compat` overrides still takes priority — the
    fallback only fills gaps for providers Goa knows nothing about.
 3. Test approach: table-driven tests asserting that a models.dev fixture
    with an unmapped provider produces runtime model entries with
    correct derived identity and base URL; existing mapped providers are
    unaffected.
 4. Validate per guideline 5 (pick a model from a previously-missing
    provider in `/config` or the model picker) and guideline 6.