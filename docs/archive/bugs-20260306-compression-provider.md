# Bugs 5 and 6

## Per-model compression override

Verified runtime overlay and model-switch application. `buildCompressionConfig`
resolves per-model thresholds/strategies and `SetModel` reapplies overrides even
when MaxTokens is automatic. Regression coverage:
`TestAgentManager_SetModel_AppliesPerModelOverride`.

## Session provider in status line

Footer provider data now comes from the live `ProviderManager.Active()` value,
with configured provider only as startup fallback. All main footer update paths
and usage records use `sessionProviderID`; regression coverage:
`TestSessionProviderID_UsesLiveProvider`.

Validation: `go test ./core ./internal/app` passed.
