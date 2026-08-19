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

# TODO

All reported items from this cycle were implemented or verified, tested, validated, and archived under `docs/archive/`. Future reports must restart this process from a new detailed plan.

## Kimi-code cache/compaction comparison follow-up

RCA comparison archived at `docs/archive/bugs-20260819-kimi-cache-comparison.md`. Kimi-code uses the same core `prompt_cache_key` mechanism, but has stronger Kimi-specific cache-break telemetry, compaction baseline reset, idle-expiry UX, and explicit cache-key propagation tests. Potential Goa updates are tracked by priority in that report: Kimi wire-policy probe, key-preservation tests across tool/recovery/compaction requests, intentional tool-policy transition classification, and compaction-aware baseline tests.

## Cache-miss RCA follow-up: tool-collapse parameter mutation

The RCA of export `goa-export-20260819-090231.zip` found no message rewrite, reorder, stale-context resend, or conversation-ID change. All three misses occur exactly when a request changes from 15 tools with omitted `tool_choice` to no `tools` plus `tool_choice: "none"`. This intentional final-step collapse is a cache-relevant parameter mutation and is the strongest common cause; report 3 also has a 111.879s idle gap that could indicate TTL expiry. No cache-affinity hint was sent.

**Follow-up required:** instrument or test provider behavior for final-step tool collapse. Determine whether the collapse can preserve a cache-compatible request shape, or whether this transition must rotate/re-baseline cache identity and be excluded from miss counters. Do not remove final-step tool safety without compatibility tests. See `docs/archive/bugs-20260819-cache-rca.md`.