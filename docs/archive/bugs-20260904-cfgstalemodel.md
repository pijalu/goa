# B-CfgStaleModel — dangling model references in teams config make startup fatal

Status: FIXED (2026-09-04)

## Observed

goa refuses to start and exits with:

```
Config error: validation errors (2):
teams.definitions.Loco.members.main.model: model "qwen3-8-27b" not found in models list
teams.definitions.Loco.members.companion.model: model "qwen3-8-27b" not found in models list
```

after the referenced model was deleted from the models list. The only
recovery is hand-editing the YAML.

## Root cause

Two halves —

1. Startup was all-or-nothing: `LoadConfig` (`internal/app/bootstrap.go`)
   treated every `internal.ValidationError` as fatal (`os.Exit(1)`), so a
   stale reference in an unused team definition bricked the whole app
   (`config/teams.go` `validateTeamMember`, same pattern for orchestrator
   roles at `config/config_validate.go` `validateRoles`).
2. Model deletion did not clean up references: both delete paths
   (`core/commands/model.go` `removeModelFromConfig`,
   `core/commands/config_models.go` `doRemoveModel`) dropped the model and
   the per-model compression override, but left
   `teams.definitions.*.members.*.model` (and `orchestrator.roles.*.model`,
   `orchestrator.pool.max_agents_per_model`) dangling.

## Fix (executed per plan)

1. **Catalog.** Every config field that references a model ID: team member
   `model` (`config/teams.go`), orchestrator role `model` and pool
   `max_agents_per_model` (`config/config_orchestrator.go` /
   `config_validate.go`), compression `per_model` (already healed by
   `sanitizeDanglingCompressionModels`), and `active_model` (already healed
   by `repairActiveProviderModel`, which re-creates a dangling active model
   so the session never loses its model).

2. **Heal-on-load.** Added `Config.sanitizeDanglingModelRefs`
   (`config/loader_overrides.go`), wired into `CascadeLoader.Load` before
   `Validate()` alongside the existing sanitize passes. Team member models
   and orchestrator role models that reference an unconfigured model are
   **rebound to the resolved active model** (`modelRefFallback`: active model
   when valid, else first configured model) — clearing to empty would itself
   fail the `model must be set` validation. Per-model pool caps for a deleted
   model are dropped. Each heal emits a stderr `Warning:` naming the field
   and the deleted model. The pass is a no-op when no models are configured
   (mirrors the validation `skipModelCheck`). Genuine config errors still
   fail validation and remain fatal.

3. **Delete-time cleanup.** Added the shared `Config.ClearModelReferences(id)`
   (`config/config_lookup.go`) — clears team member models, orchestrator role
   models, per-model pool caps, per-model compression overrides, and resets
   `active_model` — now called from both delete paths
   (`removeModelFromConfig`, `configMenu.doRemoveModel`) after the model is
   removed from the catalog. Member/role models are cleared (not rebound) on
   an explicit delete; startup healing covers references that predate the
   cleanup.

## Tests

RED first, then green:

- `config/loader_modelref_heal_test.go` — `TestLoadHealsDanglingTeamMemberModels`,
  `TestLoadHealsDanglingOrchestratorModelRefs`,
  `TestLoadKeepsConfiguredModelRefs` (overreach guard),
  `TestSanitizeDanglingModelRefsWarns` (stderr warning names the model),
  `TestLoadDanglingModelRefsGenuineErrorStillFatal` (bad `execution.mode`
  still fails even while a dangling ref is healed).
- `core/commands/model_remove_cleanup_test.go` —
  `TestRemoveModelFromConfig_ClearsAllModelRefs` and
  `TestDoRemoveModel_ClearsAllModelRefs` assert both delete paths leave zero
  dangling references (`danglingModelRefs` helper scans every model-bearing
  field).

## Validation (actual terminal output)

Built `/tmp/goa` and ran headless against a config whose only errors were
dangling model references:

- team companion + orchestrator role + pool cap all dangling → three
  `Warning:` lines, goa boots, reaches the LLM request (fails only on the
  expected no-API-key error), **exit 0**, no `Config error`.
- same dangling refs + a genuine `execution.mode: bogus` → the dangling ref
  is healed (warning shown) but startup still prints `Config error:
  validation errors … execution.mode …` and **exits 1**.

## Quality gates (each run separately)

- `go vet ./...` — exit 0
- `staticcheck ./...` — exit 0
- `gocognit -over 15 .` — no findings
- `gocyclo -over 12 .` — no findings
- `go test -count=1 -race -cover ./...` — 87 packages ok, 0 FAIL
