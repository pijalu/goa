# B34 — Login panic boundaries

Date: 2026-10-30
Status: closed

## Report

An OpenCode quota failure suggested logging in, but `/login:opencode:oauth` was
reported to crash Goa. The required invariant is that login failure, including an
unexpected provider-flow panic, must never terminate the process.

## Investigation

The exact OpenCode invocation on this tree did not panic: the production
slash-command route returned `provider "opencode" does not support OAuth`.
OpenCode has no built-in OAuth flow in `LoginCommand.newOAuthFlow`.

A fatal login path did reproduce with an injected panicking OAuth flow:

```text
panic: oauth boom

github.com/pijalu/goa/core/commands.(*LoginCommand).runOAuthFlowSync
    core/commands/login.go:707
created by github.com/pijalu/goa/core/commands.(*LoginCommand).runOAuthFlow
    core/commands/login.go:685
```

The host's async-command recovery covers `Command.Run`, but `runOAuthFlow`
launches another goroutine after that boundary. Go panic recovery does not cross
goroutine boundaries, so the second goroutine terminated the process. The auth
picker's `runOffCommandLoop` continuation had the same defect. A direct
synchronous `LoginCommand.Run` call also had no command-local recovery boundary.

## Fix

`recoverLoginPanic` converts a recovered panic into an ordinary error. It is
installed independently at all three execution boundaries:

- synchronous `LoginCommand.Run`;
- auth-picker `runOffCommandLoop` goroutines;
- background OAuth-flow goroutines.

Existing error handling then returns the synchronous error or posts the normal
login failure flash. No provider behavior or credential storage semantics were
changed.

## Regression coverage

RED-before/ GREEN-after tests cover:

- `TestLoginCommandSyncOAuthPanicBecomesError`;
- `TestLoginCommandAsyncOAuthPanicBecomesError`;
- `TestLoginContinuationPanicBecomesError`;
- `TestLoginOpencodeOAuthUnsupportedDoesNotCrash`, through the real async app
  route and rendered chat output.

Before the fix, each of the first three tests terminated the test process with
its injected panic. After the fix, all return/report errors and pass.

## Validation

Passed:

- `go vet ./...`
- `staticcheck ./...`
- `go test -timeout 120s -race -cover ./core/commands ./internal/app -count=1`
  (`core/commands` 65.3%, `internal/app` 66.4%)
- `go test -count=1 -race -cover -timeout 180s ./...`

The full suite initially exposed B36, a stale preset-order expectation after two
intentional Start Plan catalog additions. Updating that expectation made the
final full-suite run green; B36 is tracked and committed separately.

Complexity gates report only unrelated pre-existing warnings:

- `gocognit -over 15 .`: `provider.TestResolveStartPlanAPIKey`,
  `core.(*GoalDriver).Drive`, `app.initGoalSystem`, and
  `core.(*AgentManager).executeRunner`.
- `gocyclo -over 12 .`: `startplan.TestDeviceMid_PersistsAndStable` and
  `core.(*GoalDriver).Drive`.

No changed login function appears in either complexity report.

## Residual risk

Recovery converts panics to user-facing errors but intentionally does not print
a stack trace into the TUI. Runtime fatal conditions that Go does not expose as
recoverable panics remain outside this boundary, as they are for the rest of the
application.
