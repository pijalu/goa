# mcp tool not enabled although the shipped default is ON

**Date:** 2026-11-07
**Area:** `config/loader.go` (`CascadeLoader.Save`), `config/loader_save_prune.go`

## Observed

`tools.enabled.mcp` ships ON (`config/configs/default.yaml`, guarded by
`TestDefaultConfig_MCPToolEnabled`) and a fresh cascade load reports
`mcp = true`, but the live install reported `mcp = false`, so
`registerAgentMCPTool` (`internal/app/bootstrap_tools.go`) never registered the
tool. `~/.goa/config.yaml` pinned `tools.enabled.mcp: false` (and
`todo_list: false`, another flag whose default flipped).

## Root cause

`CascadeLoader.Save` marshaled the WHOLE in-memory config into the home layer.
Every save (first-run wizard, `/config`, `/goal:settings` autosave, model
switch) therefore froze the then-current value of *every* key as an explicit
home pin — including keys the user never touched. When a shipped default later
changed, the frozen pin kept winning: `mcp: false` was the default in f11bb2a2,
flipped to `true` in 3237ee5f, and every install that had saved a config before
the flip stayed OFF forever.

## Fix

`Save` now writes a **delta**: `marshalConfigDelta` marshals the config, then
prunes every node whose value equals the shipped default
(`pruneShippedDefaults` → `pruneDefaultsNode` → `yamlNodesEqual`). Only real
deviations are persisted, so untouched settings keep following the shipped
defaults while explicit choices keep winning. A config that is entirely default
writes an empty mapping — the file still exists, which is what ends first-run
detection.

The baseline is the embedded default config **run through the same
normalization pipeline** as a real load (new `(*CascadeLoader).normalize`,
extracted from `loadCascade`: env interpolation, env/CLI overrides, legacy
migrations, dangling-reference sanitizers). A plain parse of `default.yaml`
would treat derived values — `mode.defaults.coder` produced from a legacy
`execution.mode`, for example — as user choices. Remote registries are
deliberately not fetched: a config save must never depend on the network.

Cleanup of the already-poisoned live config: the stale
`tools.enabled.mcp: false` pin was removed with the app's own
`DeleteHomeField` (backup written next to the file), so the shipped default
applies again. `tools.enabled.todo_list: false` is left in place (same stale
origin, but re-enabling a tool the user may have meant to keep off is their
call).

## Regression coverage

`config/loader_save_prune_test.go`:

- `TestSave_PrunesShippedDefaults` — a default-equal save pins no keys
  (`mcp`, `todo_list`, `webfetch`, `retries`, `activity_timeout`).
- `TestSave_KeepsExplicitChoices` — `mcp: false` / `retries: 9` survive save and
  reload.
- `TestSave_DeltaRoundTripKeepsEffectiveValues` — the pruned save reloads to the
  same effective config, with default-ON tools still on.
- `TestSave_AllDefaultsWritesEmptyMapping` — an all-default save still creates a
  loadable home file.
- `TestSave_KeepsUserDataOutsideDefaults` — user-owned `mcp` server definitions
  and aliases are never pruned.

Existing guards re-run green: `TestFreshInstall_FlagsPinsDefaultOnAndOff`,
`TestFreshInstall_RegistryHasMCPOnlyDefaults` (the boot registration path really
registers `mcp` on a fresh install).

## Validation

- Real cascade load of the live install after cleanup:
  `tools.enabled.mcp = true`, `tools.enabled.todo_list = false`,
  `tools.enabled.python = true`, `tools.enabled.webfetch = true`.
- Fresh install (sandboxed `HOME`): `tools.enabled.mcp = true`.
- `go vet ./...`, `staticcheck ./...`, `go test -count=1 -race ./...` all pass.

## Follow-up found while fixing (not part of this bug)

`Config.DeepMerge` does not merge `aliases` (no `mergeAliases`) and a handwritten
`mcp:` block in the home layer did not reach the merged config in a targeted
probe — both predate and are unrelated to the save-delta change. Worth a separate
entry.
