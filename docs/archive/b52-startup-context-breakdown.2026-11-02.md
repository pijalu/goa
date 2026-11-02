<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# B52 — Review startup context size for efficiency

Closed: 2026-11-02. Archived from `bugs.md`. Full breakdown and before/after
tables: `docs/plans/startup-context-breakdown-2026-11-02.md`.

## Report

No measurement existed of what the very first provider request actually
carries: system prompt sections, eager tool schemas (vs deferred), skills
catalog, embedded docs, project context files, mode overlays. Prior work (P1
deferred tool loading, docs/plans/context-usage-tool-skills-review.md) cut
tool-schema weight, but the remaining startup budget had never been audited
end to end, so waste grew silently with every feature.

## Resolution

Measured the fixed first-request payload end to end with a repeatable
breakdown (`StartupContextBreakdown`, internal/app/startup_context.go;
`go test -run TestStartupContextBreakdown -v ./internal/app`): per-component
bytes/tokens for every system-prompt section (post-budget) and every eager
tool schema over the real default config cascade.

Baseline: **31,351 bytes (~7,838 tokens)**. Top waste found: P1 deferral was
permanently dormant on default sets — `DeferralThreshold` (8) exceeded the
number of deferred-eligible tools the B46 minimal default tool set registers
(mcp, webfetch, run_skill, todo_list = 4), so their schemas all shipped
eagerly despite the deferred markers and the default config's own
documentation that `mcp` loads via tool search.

Applied reducer: `DeferralThreshold` 8 → 2 (internal/agentic/tool_registry.go).
After: **24,865 bytes (~6,217 tokens)** — **−6,486 bytes (~1,620 tokens) on
every request**. Withheld tools stay discoverable via the `<deferred_tools>`
prompt listing and `tool_search`; the headless companion tests now assert
"advertised **or** listed in `<deferred_tools>`".

Budget pinned: `TestStartupContextBudget` caps the total first-request
payload at 26,000 bytes (baseline + ~4.5% headroom); regressions fail CI
until a component is shrunk or the ceiling is raised with a fresh measured
breakdown.

Validation: full `go test -count=1 -race` suite green, `go vet` clean,
complexity/static checks clean, headless real-flow capture confirming
discoverability.
