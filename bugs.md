# Bug and feature Tracking

## Guideline
1. Create a detailed fix plan for each bug - the plan must contain test approach and validation steps - execute the plan and validate the fix when all elements are in place.
2. Any issues found must be fixed and the fix plan must be updated accordingly.
3. Issues found during testing must be fixed and the fix plan must be updated accordingly.
4. Each bug should be moved to docs/archive when tested and closed as the associated plan.
5. Use interactive shell/filmstrip to validate the output of the tool - you must verify the actual terminal output.
6. Check code quality with each tool run separately (do not chain them with `;` or `&&`):
- `go vet ./...`
- `staticcheck ./...`
- `gocognit -over 15 .`
- `gocyclo -over 12 .`
- `go test -count=1 -race -cover ./...`
Fix any issues.
! For cognitive and cyclomatic complexity, Pre-existing warnings are acceptable only if they are unrelated to the change and explicitly noted !

At the end of the session - the bug list should be empty, change committed and this file should only contain the guidelines for bug reporting.
If new items are added, restart the process.

Use goals to execute the fix plan - focus on micro tasks goals with new contextto lower context usage - use todos for micro tasks that should share context

Commit at the end of each fix with a clear and descriptive commit message

## Report format
Describe the bug or feature request under `# To fix` below. Keep one section
per item with a short title, the observed behavior, and the expected behavior.

# To fix

## Goal verifier timeout may be lower than package suite runtime

**Observed:** Goal `verifyCommand` for compression fix was `go test ./internal/agentic -count=1 -timeout 30s`. It reproducibly timed out during unrelated package tests, while identical suite passed with a 120s timeout. User authorized model to update verifier timeout, maximum 600 seconds; goal metadata did not expose an update action, and repeated completion attempts reran unchanged 30s command.

**Expected:** Model can update an existing active goal’s `verifyCommand` timeout within user-authorized bounds, preserving command and scope; completion verifier then uses the updated value.

**Fix plan:** Add supported goal-update operation or allow `verifyCommand` replacement via existing goal update. Validate timeout bounds (user maximum 600s), update only verifier command, and confirm verifier executes the updated command. Avoid altering or skipping unrelated tests to fit too-short timeout.

## Compression limit precedence and hard-tier/status mismatch — fixed

**Root cause:** Compression limit resolver preferred configured `MaxTokens` over known model capacity; status-bar independently displayed model capacity. Hard-tier gates correctly used conservative estimated usage, but status-bar percentage and some logs used projected occupancy, obscuring the hard-tier decision.

**Fix:** Known model context window (runtime-refreshed first, then model metadata) is now authoritative across compression and status-bar. Explicit compression `MaxTokens` is fallback only when model capacity is unknown. Status-bar percentage and hard-tier checks/logs report conservative estimated occupancy; the soft-tier gate remains provider-projected to preserve its cache behavior.

**Regression coverage:** Model-window precedence over larger/smaller configured limits; hard-tier triggers when estimate reaches hard ceiling despite lower projected usage; soft tier remains projection-based.

**Validation:** Focused agentic tests passed; `go test ./internal/agentic -count=1 -race -cover -timeout 120s` passed (86.3%); `go test ./... -count=1 -timeout 120s`, `go vet ./...`, and `staticcheck ./...` passed. Complexity checks report pre-existing unrelated warnings in app/provider/core/config.

## Auto-retry after provider silence menu value mismatch

**Observed:** Provider settings menu shows `Auto-retry after provider silence 90 (warn at 80)`, but editing the setting shows `120`. Related displayed settings: active provider retry cap `2m`, stream idle timeout `2m`, maximum retries `15 retries, 90 (warn at 80) stall retry`, and stall warning after provider silence `80`.

**Expected:** Menu and edit view show the same effective auto-retry-after-silence value. Determine intended default/configured value and make both views consistent; retain warning threshold `80` if applicable.

**Fix plan:** Trace default/config resolution and both settings render/edit paths. Add a regression test asserting consistency between displayed value and edit initial value; run focused tests and required checks.
