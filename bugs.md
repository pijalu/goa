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




## 5. Per-model compression override never triggers

**Observed:** a custom per-model compression configuration does not trigger.
Example: an override for a given model setting a 20% trigger with
hard/summarize strategies (`models.<id>.context_compression.thresholds.
trigger_percent: 20` + strategies, i.e. `ModelCompressionOverride`,
config/config_compression_types.go) never compresses — usage climbs past 20%
with no compression attempt on that model. Global compression settings fire,
so the suspect path is the override plumbing: config merge
(config/config_merge.go) → overlay application (core/compression_overlay.go)
→ threshold resolution (core/agentmanager_lifecycle.go
`resolveAgenticThresholds`) → SDK soft/hard mapping. Note the legacy
`threshold_percent` alias interactions (`legacyTrigger`, cleared-alias logic
in core/commands/config_cli.go) may shadow the tiered value.

**Expected:** per-model compression overrides take effect for that model:
when its context usage crosses the configured trigger (here 20%), compression
runs using the overridden strategy (hard/summarize), same semantics as the
global configuration.

## 6. Status line shows configured default provider instead of session provider

**Observed:** the provider displayed in the status line can differ from the provider currently used by the active session. It appears to fall back to the current configuration default rather than reflecting the provider selected for the running session.

**Expected:** the status line must show the provider actually used by the active session, including after provider/model switches, and remain consistent with the request path.

# TODO

*(open items tracked under `# To fix` above; move each to docs/archive when
tested and closed.)*