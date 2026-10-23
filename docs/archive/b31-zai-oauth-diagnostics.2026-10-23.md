<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# B31 — z.ai OAuth failure exposed only `zai_oauth_required`

Closed 2026-10-23. Archived from `bugs.md`.

## Reported and expected behavior

Reported visible flash:

```text
⚡ OAuth login for zai failed: oauth flow: zai_oauth_required
```

A failed z.ai OAuth login must name an actionable stage and sanitized cause while
retaining `errors.Is(err, ErrZaiOAuthRequired)` for programmatic callers. A
successful login must still mint and store both the family business token and the
zcode JWT. No token, poll credential, or authorization-URL secret may leak through
an error or log.

## Reproduction and root cause

The existing httptest harness was extended to replay broker initialization,
broker polling, and business-exchange failures. Before the fix:

* broker-init and broker-poll errors retained text such as `z.ai oauth init:
  broker denied …`, but had no `ErrZaiOAuthRequired` match and passed server or
  transport text through unsanitized;
* every business-exchange branch in `zaiExchangeBusinessToken` discarded its
  actual error and returned the bare sentinel. Denial, `success:false`, malformed
  JSON, a missing business token, and transport failure therefore all produced
  the reported flash;
* cancellation likewise had a message but no sentinel match.

The RED focused command was:

```sh
go test -count=1 -timeout 30s ./internal/agentic/provider/oauth \
  -run 'TestZaiLoginFailureDiagnostics|TestZaiLoginTransportFailureDiagnostics|TestZaiLoginBusinessFailureRedactsFlowSecrets|TestZaiLoginCancellationRetainsDiagnosticAndSentinel|TestBigModelLoginReturnsBothCredentials' -v
```

It failed all 13 stage cases, all three transport cases, cancellation, and the
secret-redaction case for the intended reasons. The BigModel success case passed.
The pre-fix visible-terminal test also failed after two seconds with the actual
frame:

```text
│ ⚡ OAuth login for zai failed: oauth flow: zai_oauth_required                                    │
```

The local ZCode reference was checked at
`packages/services/src/providers/zaiBusinessTokenResolver.ts`: its exchange
`catch`, rejected-envelope, and missing-token branches all throw the same bare
`zai_oauth_required`, matching the observed origin. The broker and business wire
contract also matched the reference (`/oauth/cli/init`, `/oauth/cli/poll/{id}`,
and `/api/auth/z/login`). No endpoint or payload change was indicated or made.

## Fix

`loginZaiFamily` now has one public error boundary. It accumulates only the
secrets already known at each stage and wraps every failure in
`zaiOAuthRequiredError`. The wrapper's multi-error `Unwrap` retains both the
original cause and `ErrZaiOAuthRequired`; its `Error` returns only the sanitized
diagnostic. Thus `errors.Is` compatibility does not depend on displaying or
sniffing the sentinel text.

The diagnostic path now:

* names `z.ai OAuth broker initialization`, `z.ai OAuth authorization polling`,
  or `z.ai OAuth business token exchange`;
* preserves server messages, HTTP status, malformed-envelope detail, missing
  credential detail, transport causes, and cancellation;
* retains `success:false` and missing-business-token messages instead of
  collapsing them to the sentinel;
* redacts known poll tokens, OAuth tokens, zcode JWTs, flow IDs, authorization
  URLs, URL query/userinfo values, and query/path-escaped variants;
* strips userinfo, query, and fragment credentials from other HTTP(S) URLs,
  neutralizes terminal control characters, flattens whitespace, and bounds the
  result to 400 runes.

The OAuth requests, endpoint selection, successful token mapping, and auth-store
writes are unchanged. No TUI message-text sniffing was added.

## Regression coverage

`internal/agentic/provider/oauth/zai_diagnostics_test.go` covers:

* z.ai and BigModel failures, including denied responses, browser denial,
  `success:false`, malformed envelopes, missing OAuth/zcode/business credentials,
  HTTP 502, and transport failures at all three stages;
* `errors.Is(err, ErrZaiOAuthRequired)` and actionable stage/cause text on every
  failure;
* poll/OAuth/zcode credentials, authorization URL state, URL userinfo/query
  secrets, and terminal controls not appearing in rendered error text;
* cancellation and BigModel's successful two-credential exchange.

Existing `TestZaiLogin_ReturnsBothCredentials` covers z.ai success, and
`TestZaiBrowserFlow_PersistsBothCredentials` covers persistence of both values
under the canonical `zai` auth-store key. The previous bare-string assertion in
`TestZaiLogin_BusinessLoginDeniedFails` was strengthened to check `errors.Is`
plus the stage and denial cause.

`core/commands/login_zai_filmstrip_test.go` drives the production OAuth flow and
asynchronous `LoginCommand` path against a gated local httptest server. It checks
that the engine remains responsive while the business request is parked and
after the failure, that no partial credential is stored, and that the visible
flash is actionable and secret-free. `OpenURL` is nil in the fixture, so no real
browser is launched.

## Visible-terminal evidence

The post-fix filmstrip ran normally and under `-race`. Its two captured states
included the live start/authorization panels, then this failure output:

```text
=== step 1: sanitized-business-denial ===
+ │ for [redacted] and [redacted]                                                                    │
+ │ ⚡ OAuth login for zai failed: oauth flow: z.ai OAuth business token exchange: business denied   │
```

The filmstrip records the wrapped continuation row; assertions cover the
actionable flash and credential absence across every visible row. Neither test
credential appeared in the visible frame.

## Validation actually run

Focused success and failure regressions passed. Targeted race validation also
passed:

```sh
go test -count=1 -race -timeout 60s ./internal/agentic/provider/oauth ./core/commands \
  -run 'Test(ZaiLogin(FailureDiagnostics|TransportFailureDiagnostics|BusinessFailureRedactsFlowSecrets|CancellationRetainsDiagnosticAndSentinel|ReturnsBothCredentials)|BigModelLoginReturnsBothCredentials|LoginZaiOAuthFailure_Filmstrip)$' -v
```

Result: both packages passed; the filmstrip above was emitted from that run.
Package runs passed:

```text
go test -count=1 -timeout 30s ./internal/agentic/provider/oauth
ok  github.com/pijalu/goa/internal/agentic/provider/oauth  13.446s

go test -count=1 -timeout 60s ./core/commands
ok  github.com/pijalu/goa/core/commands  4.038s
```

Required quality gates were run separately:

* `go vet ./...` — exit 0, no diagnostics (run again after the final test
  refactor).
* `staticcheck ./...` — exit 0, no diagnostics (run again after the final test
  refactor).
* `gocognit -over 15 .` — exit 1 for the same three unrelated pre-existing
  findings documented by the preceding B30 gate run: `GoalDriver.Drive` 21,
  `AgentManager.executeRunner` 16, and `app.initGoalSystem` 16. No B31 finding.
* `gocyclo -over 12 .` — the first run exposed a new score of 13 in the filmstrip
  test. The HTTP fixture was extracted and the test rerun. Final result: exit 1
  only for unrelated pre-existing `GoalDriver.Drive` 13; no B31 finding.
* `go test -count=1 -race -cover -timeout 180s ./...` — exit 0, all packages
  passed in 55.15s. Relevant reported coverage: OAuth 66.0%, core/commands
  65.2%, and TUI 77.0%.

All httptest servers were closed by test cleanup; the gated business handler is
released before server close even on failure. No `goa server`, browser, or
headed browser process was started.
