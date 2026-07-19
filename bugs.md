<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Bug Tracking

## Guideline

1. Create a detailed fix plan for each bug - the plan must contain test approach and validation steps - execute the plan and validate the fix when all elements are in place.
2. Any issues found must be fixed and the fix plan must be updated accordingly.
3. Issues found during testing must be fixed and the fix plan must be updated accordingly.
4. Each bug should be moved to archive when tested and closed as the associated plan.
5. Use interactive shell to validate the output of the tool - you must verify the actual terminal output.
6. Check code quality with each tool run separately (do not chain them with `;` or `&&`):
   - `go vet ./...`
   - `staticcheck ./...`
   - `gocognit -over 15 .`
   - `gocyclo -over 12 .`
   - `go test -count=1 -race -cover ./...`
   Fix any new issues introduced by the change. Pre-existing warnings are acceptable only if they are unrelated to the change and explicitly noted.

At the end of the session - the bug list should be empty and this file should only contain the guidelines for bug reporting.
If new items are added, restart the process.

## Workflow for bugs
1. Reproduce the failure before editing — ideally a command or script that triggers it on demand.
2. State the observed failure exactly (command + output).
3. Localize to the smallest region — ideally the specific lines — before editing. Precise localization is the strongest predictor of a correct fix.
4. Change one hypothesis at a time.
5. Prefer the minimal fix over a broad refactor.
6. Verify against the original failing command before declaring done.
7. Run the code-quality checks from guideline #6 separately and confirm the fix does not introduce new violations.
8. Move the bug list to `docs/archive/bugs.<fixdate>.md` when all items are closed.

# Open Bugs

## Quota — provider switch still shows previous provider's limits (follow-up)

Switching to a local provider via /model (e.g. lmstudio google/gemma-4-e4b)
still shows the previous provider's quota (`[9%|30%]` from Kimi) in the footer,
even though the model line correctly shows the new provider.

### Root cause
Footer plugin segments are cached strings: `pushPluginSegments` only
re-evaluates the JS `Render()` when the plugin calls `goa.ui.refreshSegment`.
A /model provider switch fires `FooterRefresh` → `refreshFooterFromConfig`,
which rebuilds `FooterData` but PRESERVES the cached `PluginSegments` (by
design, so token-stats churn doesn't blank them) — so the stale quota string
persists until the plugin's next 60s tick. The earlier fix's test
(`TestQuota_ProviderSwitchUpdatesSegment`) called `renderSegment()` directly
and bypassed this app-level cache, so it passed while the live path stayed
broken.

### Fix plan
- In `refreshFooterFromConfig` (internal/app/events.go), re-push plugin
  segments via `pushPluginSegments` before rebuilding FooterData, so a
  provider/model change re-evaluates the segment against the new config.
- Test approach: app-level Filmstrip test that changes `cfg.ActiveProvider`
  and calls only `refreshFooterFromConfig` (no plugin `refreshSegment`),
  asserting the footer segment text switches and the stale value is gone.
- Validation: prove the test is RED without the fix (temp-revert), GREEN with
  it; then live PTY check of /model to a local provider.

**Status:** FIXED — see `docs/archive/bugs.2026-07-19.md` (quota section) and
`TestFilmstrip_ProviderSwitchRefreshesSegment`.
