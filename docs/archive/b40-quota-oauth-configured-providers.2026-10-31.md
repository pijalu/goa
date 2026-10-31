# B40 — Quota recognizes z.ai OAuth and lists configured providers only

Closed: 2026-10-31

## Report

After `/login:zai:oauth` succeeded, `/quota` could continue to show the cached
`auth required — /login:zai:oauth` state instead of the Start Plan balance. The
table, JSON, and auth-status views also enumerated built-in fetchers for
providers absent from the user's configuration.

## Root cause and resolution

The quota plugin treated its built-in fetcher registry as the set of configured
accounts. It also reused an `auth_required` cache entry until the normal refresh
interval elapsed, even after the live auth store gained an OAuth credential.

Quota collection and rendering now resolve providers against the live
configuration. Refreshes skip unconfigured non-local fetchers, stale cache and
timestamp entries are pruned when configuration changes, and table, JSON,
auth-status, and ordinary quota completion output include configured providers
only. Login/logout discovery still exposes all supported providers.

A cached `auth_required` entry whose provider now has usable authentication
triggers the existing asynchronous refresh path on the next bare `/quota`.
The command remains nonblocking: it acknowledges immediately, then emits the
refreshed table through `goa.output`. Configured providers with genuine
collection or authentication failures remain visible with their diagnostics.

## Regression coverage

- `TestQuota_OAuthLoginRefreshesCachedAuthRequired` reproduces the cached
  logged-out state, persists the login-derived zcode JWT, and requires the next
  bare command to emit the real 60% balance without another login hint.
- `TestQuota_UnconfiguredProvidersStayHidden` covers table, JSON, auth-status,
  fetch avoidance, and cache contents while retaining a configured provider's
  real quota data.
- `TestQuota_ProviderRemovalPrunesCachedRows` proves live provider removal
  deletes stale rows and cache entries without hiding other configured rows.
- Existing quota fixtures now explicitly configure the providers they exercise;
  their behavioral assertions were not weakened.
- `TestQuotaPlugin_ZaiOAuthThenQuota_Filmstrip` uses the production auth store,
  provider manager, bundled plugin, slash-command dispatcher, app chat-event
  handler, and terminal renderer with a local HTTP fixture. The visible frame
  contains `Z.ai Start Plan`, `glm-visible`, and `60%`, with no
  `auth required`, `/login:zai:oauth`, Codex, or BigModel row.
- `TestQuotaPlugin_ExtendedInfoInTUI_Filmstrip` also asserts unconfigured
  providers never reach the visible table.

The filmstrip initially exposed missing event pumping in the new test scenario,
not a production delivery failure: direct plugin output was correct while the
test left the asynchronous `ChatEvent` queued. The scenario now drains events
through the production `handleChatEvent` path, matching `commandLoop`.

## Verification run

- RED: `go test -timeout 30s ./plugins -run 'TestQuota_(OAuthLoginRefreshesCachedAuthRequired|UnconfiguredProvidersStayHidden|ProviderRemovalPrunesCachedRows)' -count=1 -v`
  reproduced stale post-login auth state, unconfigured output/cache entries,
  and removed-provider state.
- GREEN: `go test -timeout 60s ./plugins -count=1` — PASS.
- Production terminal: `go test -timeout 30s ./internal/app -run TestQuotaPlugin_ZaiOAuthThenQuota_Filmstrip -count=1 -v` — PASS;
  the captured filmstrip visibly shows the refreshed Start Plan balance.
- Focused production quota filmstrips — PASS.
- `go test -timeout 120s ./plugins ./internal/app ./provider ./internal/auth ./internal/agentic/provider/... ./tui/...` — PASS.
- `go vet ./...` — PASS.
- `staticcheck ./...` — PASS.
- `go test -count=1 -race -cover -timeout 180s ./...` — PASS, all packages.
- `git diff --check` — PASS.

Complexity gates contain no new B40 findings. The remaining unrelated,
pre-existing reports are `TestResolveStartPlanAPIKey` 23, `GoalDriver.Drive` 21,
`initGoalSystem` 16, and `AgentManager.executeRunner` 16 for gocognit;
`TestDeviceMid_PersistsAndStable` 13 and `GoalDriver.Drive` 13 for gocyclo.
All HTTP fixtures were owned by `httptest.Server` and closed by test cleanup;
no validation server or browser process remains running.
