# Per-model idle-summarize overrides

**Status:** closed and validated on 2026-11-27.

## Observed behavior

`context_compression.idle_summarize.trigger_percent` and `delay_seconds` were
only global. Models could not use different idle policies, and one model could
not explicitly disable idle summarization while another inherited the global
policy.

## Expected behavior

Both fields are configurable under
`context_compression.per_model.<models[].id>.idle_summarize`. Absent pointer
fields inherit from the cascade/global configuration. Explicit
`trigger_percent: 0` permits any nonempty history; explicit `delay_seconds: 0`
disables idle summarization only for that model. Effective per-model compression
enablement continues to gate the timer.

## Reproduction and implementation plan

1. Add failing configuration tests for YAML decode/deep-copy, field-wise cascade
   merging, validation while global compression is disabled, explicit zeroes,
   and clear-to-inherit through `/config:set`.
2. Add failing manager tests for stable model-ID resolution, model switching,
   live refresh, and a real scheduler E2E where a zero-delay model does not
   compact while an enabled model does.
3. Extend the existing per-model compression overlay rather than creating a
   parallel runtime configuration path.
4. Add both fields to `/config → Compression → Advanced → Per-model overrides`,
   persist them through the existing typed setters, and exercise the actual
   terminal selectors and YAML reload.
5. Document YAML, command syntax, stable IDs, inheritance, and zero semantics.

## Root causes and implementation

- `ModelCompressionOverride` now contains `IdleSummarizeSettings`; merge and
  validation reuse the global pointer-aware helpers. Every per-model block is
  validated before the legacy global-disabled early return.
- `overlayCompressionForModel` carries the effective idle settings, overlays
  only non-nil fields, and resolves overrides through the stable configured
  `models[].id`. Existing `SetModel` and `RefreshContextCompression` paths push
  the resulting settings live. Compression disablement still forces delay zero.
- Dynamic `/config:set` fields support both settings. Shared delay parsing keeps
  numeric zero distinct from the per-model empty clear operation.
- Global and per-model pickers share option builders. Per-model rows display
  inherited and explicit values and support independent clear-to-inherit.
- Filmstrip testing exposed an existing selector ambiguity: an empty selector
  value means Escape/cancel, so it cannot also mean **inherit (clear)**. All
  per-model selector-based inherit rows now use the nonempty internal action
  `__inherit__`; only the setter boundary translates it to an empty override.
  Text-input clearing and Escape behavior are unchanged.

## Regression coverage

- `config/idle_summarize_permodel_test.go`: YAML, deep-copy, cascade, explicit
  zero, and validation paths.
- `core/agentmanager_idle_summarize_test.go`: stable ID versus API name,
  inheritance, custom values, explicit disable, model switching, live refresh,
  compression gating, and real scheduler behavior.
- `core/commands/config_idle_summarize_permodel_test.go`: setters, picker rows,
  zero/clear behavior, persistence round-trip, and independent fields.
- `internal/app/config_compression_filmstrip_test.go`: real terminal navigation,
  selection of 25% and off, YAML reload, actual **inherit (clear)** selection,
  and preservation of the other field. Existing global compression filmstrips
  and Escape behavior were rerun.

## Validation results

- Focused config, runtime, scheduler, command, and terminal-filmstrip tests pass
  under the race detector.
- `go vet ./...`: pass.
- `staticcheck ./...`: pass.
- `gocyclo -over 12 .`: pass with no findings.
- `gocognit -over 15 .`: no changed-code findings. It exits nonzero only for six
  known unrelated pre-existing functions: `TestResolveStartPlanAPIKey` (23),
  `TestAgentMCPCloseRemovesExtractedArtifacts` (23),
  `TestMakeToolFactory_BuildsEveryConfigurableTool` (19),
  `AgentManager.executeRunner` (16), `initGoalSystem` (16), and
  `TestConfigurableTools_AllDefaultsMirrorEmbeddedConfig` (16).
- `go test -count=1 -race -cover -timeout 180s ./...`: pass across the full
  repository. Config coverage is 84.3%.
- `git diff --check`: clean.
- No validation server or browser was started, so no validation process cleanup
  was required.

## Residual risk

No known functional residual. Idle scheduling remains subject to the existing
provider/model configuration and the intentional compression-enablement gate.
