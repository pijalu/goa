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
     flows do not restate unrelated sections in the home file.
  4. Validate with the guideline-6 checks run separately; verify the
     /config compression display interactively per guideline 5 after a
     round-trip through each flow.