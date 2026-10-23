<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# B33 — z.ai business success became an OAuth failure and quota stayed unauthenticated

Closed 2026-10-23. Archived from `bugs.md`.

## Reported and expected behavior

After z.ai authentication, the quota table still reported
`coding_plan_reset_zcode_jwt_required` and advised manually configuring
`providers[].extra.zcodeJwt`. Login simultaneously displayed:

```text
⚡ OAuth login for zai failed: oauth flow: z.ai OAuth business token exchange: Operation successful
```

A successful exchange must return and persist both credentials, show no failure
flash, and let the managed Coding Plan quota credential path work without manual
configuration. Genuine denials and malformed responses must remain failures.

## Reproduction and root cause

`TestZaiLogin_BusinessCode200Succeeds` replays an HTTP-success business response
with this envelope shape:

```json
{
  "code": 200,
  "success": true,
  "msg": "Operation successful",
  "data": {"access_token": "business-token-200"}
}
```

Before the fix, the focused test failed with the exact reported diagnostic:

```text
loginZai rejected business code 200: z.ai OAuth business token exchange: Operation successful
```

`zaiExchangeBusinessToken` already had the correct business-code predicate:
`zaiBusinessCodeOK` accepts an omitted code, 0, or HTTP-style 200. However, the
request first passed through shared `zaiRequest`, which rejected every explicit
non-zero code. Consequently, code 200 was rejected before the business parser and
its correct predicate could run. The local ZCode reference independently confirms
that 0 and 200 are successful business codes.

Because the exchange returned an error, `loginZaiFamily` returned no token pair and
`LoginCommand` did not store a new credential. The quota error was therefore the
downstream missing-credential symptom, not a separate quota lookup defect.

## Fix

The shared HTTP/JSON request path now accepts an endpoint-specific code policy:

* broker initialization and polling retain their existing code-0 behavior and do
  not treat code 200 as success;
* business exchange uses `zaiBusinessCodeOK`, accepting omitted code, 0, and 200;
* `success:false`, denied non-success codes, malformed JSON, transport failures,
  and missing tokens retain their existing actionable error paths.

No credential-store or quota-plugin production change was needed.

## Regression and integration evidence

* `TestZaiLogin_BusinessCode200Succeeds` now passes and returns both the exchanged
  business token and broker-issued zcode JWT.
* `TestLoginZaiOAuthBusinessCode200_Filmstrip` drives the real broker → poll →
  business flow through asynchronous `LoginCommand`. It verifies that both values
  reach the real auth store and that the visible terminal shows
  `OAuth login for zai succeeded.` with no failure flash or `Operation successful`
  error. The test passed under `-race`.
* Existing `TestZaiBrowserFlow_PersistsBothCredentials` covers the store wiring;
  existing `TestQuotaZaiCodingPlan_GoaLoginUnblocksResets` and
  `TestQuotaZaiCodingPlan_ManagedJwtPairedWithManagedToken` verify that the same
  managed pair unlocks Coding Plan resets without manual `zcodeJwt` configuration.
  Both quota tests passed directly and in the full suite.
* Existing B31 denial, malformed-response, transport, cancellation, redaction, and
  BigModel tests remained green, so the new success convention did not weaken
  failure handling.

## Validation actually run

* Focused RED/GREEN OAuth test and business-code-200 command filmstrip — passed after the fix.
* `go test -count=1 ./internal/agentic/provider/oauth ./core/commands` — passed.
* `go test -count=1 -race ./internal/ansi ./tui ./internal/agentic/provider/oauth ./core/commands` — passed.
* Direct managed-credential quota tests — passed.
* `go test -count=1 -race -cover -timeout 180s ./...` — passed; final run 56.44s.
* `go vet ./...` — exit 0, no diagnostics.
* `staticcheck ./...` — exit 0, no diagnostics.
* `gocognit -over 15 .` — only unrelated pre-existing `GoalDriver.Drive` 21,
  `AgentManager.executeRunner` 16, and `app.initGoalSystem` 16 after the B32 scanner
  refactor; no B33 finding.
* `gocyclo -over 12 .` — only unrelated pre-existing `GoalDriver.Drive` 13.

No real account business exchange was performed; the exact reported response was
replayed through local HTTP fixtures and the reference contract was checked. All
httptest servers were closed by test cleanup. No browser, `goa server`, or persistent
validation server was left running.
