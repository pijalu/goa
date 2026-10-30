# B36 — Start Plan catalog additions left the preset-order gate stale

Date: 2026-10-30
Status: closed

## Report

The full race suite failed `config.TestPresetProviders_StableOrder` with
`PresetProviders() = 25 presets, want 23`. Commit `a1782e10` intentionally added
`zai-start-plan` and `bigmodel-start-plan` to the provider catalog with
wizard-addable base URLs but did not extend the exact-order expectation, despite
the test's documented contract requiring that update.

The expected behavior was for the stable-order test to name both intended Start
Plan presets in catalog order, without removing or hiding either provider to make
the old count pass.

## Fix

Added `zai-start-plan` and `bigmodel-start-plan` to the exact-order expectation
between `zai-api` and `poolside`. This updates only the stale test expectation;
the provider catalog and preset-selection behavior remain unchanged.

## Validation

Passed:

- `go test -timeout 30s ./config -run 'TestPresetProviders_(StableOrder|CoverCatalogProviders)' -count=1`
- `go test -count=1 -race -cover -timeout 60s ./config` (80.3% coverage)
- `go test -count=1 -race -cover -timeout 180s ./...`
- `go vet ./...`
- `staticcheck ./...`

Complexity gates report only unrelated pre-existing warnings:

- `gocognit -over 15 .`: `provider.TestResolveStartPlanAPIKey`,
  `core.(*GoalDriver).Drive`, `app.initGoalSystem`, and
  `core.(*AgentManager).executeRunner`.
- `gocyclo -over 12 .`: `startplan.TestDeviceMid_PersistsAndStable` and
  `core.(*GoalDriver).Drive`.

The changed expectation introduces no production-code complexity.

## Residual risk

The test continues to pin the complete preset order, so future intentional
catalog additions must update the expectation as documented. No provider behavior
changed in this fix.
