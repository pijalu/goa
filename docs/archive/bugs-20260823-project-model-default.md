# Bug: Last-used model not project-scoped; no usage-based default

**Date closed:** 2026-08-23
**Status:** FIXED (commit: per-project model persistence + usage-based boot default)

## Observed

Model switches persisted `active_provider` / `active_model` to the HOME config
first (`saveHomeProvidersAndModels`), so every project shared one global
last-used model; a project only got its own copy when `execution.auto_save_model`
was on **and** `.goa/config.yaml` already existed (the mirror refused to create
it). When no default model was configured at all, startup fell back to an
arbitrary provider default instead of learning from actual usage.

## Expected

- The last-used model is saved FIRST to the project layer (`.goa/config.yaml`,
  created when missing) so each project keeps its own active model; home stays
  the global fallback for projects without a pin.
- When no default model exists in any layer, boot picks the most-used model
  from the persistent usage stats instead of an arbitrary one.

## Root cause

1. The switch-persistence helper wrote home unconditionally and treated the
   project as an optional mirror gated on an existing file — project-first
   pinning (the highest-precedence cascade layer) never happened for new
   projects.
2. Boot resolution (`InitSubsystems` → `NewProviderManager`) had no fallback
   path consulting the usage store; an empty `active_model` simply surfaced the
   first provider's default.

## Fix plan

1. `config`: add `ConfigSaver.SaveProjectActiveModel(cfg)` — persists ONLY
   `active_provider` + `active_model` into `<project>/.goa/config.yaml`,
   creating the file via the existing node-edit machinery; no-op when no
   project dir is configured. Catalogs are intentionally NOT copied into
   project files (they stay global in home).
2. `core/commands`: rename the switch helper to `persistModelSwitch` and write
   PROJECT first, then HOME (global fallback). The project pin respects
   `execution.auto_save_model` (default true); home behavior is unchanged so
   opt-out users keep their current semantics.
3. `internal/app`: new `usage_default.go` — `applyUsageBasedDefaultModel(cfg,
   projectDir)` runs right after `LoadConfig`, before subsystem init. It acts
   only when `cfg.ActiveModel == ""` (CLI overrides already populate it),
   providers exist, and this isn't the first-run wizard path. Selection:
   query the usage store scoped to the project first, then globally; pick the
   top entry that maps to a configured model (by ID or wire name); set both
   `ActiveProvider` and `ActiveModel` as a coupled pair. Best-effort: errors
   are logged, never fatal.
4. Tests:
   - config: `TestSaveProjectActiveModel_*` — creates fresh file, updates
     existing pair, preserves other project keys, no-op without project dir,
     interface conformance of all fakes.
   - core/commands: fake-saver order assertion (project before home);
     real-loader end-to-end through `/model` selection asserting the pair is
     pinned in the project file AND reloaded from disk (feeds footer).
   - internal/app: table tests for `pickMostUsedModel` (token ordering, ID/wire
     matching, unknown-model skip, empty stats) plus sqlite-backed integration
     (project scope wins over global; empty store leaves config untouched).

## Validation

- `go test ./config ./core ./core/commands ./internal/app -count=1` — PASS
- Race suite on all touched packages — PASS
- Gates run separately: `go vet ./...` clean; staticcheck shows only the
  pre-existing SA1019 (`DefaultModel` deprecation, unrelated file, noted);
  gocognit/gocyclo counts match the pre-change baseline (3 / 23, all
  pre-existing in unmodified files).
- Persistence round-trip test doubles as footer validation: the reloaded
  project pair is exactly what `activeModelDisplay` renders in the statusbar.
