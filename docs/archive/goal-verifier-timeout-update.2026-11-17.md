# Goal verifier timeout may be lower than package suite runtime

**Reported:** 2026-11-17
**Resolved:** 2026-11-17

## Original report

**Observed:** Goal `verifyCommand` for compression fix was `go test ./internal/agentic -count=1 -timeout 30s`. It reproducibly timed out during unrelated package tests, while identical suite passed with a 120s timeout. User authorized model to update verifier timeout, maximum 600 seconds; goal metadata did not expose an update action, and repeated completion attempts reran unchanged 30s command.

**Expected:** Model can update an existing active goal’s `verifyCommand` timeout within user-authorized bounds, preserving command and scope; completion verifier then uses the updated value.

**Fix plan:** Add supported goal-update operation or allow `verifyCommand` replacement via existing goal update. Validate timeout bounds (user maximum 600s), update only verifier command, and confirm verifier executes the updated command. Avoid altering or skipping unrelated tests to fit too-short timeout.

## Fix

- Added active-goal verifier updates through `goal update verifyCommand`, with an expected-command compare-and-swap guard.
- Parsed replacement commands with the existing shell syntax parser. Only the `go test -timeout` value may change; executable, package/test scope, other flags, arguments, environment, and shell structure must remain equivalent.
- Supported separated and equals forms of `-timeout` and `--timeout`, with positive durations capped by the host-authorized verifier limit.
- Persisted updates in goal events so reload restores the corrected command and resets only the completion failure streak.
- Rejected completion when verification started under an older command.
- Kept verifier updates separate from lifecycle updates to avoid bypassing completion, pause, resume, and other status gates.
- Updated the tool prompt and goals documentation.

## Regression coverage

- Missing, unchanged, invalid, excessive, scope-changing, and flag-changing commands.
- Timeout-only updates with equivalent literal shell forms.
- Active-only updates, event persistence/replay, metadata preservation, and failure-streak reset.
- Successful completion with the corrected command and rejection of stale in-flight verification.
- Separate verifier/status actions and existing tool action/schema coverage.

## Validation

- `go test ./core/goal ./tools/goal -run VerifyCommand -count=1 -timeout 30s` initially failed to compile against the missing update operation, then passed after implementation.
- `go test ./core/goal ./tools/goal ./prompts/goal -count=1 -timeout 180s` passed.
- `go test ./core/commands ./tui/goal -count=1 -timeout 60s` passed.
- `go vet ./...` passed.
- `staticcheck ./...` passed.
- `go test -count=1 -race -cover ./...` passed.
- Complexity checks reported only pre-existing findings outside this change.
- ANSI-free `Filmstrip` validation exercised `goal create`, `goal update verifyCommand`, `/goal:current`, and the goal overlay. The overlay displayed only the updated timeout.

**Commit:** recorded separately with the implementation.
